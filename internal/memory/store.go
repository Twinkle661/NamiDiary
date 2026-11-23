package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/config"
	"github.com/Twinkle661/NamiDiary/internal/sqlitex"
)

type Store struct {
	DB     *sql.DB
	Config config.Memory
	mu     sync.Mutex
}

func Open(path string, c config.Memory) (*Store, error) {
	db, err := sqlitex.Open(path)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS memory_topic(topic TEXT PRIMARY KEY,kind TEXT NOT NULL CHECK(kind IN ('core','people','area','project')),aliases TEXT NOT NULL DEFAULT '[]',archived TEXT DEFAULT NULL,CHECK(archived IS NULL OR kind='project'));CREATE TABLE IF NOT EXISTS memory_entry(id INTEGER PRIMARY KEY AUTOINCREMENT,topic TEXT NOT NULL REFERENCES memory_topic(topic) ON UPDATE CASCADE,content TEXT NOT NULL,sources TEXT NOT NULL DEFAULT '[]',updated TEXT NOT NULL,updated_by TEXT NOT NULL CHECK(updated_by IN ('dream','agent','user')));CREATE INDEX IF NOT EXISTS idx_entry_topic ON memory_entry(topic);CREATE TABLE IF NOT EXISTS memory_meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);INSERT OR IGNORE INTO memory_meta VALUES('schema_version','1');INSERT OR IGNORE INTO memory_topic(topic,kind) VALUES('profile','core'),('preference','core'),('self','core');`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db, Config: c}, nil
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func read(ctx context.Context, q querier) (Library, error) {
	l := Library{}
	rows, err := q.QueryContext(ctx, "SELECT topic,kind,aliases,archived FROM memory_topic")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t Topic
		var a string
		t.Entries = []Entry{}
		if err = rows.Scan(&t.Topic, &t.Kind, &a, &t.Archived); err != nil {
			rows.Close()
			return nil, err
		}
		if json.Unmarshal([]byte(a), &t.Aliases) != nil || t.Aliases == nil {
			t.Aliases = []string{}
		}
		l = append(l, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, "SELECT id,topic,content,sources,updated,updated_by FROM memory_entry ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		var topic, src string
		if err = rows.Scan(&e.ID, &topic, &e.Content, &src, &e.Updated, &e.UpdatedBy); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(src), &e.Sources) != nil || e.Sources == nil {
			e.Sources = []string{}
		}
		idx := l.Resolve(topic)
		if idx < 0 {
			return nil, fmt.Errorf("entry references missing topic")
		}
		l[idx].Entries = append(l[idx].Entries, e)
	}
	l.Sort()
	return l, rows.Err()
}
func (s *Store) Library(ctx context.Context) (Library, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	l, err := read(ctx, tx)
	if err != nil {
		return nil, err
	}
	return l, tx.Commit()
}
func persist(ctx context.Context, tx *sql.Tx, l Library) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM memory_entry;DELETE FROM memory_topic;"); err != nil {
		return err
	}
	for _, t := range l {
		a, _ := json.Marshal(t.Aliases)
		if _, err := tx.ExecContext(ctx, "INSERT INTO memory_topic(topic,kind,aliases,archived) VALUES(?,?,?,?)", t.Topic, t.Kind, string(a), t.Archived); err != nil {
			return err
		}
		for _, e := range t.Entries {
			src, _ := json.Marshal(e.Sources)
			if _, err := tx.ExecContext(ctx, "INSERT INTO memory_entry(id,topic,content,sources,updated,updated_by) VALUES(?,?,?,?,?,?)", e.ID, t.Topic, e.Content, string(src), e.Updated, e.UpdatedBy); err != nil {
				return err
			}
		}
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO memory_meta(key,value) VALUES('last_change',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) Replace(ctx context.Context, l Library) error {
	if err := ValidateLibrary(l); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = persist(ctx, tx, l); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sqlite_sequence WHERE name='memory_entry';INSERT INTO sqlite_sequence(name,seq) SELECT 'memory_entry',coalesce(max(id),0) FROM memory_entry"); err != nil {
		return err
	}
	return tx.Commit()
}
func Empty() Library {
	l := Library{}
	for _, c := range CoreTopics {
		l = append(l, Topic{Topic: c, Kind: "core", Aliases: []string{}, Entries: []Entry{}})
	}
	return l
}
func (s *Store) Apply(ctx context.Context, src Source, actions []Action, opts ApplyOptions) (BatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := BatchResult{AddedIDs: []int64{}}
	if src != User && src != Agent && src != Dream {
		return result, apperr.Invalid("Invalid mutation source.")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	l, err := read(ctx, tx)
	if err != nil {
		return result, err
	}
	before, _ := json.Marshal(l)
	var next int64
	err = tx.QueryRowContext(ctx, "SELECT coalesce((SELECT seq FROM sqlite_sequence WHERE name='memory_entry'),0)").Scan(&next)
	if err != nil {
		return result, err
	}
	for _, t := range l {
		for _, e := range t.Entries {
			next = max(next, e.ID)
		}
	}
	for i, a := range actions {
		err := s.apply(&l, src, a, opts, &next, &result)
		if err != nil {
			var ae *apperr.Error
			if errors.As(err, &ae) {
				copy := *ae
				details := map[string]any{"action_index": i + 1, "op": a.Op, "reason": ae.Message}
				if prior, ok := ae.Details.(map[string]int); ok {
					for k, v := range prior {
						details[k] = v
					}
				}
				copy.Details = details
				copy.Message = fmt.Sprintf("Action %d (%s): %s", i+1, a.Op, ae.Message)
				return BatchResult{}, &copy
			}
			return BatchResult{}, err
		}
	}
	cleanup(&l)
	if opts.PostCheck != nil {
		if err := opts.PostCheck(&l); err != nil {
			return BatchResult{}, err
		}
	}
	cleanup(&l)
	l.Sort()
	after, _ := json.Marshal(l)
	result.Changed = string(before) != string(after)
	if result.Changed {
		if err = persist(ctx, tx, l); err != nil {
			return BatchResult{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return BatchResult{}, err
	}
	return result, nil
}
func cleanup(l *Library) {
	out := Library{}
	for _, t := range *l {
		if t.Kind == "core" || len(t.Entries) > 0 {
			out = append(out, t)
		}
	}
	*l = out
}
func (s *Store) apply(lib *Library, src Source, a Action, o ApplyOptions, next *int64, result *BatchResult) error {
	l := *lib
	if o.AllowedOps != nil && !o.AllowedOps[a.Op] {
		return apperr.Invalid("Operation is not allowed for this agent.")
	}
	valid := map[string]bool{"add": true, "update": true, "drop": true, "set_topic": true, "archive_project": true}
	if !valid[a.Op] {
		return apperr.Invalid("Unknown action operation.")
	}
	if a.Op != "add" && (a.Kind != "" || len(a.Aliases) > 0 && a.Op != "set_topic") {
		return apperr.Invalid("Fields are not valid for this operation.")
	}
	if a.Op == "add" || a.Op == "update" {
		a.Content = strings.TrimSpace(a.Content)
		if a.Content == "" {
			return apperr.Invalid("Content must not be empty.")
		}
		if n := utf8.RuneCountInString(a.Content); n > s.Config.EntryMaxChars {
			e := apperr.New(400, "ENTRY_TOO_LONG", "Entry exceeds the character limit.")
			e.Details = map[string]int{"actual": n, "max": s.Config.EntryMaxChars}
			return e
		}
	}
	ti, ei := -1, -1
	if a.Op == "update" || a.Op == "drop" {
		ti, ei = l.Find(a.ID)
		if ti < 0 {
			return apperr.Missing("Entry not found.")
		}
	} else {
		var err error
		a.Topic, err = name(a.Topic)
		if err != nil {
			return err
		}
		ti = l.Resolve(a.Topic)
	}
	if a.Op == "add" && ti < 0 {
		if o.ForbidCreateTopic {
			return apperr.Invalid("Creating topics is not allowed.")
		}
		if a.Kind == "core" || IsCoreTopic(a.Topic) {
			return apperr.New(400, "CORE_TOPIC_RESTRICTED", "Core topics are fixed.")
		}
		if a.Kind != "people" && a.Kind != "area" && a.Kind != "project" {
			return apperr.Invalid("A valid kind is required for new topics.")
		}
		count := 0
		for _, t := range l {
			if t.Kind == a.Kind {
				count++
			}
		}
		if count >= s.Config.Limits.For(a.Kind, a.Topic).MaxTopics {
			return apperr.New(409, "TOPIC_LIMIT_REACHED", "Topic limit reached.")
		}
		l = append(l, Topic{Topic: a.Topic, Kind: a.Kind, Aliases: []string{}, Entries: []Entry{}})
		ti = len(l) - 1
		as, err := aliases(l, ti, a.Aliases)
		if err != nil {
			return err
		}
		l[ti].Aliases = as
	} else if a.Op == "add" {
		if a.Kind != "" && a.Kind != l[ti].Kind {
			return apperr.New(409, "KIND_MISMATCH", "Kind does not match existing topic.")
		}
		if len(a.Aliases) > 0 {
			return apperr.Invalid("Use set_topic to change aliases.")
		}
	}
	if ti < 0 {
		return apperr.Missing("Topic not found.")
	}
	if o.AllowedKindsForWrite != nil && !o.AllowedKindsForWrite[l[ti].Kind] {
		return apperr.Invalid("Writing this kind is not allowed for this agent.")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch a.Op {
	case "add":
		if l[ti].Archived != nil {
			return apperr.New(409, "TOPIC_ARCHIVED", "Cannot add to an archived project.")
		}
		sources, err := NormalizeSources(a.Sources)
		if err != nil {
			return err
		}
		*next++
		l[ti].Entries = append(l[ti].Entries, Entry{*next, a.Content, sources, now, src})
		result.AddedIDs = append(result.AddedIDs, *next)
	case "update":
		e := &l[ti].Entries[ei]
		sources, err := NormalizeSources(append(append([]string{}, e.Sources...), a.Sources...))
		if err != nil {
			return err
		}
		e.Content = a.Content
		e.Sources = sources
		e.Updated = now
		e.UpdatedBy = src
	case "drop":
		l[ti].Entries = append(l[ti].Entries[:ei], l[ti].Entries[ei+1:]...)
	case "set_topic":
		as, err := aliases(l, ti, a.Aliases)
		if err != nil {
			return err
		}
		l[ti].Aliases = as
	case "archive_project":
		if l[ti].Kind != "project" {
			return apperr.New(400, "CORE_TOPIC_RESTRICTED", "Only project topics can be archived.")
		}
		if l[ti].Archived == nil {
			l[ti].Archived = &now
		}
	}
	*lib = l
	return nil
}
func (s *Store) Meta(ctx context.Context, key string) string {
	var value string
	_ = s.DB.QueryRowContext(ctx, "SELECT value FROM memory_meta WHERE key=?", key).Scan(&value)
	return value
}
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO memory_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}
func (s *Store) Repair(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	l, err := read(ctx, tx)
	if err != nil {
		return false, err
	}
	changed := false
	// Inspect raw JSON too: the read model deliberately remains available during repair.
	rows, err := tx.QueryContext(ctx, "SELECT aliases FROM memory_topic UNION ALL SELECT sources FROM memory_entry")
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return false, err
		}
		var xs []string
		if json.Unmarshal([]byte(raw), &xs) != nil || xs == nil {
			changed = true
			slog.Warn("Repairing malformed memory JSON")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	rows, err = tx.QueryContext(ctx, "SELECT id,sources FROM memory_entry")
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var id int64
		var raw string
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return false, err
		}
		var items []json.RawMessage
		filtered := []string{}
		if json.Unmarshal([]byte(raw), &items) == nil {
			for _, item := range items {
				var date string
				if json.Unmarshal(item, &date) == nil {
					if _, err := NormalizeSources([]string{date}); err == nil {
						filtered = append(filtered, date)
					}
				}
			}
		}
		filtered, _ = NormalizeSources(filtered)
		normalized, _ := json.Marshal(filtered)
		if string(normalized) != raw {
			changed = true
			ti, ei := l.Find(id)
			if ti >= 0 {
				l[ti].Entries[ei].Sources = filtered
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, core := range Empty() {
		i := l.Resolve(core.Topic)
		if i < 0 {
			l = append(l, core)
			changed = true
		} else if l[i].Kind != "core" {
			l[i].Kind = "core"
			changed = true
		}
	}
	for i := range l {
		t := &l[i]
		if t.Kind != "project" && t.Archived != nil {
			t.Archived = nil
			changed = true
		}
		for j := range t.Entries {
			e := &t.Entries[j]
			filtered := []string{}
			for _, v := range e.Sources {
				if _, err := NormalizeSources([]string{v}); err == nil {
					filtered = append(filtered, v)
				}
			}
			filtered, _ = NormalizeSources(filtered)
			a, _ := json.Marshal(filtered)
			b, _ := json.Marshal(e.Sources)
			if string(a) != string(b) {
				changed = true
				e.Sources = filtered
			}
		}
	}
	before := len(l)
	cleanup(&l)
	changed = changed || len(l) != before
	if changed {
		if err = persist(ctx, tx, l); err != nil {
			return false, err
		}
	}
	return changed, tx.Commit()
}
