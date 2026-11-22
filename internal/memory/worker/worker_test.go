package worker_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Twinkle661/NamiDiary/internal/diary"
	"github.com/Twinkle661/NamiDiary/internal/llm"
	"github.com/Twinkle661/NamiDiary/internal/memory"
	"github.com/Twinkle661/NamiDiary/internal/memory/agents"
	"github.com/Twinkle661/NamiDiary/internal/memory/backup"
	"github.com/Twinkle661/NamiDiary/internal/memory/worker"
	"github.com/Twinkle661/NamiDiary/internal/testutil"
)

type provider struct {
	mu       sync.Mutex
	calls    int
	messages [][]llm.Message
	entered  chan struct{}
	release  chan struct{}
}

func (p *provider) Complete(ctx context.Context, agent string, m []llm.Message) (string, error) {
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	p.messages = append(p.messages, append([]llm.Message{}, m...))
	p.mu.Unlock()
	if first && p.entered != nil {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return `{"actions":[]}`, nil
}

type reader struct {
	mu      sync.Mutex
	queries []diary.LatestQuery
}

func (r *reader) Latest(ctx context.Context, q diary.LatestQuery) ([]diary.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, q)
	out := []diary.Entry{}
	for _, date := range []string{"2026-01-01", "2026-01-03", "2026-01-10"} {
		if q.Until == nil || date <= *q.Until {
			out = append(out, diary.Entry{Date: date, Body: "diary"})
		}
	}
	if len(out) > q.N {
		out = out[len(out)-q.N:]
	}
	return out, nil
}
func wait(t *testing.T, w *worker.Worker, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, err := w.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !s.Busy && !s.PendingDream && len(s.LastJobs) >= n {
			return
		}
		time.Sleep(time.Millisecond * 5)
	}
	s, _ := w.Status(context.Background())
	t.Fatalf("worker did not finish: %+v", s)
}
func TestBusyReadsAndCoalescing(t *testing.T) {
	s, c := testutil.Store(t)
	p := &provider{entered: make(chan struct{}), release: make(chan struct{})}
	r := &agents.Runner{Store: s, Config: c.Memory, Diary: &reader{}, Client: p, Location: time.UTC}
	w := worker.New(context.Background(), s, r, backup.New(s, c.App.DataDir, c.Memory.Backup.Retention))
	t.Cleanup(w.Stop)
	w.EnqueueDream("first")
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("no job")
	}
	for i := 0; i < 20; i++ {
		w.EnqueueDream("next")
	}
	if _, err := w.Apply(context.Background(), memory.User, []memory.Action{{Op: "add", Topic: "profile", Content: "fact"}}); err == nil || !strings.Contains(err.Error(), "MEMORY_BUSY") {
		t.Fatalf("write busy %v", err)
	}
	if err := w.Request("init", 0, ""); err == nil {
		t.Fatal("job accepted while busy")
	}
	if _, err := s.Library(context.Background()); err != nil {
		t.Fatal("read blocked")
	}
	close(p.release)
	wait(t, w, 2)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls != 2 {
		t.Fatalf("got %d calls", p.calls)
	}
}
func TestInitReplayUntilDates(t *testing.T) {
	s, c := testutil.Store(t)
	p := &provider{}
	d := &reader{}
	r := &agents.Runner{Store: s, Config: c.Memory, Diary: d, Client: p, Location: time.UTC}
	w := worker.New(context.Background(), s, r, backup.New(s, c.App.DataDir, c.Memory.Backup.Retention))
	t.Cleanup(w.Stop)
	if err := w.Request("init", 3, ""); err != nil {
		t.Fatal(err)
	}
	wait(t, w, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.messages) != 3 {
		t.Fatalf("passes %d", len(p.messages))
	}
	for i, date := range []string{"2026-01-01", "2026-01-03", "2026-01-10"} {
		if !strings.Contains(p.messages[i][1].Content, "<today>"+date+"</today>") {
			t.Fatal("wrong historical date")
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.queries) != 4 {
		t.Fatalf("queries %d", len(d.queries))
	}
	for i := 1; i < 4; i++ {
		if d.queries[i].Until == nil {
			t.Fatal("historical context lacks until")
		}
	}
}
func TestExternalViolationQueuesMaintenance(t *testing.T) {
	s, c := testutil.Store(t)
	s.Config.Limits.Profile.MaxEntries = 1
	c.Memory.Limits.Profile.MaxEntries = 1
	p := &provider{}
	r := &agents.Runner{Store: s, Config: c.Memory, Diary: &reader{}, Client: p, Location: time.UTC}
	w := worker.New(context.Background(), s, r, backup.New(s, c.App.DataDir, c.Memory.Backup.Retention))
	t.Cleanup(w.Stop)
	_, err := w.Apply(context.Background(), memory.User, []memory.Action{{Op: "add", Topic: "profile", Content: "one"}, {Op: "add", Topic: "profile", Content: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	wait(t, w, 1)
	status, _ := w.Status(context.Background())
	if status.LastJobs[0].Type != "maintenance" || status.LastJobs[0].OK || status.LastSnapshot == "" {
		t.Fatalf("maintenance status %+v", status)
	}
}
