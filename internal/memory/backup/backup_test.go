package backup_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Twinkle661/NamiDiary/internal/memory"
	"github.com/Twinkle661/NamiDiary/internal/memory/backup"
	"github.com/Twinkle661/NamiDiary/internal/testutil"
)

func TestRoundTripCollisionAndValidation(t *testing.T) {
	s, c := testutil.Store(t)
	ctx := context.Background()
	_, err := s.Apply(ctx, memory.User, []memory.Action{{Op: "add", Topic: "研究", Kind: "area", Content: "A comma, a quote \" and\na new line 日本語", Aliases: []string{"study"}, Sources: []string{"2026-01-01"}}, {Op: "add", Topic: "goal", Kind: "project", Content: "finished"}, {Op: "archive_project", Topic: "goal"}}, memory.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b := backup.New(s, c.App.DataDir, c.Memory.Backup.Retention)
	before, _ := s.Library(ctx)
	name, err := b.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name2, err := b.Snapshot(ctx)
	if err != nil || name == name2 {
		t.Fatalf("collision %s %s %v", name, name2, err)
	}
	if err = s.Replace(ctx, memory.Empty()); err != nil {
		t.Fatal(err)
	}
	if err = b.Restore(ctx, name, true); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Library(ctx)
	x, _ := json.Marshal(before)
	y, _ := json.Marshal(after)
	if string(x) != string(y) {
		t.Fatalf("round trip mismatch %s / %s", x, y)
	}
	if err = b.Restore(ctx, "../escape", true); err == nil {
		t.Fatal("traversal accepted")
	}
	if err = os.WriteFile(filepath.Join(c.App.DataDir, "memory", name, "entries.csv"), []byte("id,topic,content,sources,updated,updated_by\n1,missing,invalid,[],bad,user\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = b.Restore(ctx, name, true); err == nil {
		t.Fatal("corrupt snapshot accepted")
	}
	after, _ = s.Library(ctx)
	y, _ = json.Marshal(after)
	if string(x) != string(y) {
		t.Fatal("invalid restore mutated database")
	}
	dirs, _ := os.ReadDir(filepath.Join(c.App.DataDir, "memory"))
	for _, d := range dirs {
		if d.Name()[0] == '.' {
			t.Fatal("temporary snapshot leaked")
		}
	}
}
func TestRetentionKeepsNewest(t *testing.T) {
	now := time.Now()
	ss := []backup.Snapshot{{Name: "newest", Time: now.Add(-48 * time.Hour)}, {Name: "older", Time: now.Add(-72 * time.Hour)}}
	got := (backup.MaxAge{Age: time.Hour}).Prune(ss, now)
	if len(got) != 1 || got[0].Name != "older" {
		t.Fatal(got)
	}
}
