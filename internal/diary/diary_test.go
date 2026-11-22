package diary

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/testutil"
)

func service(t *testing.T) (*Service, *testutil.Embeddings) {
	t.Helper()
	c := testutil.Config(t)
	e := &testutil.Embeddings{}
	s, err := Open(c, e)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	s.Now = func() time.Time { return time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC) }
	return s, e
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *apperr.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s; got %v", code, err)
	}
}
func TestMarkdownCompatibility(t *testing.T) {
	for _, loc := range []string{"", "A city"} {
		e := Entry{Date: "2026-01-01", Created: "2026-01-01T00:00:00.000Z", Updated: "2026-01-01T00:00:00.000Z", Keywords: []string{"reading", "歩く"}, Summary: "A summary", Location: loc, Body: "hello\n\nworld"}
		parsed, err := Parse([]byte(e.Markdown()))
		if err != nil || parsed.Markdown() != e.Markdown() {
			t.Fatalf("round trip %v", err)
		}
		if loc == "" && strings.Contains(e.Markdown(), "location:") {
			t.Fatal("empty location written")
		}
	}
	e, err := Parse([]byte("---\ndate: 2026-01-01\nkeywords: reading\nsummary: null\n---\nBody\n"))
	if err != nil || len(e.Keywords) != 1 || e.Date != "2026-01-01" || e.Summary != "" {
		t.Fatalf("legacy parse %+v %v", e, err)
	}
}
func TestAddPatchLatestAndIndex(t *testing.T) {
	s, emb := service(t)
	ctx := context.Background()
	hooks := 0
	s.OnAdded = func(string) { hooks++ }
	for _, date := range []string{"2026-01-09", "2026-01-11"} {
		if err := s.Add(ctx, date, WriteRequest{Content: "Body reading", Meta: Meta{Summary: "summary", Keywords: []string{"book"}, Location: " City "}}); err != nil {
			t.Fatal(err)
		}
	}
	if hooks != 2 || len(emb.Inputs) != 2 {
		t.Fatalf("hooks=%d embeds=%d", hooks, len(emb.Inputs))
	}
	requireCode(t, s.Add(ctx, "2026-01-12", WriteRequest{Content: "body"}), "DATE_TOO_FAR_AHEAD")
	requireCode(t, s.Add(ctx, "2026-01-11", WriteRequest{Content: "body"}), "CONFLICT")
	if err := s.Patch(ctx, WriteRequest{Content: "New", Meta: Meta{Keywords: []string{"book", "new"}}}); err != nil {
		t.Fatal(err)
	}
	e, err := s.Get(ctx, "2026-01-11")
	if err != nil || e.Body != "Body reading\n\n---\n\nNew" || e.Location != "City" || strings.Join(e.Keywords, ",") != "book,new" {
		t.Fatalf("patch %+v %v", e, err)
	}
	if hooks != 2 || len(emb.Inputs) != 2 {
		t.Fatal("patch invoked dream or reembedded unchanged summary")
	}
	es, err := s.Latest(ctx, LatestQuery{N: 2})
	if err != nil || len(es) != 2 || es[0].Date != "2026-01-09" {
		t.Fatalf("latest %v %v", es, err)
	}
	until := "2026-01-10"
	es, err = s.Latest(ctx, LatestQuery{N: 2, Until: &until})
	if err != nil || len(es) != 1 {
		t.Fatal("until filter")
	}
	out, err := s.Search(ctx, "2026-01-01", "2026-01-31", 20, []string{"reading", "book"})
	if err != nil || len(out) != 2 {
		t.Fatalf("search %v %v", out, err)
	}
	if err = s.Normalize(ctx, false); err != nil {
		t.Fatal(err)
	}
	if len(emb.Inputs) != 2 {
		t.Fatal("normalize failed to reuse vectors")
	}
	out, err = s.Similar(ctx, SimilarRequest{UserMessage: "test"})
	if err != nil || len(out) != 2 {
		t.Fatalf("similar %v %v", out, err)
	}
}
func TestPatchBoundariesAndFailedIndex(t *testing.T) {
	s, emb := service(t)
	ctx := context.Background()
	hooks := 0
	s.OnAdded = func(string) { hooks++ }
	emb.Err = errors.New("provider unavailable")
	if err := s.Add(ctx, "2026-01-10", WriteRequest{Content: "body", Meta: Meta{Summary: "summary"}}); err != nil {
		t.Fatalf("embedding failure must not reject the write: %v", err)
	}
	if hooks != 1 {
		t.Fatal("dream not enqueued for an indexed entry without a vector")
	}
	if e, err := s.Get(ctx, "2026-01-10"); err != nil || e.Body != "body" {
		t.Fatalf("entry without vector not readable: %+v %v", e, err)
	}
	emb.Err = nil
	if out, err := s.Similar(ctx, SimilarRequest{UserMessage: "q"}); err != nil || len(out) != 0 {
		t.Fatalf("missing vector should be skipped: %v %v", out, err)
	}
	if err := s.Normalize(ctx, false); err != nil {
		t.Fatal(err)
	}
	if out, err := s.Similar(ctx, SimilarRequest{UserMessage: "q"}); err != nil || len(out) != 1 {
		t.Fatalf("normalize did not backfill vector: %v %v", out, err)
	}
	now := s.Now()
	s.Now = func() time.Time { return now.Add(24 * time.Hour) }
	if err := s.Patch(ctx, WriteRequest{Content: "boundary"}); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return now.Add(24*time.Hour + time.Nanosecond) }
	requireCode(t, s.Patch(ctx, WriteRequest{Content: "late"}), "PATCH_WINDOW_EXPIRED")
	e, err := s.Get(ctx, "2026-01-10")
	if err != nil {
		t.Fatal(err)
	}
	e.Created = ""
	if err := atomicWrite(e.FilePath, []byte(e.Markdown())); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return now }
	requireCode(t, s.Patch(ctx, WriteRequest{Content: "legacy"}), "PATCH_WINDOW_EXPIRED")
}
func TestBusyAndRebuildRollback(t *testing.T) {
	s, _ := service(t)
	ctx := context.Background()
	if err := s.Add(ctx, "2026-01-10", WriteRequest{Content: "body"}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	requireCode(t, s.Normalize(ctx, true), "DIARY_BUSY")
	s.mu.Unlock()
	if err := os.WriteFile(s.path("2026-01-10"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	requireCode(t, s.Normalize(ctx, true), "DIARY_PARSE_ERROR")
	var n int
	if err := s.DB.QueryRow("SELECT count(*) FROM diary").Scan(&n); err != nil || n != 1 {
		t.Fatal("rebuild changed index after parse failure")
	}
}
func TestQueryText(t *testing.T) {
	text := "你好世界"
	cases := []struct {
		r    SimilarRequest
		n    int
		want string
	}{{SimilarRequest{UserMessage: text}, 2, "世界"}, {SimilarRequest{UserMessage: "ignored", Recent: []Message{{Role: "assistant", Text: &text}}}, 50, "assistant: 你好世界"}}
	for _, tc := range cases {
		got, err := BuildQueryText(tc.r, tc.n)
		if err != nil || got != tc.want {
			t.Fatalf("%q %v", got, err)
		}
	}
	if _, err := BuildQueryText(SimilarRequest{}, 10); err == nil {
		t.Fatal("empty query accepted")
	}
	if _, err := BuildQueryText(SimilarRequest{Recent: []Message{{Role: "user"}}}, 10); err == nil {
		t.Fatal("missing text accepted")
	}
}
func TestFutureTimezone(t *testing.T) {
	s, _ := service(t)
	s.Location = time.FixedZone("test", 14*3600)
	s.Now = func() time.Time { return time.Date(2026, 1, 10, 23, 0, 0, 0, time.UTC) }
	if err := s.Add(context.Background(), "2026-01-12", WriteRequest{Content: "body"}); err != nil {
		t.Fatalf("local tomorrow should be accepted: %v", err)
	}
	requireCode(t, s.Add(context.Background(), "2026-01-13", WriteRequest{Content: "body"}), "DATE_TOO_FAR_AHEAD")
}

func TestSimilarDistanceOrder(t *testing.T) {
	s, _ := service(t)
	ctx := context.Background()
	for i, summary := range []string{"xx", "xxxxx", "xxxxxxxxxx"} {
		date := []string{"2026-01-01", "2026-01-02", "2026-01-03"}[i]
		if err := s.Add(ctx, date, WriteRequest{Content: "body", Meta: Meta{Summary: summary}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Similar(ctx, SimilarRequest{UserMessage: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || !strings.HasPrefix(got[0], "2026-01-03") || !strings.HasPrefix(got[2], "2026-01-01") {
		t.Fatalf("incorrect distance ordering %v", got)
	}
}

func TestSimilarWithoutSummaryVectors(t *testing.T) {
	s, _ := service(t)
	s.Config.Embedding.Dimensions = 6
	ctx := context.Background()
	if err := s.Add(ctx, "2026-01-01", WriteRequest{Content: "No summary"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Similar(ctx, SimilarRequest{UserMessage: "query"})
	if err != nil || len(got) != 0 {
		t.Fatalf("empty vector index: %v %v", got, err)
	}
}

func TestReadsUseIndexNotMarkdown(t *testing.T) {
	s, _ := service(t)
	ctx := context.Background()
	for _, date := range []string{"2026-01-08", "2026-01-09"} {
		if err := s.Add(ctx, date, WriteRequest{Content: "original " + date, Meta: Meta{Summary: "s"}}); err != nil {
			t.Fatal(err)
		}
	}
	// External edits are invisible to reads until normalize; a broken file does not break reads.
	if err := os.WriteFile(s.path("2026-01-08"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(s.path("2026-01-09"), []byte(Entry{Date: "2026-01-09", Created: "x", Updated: "x", Body: "edited"}.Markdown())); err != nil {
		t.Fatal(err)
	}
	es, err := s.Latest(ctx, LatestQuery{N: 5})
	if err != nil || len(es) != 2 || es[0].Body != "original 2026-01-08" || es[1].Body != "original 2026-01-09" {
		t.Fatalf("latest %+v %v", es, err)
	}
	if e, err := s.Get(ctx, "2026-01-08"); err != nil || e.Summary != "s" || len(e.Keywords) != 0 {
		t.Fatalf("get %+v %v", e, err)
	}
	requireCode(t, func() error { _, err := s.Get(ctx, "2026-01-07"); return err }(), "NOT_FOUND")
}
func TestOpenRebuildsMissingIndex(t *testing.T) {
	s, emb := service(t)
	ctx := context.Background()
	if err := s.Add(ctx, "2026-01-09", WriteRequest{Content: "body", Meta: Meta{Summary: "summary"}}); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(filepath.Join(s.Dir, "diary.db"+suffix))
	}
	c := testutil.Config(t)
	c.App.DataDir = s.Dir
	r, err := Open(c, emb)
	if err != nil {
		t.Fatal(err)
	}
	defer r.DB.Close()
	if e, err := r.Get(ctx, "2026-01-09"); err != nil || e.Body != "body" {
		t.Fatalf("startup rebuild %+v %v", e, err)
	}
	if out, err := r.Similar(ctx, SimilarRequest{UserMessage: "q"}); err != nil || len(out) != 1 {
		t.Fatalf("startup rebuild vectors %v %v", out, err)
	}
}

func TestActiveNormalize(t *testing.T) {
	for _, active := range []bool{true, false} {
		s, _ := service(t)
		s.Config.ActiveNormalize = active
		ctx := context.Background()
		if err := s.Add(ctx, "2026-01-08", WriteRequest{Content: "original"}); err != nil {
			t.Fatal(err)
		}
		if err := atomicWrite(s.path("2026-01-08"), []byte(Entry{Date: "2026-01-08", Created: "x", Updated: "x", Body: "edited"}.Markdown())); err != nil {
			t.Fatal(err)
		}
		if err := s.Add(ctx, "2026-01-09", WriteRequest{Content: "next"}); err != nil {
			t.Fatal(err)
		}
		e, err := s.Get(ctx, "2026-01-08")
		want := map[bool]string{true: "edited", false: "original"}[active]
		if err != nil || e.Body != want {
			t.Fatalf("active=%v: got %q %v", active, e.Body, err)
		}
		// A broken external file must not fail the write when active normalize runs.
		if err := os.WriteFile(s.path("2026-01-08"), []byte("invalid"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := s.Patch(ctx, WriteRequest{Content: "more"}); err != nil {
			t.Fatalf("active=%v patch: %v", active, err)
		}
		if e, err := s.Get(ctx, "2026-01-09"); err != nil || e.Body != "next\n\n---\n\nmore" {
			t.Fatalf("active=%v patched row %q %v", active, e.Body, err)
		}
	}
}
func TestOpenActiveNormalizeSyncsEdits(t *testing.T) {
	s, emb := service(t)
	ctx := context.Background()
	if err := s.Add(ctx, "2026-01-09", WriteRequest{Content: "original"}); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	if err := atomicWrite(s.path("2026-01-09"), []byte(Entry{Date: "2026-01-09", Created: "x", Updated: "x", Body: "edited"}.Markdown())); err != nil {
		t.Fatal(err)
	}
	for _, active := range []bool{false, true} {
		c := testutil.Config(t)
		c.App.DataDir = s.Dir
		c.Diary.ActiveNormalize = active
		r, err := Open(c, emb)
		if err != nil {
			t.Fatal(err)
		}
		e, err := r.Get(ctx, "2026-01-09")
		r.DB.Close()
		want := map[bool]string{true: "edited", false: "original"}[active]
		if err != nil || e.Body != want {
			t.Fatalf("active=%v: got %q %v", active, e.Body, err)
		}
	}
}
