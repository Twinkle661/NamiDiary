package agents_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Twinkle661/NamiDiary/internal/diary"
	"github.com/Twinkle661/NamiDiary/internal/llm"
	"github.com/Twinkle661/NamiDiary/internal/memory"
	"github.com/Twinkle661/NamiDiary/internal/memory/agents"
	"github.com/Twinkle661/NamiDiary/internal/testutil"
)

type fakeLLM struct {
	Replies  []string
	Errors   []error
	Messages [][]llm.Message
}

func (f *fakeLLM) Complete(ctx context.Context, agent string, m []llm.Message) (string, error) {
	i := len(f.Messages)
	f.Messages = append(f.Messages, append([]llm.Message{}, m...))
	if i < len(f.Errors) && f.Errors[i] != nil {
		return "", f.Errors[i]
	}
	return f.Replies[min(i, len(f.Replies)-1)], nil
}

type diaryReader struct{}

func (diaryReader) Latest(context.Context, diary.LatestQuery) ([]diary.Entry, error) {
	return []diary.Entry{{Date: "2026-01-01", Body: "A durable fact"}}, nil
}
func TestParseStrict(t *testing.T) {
	for _, raw := range []string{`{}`, `{"actions":null}`, `{"actions":[],"extra":1}`, `{"actions":[{"op":"add","new_name":"unknown"}]}`, `{"actions":[]} {}`} {
		if _, err := agents.Parse(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`{"actions":[]}`, "```json\n{\"actions\":[]}\n```"} {
		if _, err := agents.Parse(raw); err != nil {
			t.Fatal(err)
		}
	}
}
func TestRetryAndAtomicity(t *testing.T) {
	s, c := testutil.Store(t)
	client := &fakeLLM{Replies: []string{`{"actions":[{"op":"add","topic":"profile","content":"must roll back"},{"op":"drop","id":999}]}`, `{"actions":[{"op":"add","topic":"profile","content":"corrected"}]}`}}
	r := agents.Runner{Store: s, Diary: diaryReader{}, Client: client, Config: c.Memory, Location: time.UTC}
	if err := r.Run(context.Background(), "dream", nil, ""); err != nil {
		t.Fatal(err)
	}
	if len(client.Messages) != 2 || len(client.Messages[1]) != 4 || !strings.Contains(client.Messages[1][3].Content, "Action 2") {
		t.Fatalf("missing feedback %+v", client.Messages)
	}
	l, _ := s.Library(context.Background())
	es := l[l.Resolve("profile")].Entries
	if len(es) != 1 || es[0].Content != "corrected" {
		t.Fatal("partial batch leaked")
	}
	client = &fakeLLM{Replies: []string{`{"actions":[]}`}, Errors: []error{errors.New("transport")}}
	r.Client = client
	r.Backoff = time.Millisecond
	if err := r.Run(context.Background(), "dream", nil, ""); err != nil {
		t.Fatal(err)
	}
	if len(client.Messages) != 2 || len(client.Messages[1]) != 2 {
		t.Fatal("transport retry added validation feedback")
	}
}
func TestArchiveAndCompactPostchecks(t *testing.T) {
	s, c := testutil.Store(t)
	ctx := context.Background()
	_, err := s.Apply(ctx, memory.User, []memory.Action{{Op: "add", Topic: "goal", Kind: "project", Content: "Finished"}, {Op: "archive_project", Topic: "goal"}}, memory.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeLLM{Replies: []string{`{"actions":[{"op":"add","topic":"profile","content":"forbidden"}]}`, `{"actions":[{"op":"add","topic":"study","kind":"area","content":"Outcome"}]}`}}
	r := agents.Runner{Store: s, Diary: diaryReader{}, Client: client, Config: c.Memory, Location: time.UTC}
	if err = r.Run(ctx, "archive", nil, "goal"); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Library(ctx)
	if l.Resolve("goal") >= 0 || l.Resolve("study") < 0 {
		t.Fatal("archive postcheck")
	}
	s.Config.Limits.Profile.MaxEntries = 1
	r.Config.Limits.Profile.MaxEntries = 1
	_, err = s.Apply(ctx, memory.User, []memory.Action{{Op: "add", Topic: "profile", Content: "one"}, {Op: "add", Topic: "profile", Content: "two"}}, memory.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Library(ctx)
	client = &fakeLLM{Replies: []string{`{"actions":[]}`}}
	r.Client = client
	if err = r.Run(ctx, "compact", nil, ""); err == nil {
		t.Fatal("over-limit compact accepted")
	}
	if len(client.Messages) != c.Memory.LLM.MaxAttempts {
		t.Fatal("wrong retry count")
	}
	after, _ := s.Library(ctx)
	if len(after[after.Resolve("profile")].Entries) != len(before[before.Resolve("profile")].Entries) {
		t.Fatal("failed compact mutated state")
	}
}
