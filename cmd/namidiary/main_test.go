package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Twinkle661/NamiDiary/internal/memory"
	"github.com/Twinkle661/NamiDiary/internal/testutil"
)

func TestStartupRestoreOnlyWhenMissing(t *testing.T) {
	c := testutil.Config(t)
	ctx := context.Background()
	s, b, err := openMemory(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Apply(ctx, memory.User, []memory.Action{{Op: "add", Topic: "profile", Content: "recoverable fact"}}, memory.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	want, _ := s.Library(ctx)
	if err = s.Replace(ctx, memory.Empty()); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, _, err = openMemory(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	lib, _ := s.Library(ctx)
	if len(lib[lib.Resolve("profile")].Entries) != 0 {
		t.Fatal("existing database overwritten")
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(c.App.DataDir, "memory.db")); err != nil {
		t.Fatal(err)
	}
	s, _, err = openMemory(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	got, _ := s.Library(ctx)
	a, _ := json.Marshal(want)
	z, _ := json.Marshal(got)
	if string(a) != string(z) {
		t.Fatalf("automatic restore mismatch %s %s", a, z)
	}
}
