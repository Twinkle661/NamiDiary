package sqlitex

import (
	"os"
	"testing"
)

func TestRelativePathsAndPragmas(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data with spaces", 0700); err != nil {
		t.Fatal(err)
	}
	db, err := Open("./data with spaces/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var journal string
	if err = db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("journal %s %v", journal, err)
	}
	var fk, busy int
	if err = db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign keys %d %v", fk, err)
	}
	if err = db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy != 5000 {
		t.Fatalf("busy timeout %d %v", busy, err)
	}
}
