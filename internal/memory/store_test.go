package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/memory"
	"github.com/Twinkle661/NamiDiary/internal/testutil"
)

func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *apperr.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}
func TestAtomicActionsAndCleanup(t *testing.T) {
	s, _ := testutil.Store(t)
	ctx := context.Background()
	add := memory.Action{Op: "add", Topic: "research", Kind: "area", Content: "A durable fact", Aliases: []string{"study"}, Sources: []string{"2026-01-02", "2026-01-01", "2026-01-02"}}
	result, err := s.Apply(ctx, memory.User, []memory.Action{add, {Op: "add", Topic: "profile", Content: "identity"}, {Op: "update", ID: 999, Content: "invalid"}}, memory.ApplyOptions{})
	code(t, err, "NOT_FOUND")
	if result.Changed {
		t.Fatal("failed batch reports changed")
	}
	l, _ := s.Library(ctx)
	if len(l) != 2 || len(l[0].Entries) != 0 {
		t.Fatal("partial batch committed")
	}
	r, err := s.Apply(ctx, memory.Agent, []memory.Action{add}, memory.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := r.AddedIDs[0]
	r, err = s.Apply(ctx, memory.User, []memory.Action{{Op: "update", ID: id, Content: "updated", Sources: []string{"2026-01-03"}}}, memory.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	l, _ = s.Library(ctx)
	topic := l[l.Resolve("STUDY")]
	if topic.Entries[0].UpdatedBy != memory.User || len(topic.Entries[0].Sources) != 3 {
		t.Fatalf("wrong provenance %+v", topic)
	}
	_, err = s.Apply(ctx, memory.Dream, []memory.Action{{Op: "drop", ID: id}, {Op: "add", Topic: "study", Content: "replacement"}}, memory.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	l, _ = s.Library(ctx)
	topic = l[l.Resolve("study")]
	if len(topic.Aliases) != 1 || topic.Entries[0].UpdatedBy != memory.Dream {
		t.Fatal("cleanup happened too early")
	}
	_, err = s.Apply(ctx, memory.User, []memory.Action{{Op: "drop", ID: topic.Entries[0].ID}}, memory.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	l, _ = s.Library(ctx)
	if len(l) != 2 {
		t.Fatal("empty topic retained")
	}
}
func TestValidationRestrictionsAndLimits(t *testing.T) {
	s, _ := testutil.Store(t)
	ctx := context.Background()
	s.Config.Limits.People.MaxTopics = 1
	s.Config.Limits.People.MaxEntries = 1
	apply := func(a memory.Action) error {
		_, err := s.Apply(ctx, memory.User, []memory.Action{a}, memory.ApplyOptions{})
		return err
	}
	if err := apply(memory.Action{Op: "add", Topic: "colleague", Kind: "people", Content: "fact", Aliases: []string{"COLLEAGUE", "peer", "PEER"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a    memory.Action
		want string
	}{{memory.Action{Op: "add", Topic: "second", Kind: "people", Content: "fact"}, "TOPIC_LIMIT_REACHED"}, {memory.Action{Op: "add", Topic: "colleague", Kind: "area", Content: "fact"}, "KIND_MISMATCH"}, {memory.Action{Op: "add", Topic: "other", Kind: "core", Content: "fact"}, "CORE_TOPIC_RESTRICTED"}, {memory.Action{Op: "add", Topic: "colleague", Content: strings.Repeat("界", 201)}, "ENTRY_TOO_LONG"}, {memory.Action{Op: "set_topic", Topic: "colleague", Aliases: []string{"profile"}}, "ALIAS_CONFLICT"}, {memory.Action{Op: "archive_project", Topic: "colleague"}, "CORE_TOPIC_RESTRICTED"}, {memory.Action{Op: "add", Topic: "colleague", Content: "fact", Sources: []string{"bad-date"}}, "INVALID_REQUEST"}, {memory.Action{Op: "drop", ID: 987}, "NOT_FOUND"}} {
		code(t, apply(tc.a), tc.want)
	}
	if err := apply(memory.Action{Op: "add", Topic: "PEER", Content: "second fact"}); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Library(ctx)
	if len(memory.Violations(l, s.Config.Limits)) != 1 {
		t.Fatal("soft entry limit not reported")
	}
	_, err := s.Apply(ctx, memory.Dream, []memory.Action{{Op: "drop", ID: 1}}, memory.ApplyOptions{AllowedOps: map[string]bool{"add": true}})
	code(t, err, "INVALID_REQUEST")
	_, err = s.Apply(ctx, memory.Dream, []memory.Action{{Op: "update", ID: 1, Content: "changed"}}, memory.ApplyOptions{AllowedKindsForWrite: map[string]bool{"area": true}})
	code(t, err, "INVALID_REQUEST")
	if err := apply(memory.Action{Op: "add", Topic: "goal", Kind: "project", Content: "goal fact"}); err != nil {
		t.Fatal(err)
	}
	if err := apply(memory.Action{Op: "archive_project", Topic: "goal"}); err != nil {
		t.Fatal(err)
	}
	code(t, apply(memory.Action{Op: "add", Topic: "goal", Content: "late"}), "TOPIC_ARCHIVED")
	l, _ = s.Library(ctx)
	b, _ := json.Marshal(memory.Directory{Library: l})
	if strings.Contains(string(b), "goal") || !strings.HasPrefix(string(b), `{"core":`) {
		t.Fatalf("directory %s", b)
	}
	s.Config.Limits.Project.MaxTopics = 0
	if len(memory.Violations(l, s.Config.Limits)) != 2 {
		t.Fatal("archived projects not counted")
	}
}
func TestPostCheckRollbackAndReadOrdering(t *testing.T) {
	s, _ := testutil.Store(t)
	ctx := context.Background()
	_, err := s.Apply(ctx, memory.User, []memory.Action{{Op: "add", Topic: "zeta", Kind: "area", Content: "fact"}, {Op: "add", Topic: "Alpha", Kind: "people", Content: "fact", Aliases: []string{"alias"}}}, memory.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Library(ctx)
	_, err = s.Apply(ctx, memory.Dream, []memory.Action{{Op: "drop", ID: 1}}, memory.ApplyOptions{PostCheck: func(*memory.Library) error { return errors.New("failed postcheck") }})
	if err == nil {
		t.Fatal("postcheck ignored")
	}
	after, _ := s.Library(ctx)
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("postcheck committed")
	}
	if after[2].Topic != "Alpha" || after[3].Topic != "zeta" {
		t.Fatal("kind order")
	}
	selected, err := after.Select([]string{"ALIAS", "Alpha"})
	if err != nil || len(selected) != 1 {
		t.Fatal("alias lookup or dedup")
	}
	_, err = after.Select([]string{"missing"})
	code(t, err, "NOT_FOUND")
}

func TestRepairPreservesValidSourceDates(t *testing.T) {
	s, _ := testutil.Store(t)
	ctx := context.Background()
	_, err := s.Apply(ctx, memory.User, []memory.Action{{Op: "add", Topic: "study", Kind: "area", Content: "fact"}}, memory.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE memory_topic SET aliases='broken' WHERE topic='study';UPDATE memory_entry SET sources='["2026-01-01", 42, "bad", "2026-01-01"]'`); err != nil {
		t.Fatal(err)
	}
	changed, err := s.Repair(ctx)
	if err != nil || !changed {
		t.Fatalf("repair %v %v", changed, err)
	}
	l, _ := s.Library(ctx)
	topic := l[l.Resolve("study")]
	if len(topic.Aliases) != 0 || len(topic.Entries[0].Sources) != 1 || topic.Entries[0].Sources[0] != "2026-01-01" {
		t.Fatalf("bad repair %+v", topic)
	}
	changed, err = s.Repair(ctx)
	if err != nil || changed {
		t.Fatalf("repair not idempotent %v %v", changed, err)
	}
}
