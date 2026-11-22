package diary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/config"
	"github.com/Twinkle661/NamiDiary/internal/sqlitex"
)

type Embedder interface {
	Embed(context.Context, []string) ([][]float32, error)
}
type Service struct {
	DB       *sql.DB
	Config   config.Diary
	Dir      string
	Location *time.Location
	Embedder Embedder
	OnAdded  func(string)
	Now      func() time.Time
	mu       sync.Mutex
}
type LatestQuery struct {
	N     int
	Until *string
}
type Meta struct {
	Keywords []string `json:"keywords"`
	Summary  string   `json:"summary"`
	Location string   `json:"location"`
}
type WriteRequest struct {
	Content string `json:"content"`
	Meta    Meta   `json:"meta"`
}
type Message struct {
	Role string  `json:"role"`
	Text *string `json:"text"`
}
type SimilarRequest struct {
	UserMessage string    `json:"user_message"`
	Recent      []Message `json:"recent"`
}

func schema(dim int) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS diary(id INTEGER PRIMARY KEY AUTOINCREMENT,date TEXT NOT NULL,created TEXT,updated TEXT,keywords TEXT,summary TEXT,location TEXT,content TEXT NOT NULL,file_path TEXT NOT NULL UNIQUE);CREATE INDEX IF NOT EXISTS diary_date ON diary(date);CREATE VIRTUAL TABLE IF NOT EXISTS diary_fts USING fts5(content,keywords,tokenize='trigram');CREATE VIRTUAL TABLE IF NOT EXISTS diary_vec USING vec0(id INTEGER PRIMARY KEY,embedding float[%d]);`, dim)
}

// Open prepares the derived index. A new database, a stale embedding status or
// active normalize triggers one Markdown scan so reads can be served from SQL afterwards.
func Open(c config.Config, emb Embedder) (*Service, error) {
	if err := os.MkdirAll(filepath.Join(c.App.DataDir, "diary"), 0700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(c.App.DataDir, "diary.db")
	_, statErr := os.Stat(dbPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	fresh := os.IsNotExist(statErr)
	db, err := sqlitex.Open(dbPath)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(c.App.Timezone)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &Service{DB: db, Config: c.Diary, Dir: c.App.DataDir, Location: loc, Embedder: emb, Now: time.Now}
	if _, err = db.Exec(schema(c.Diary.Embedding.Dimensions)); err != nil {
		db.Close()
		return nil, err
	}
	if fresh || s.Config.ActiveNormalize || !s.validStatus() {
		if err = s.rebuild(context.Background(), false); err != nil {
			db.Close()
			return nil, fmt.Errorf("diary index rebuild: %w", err)
		}
	}
	return s, nil
}
func (s *Service) path(date string) string {
	return filepath.Join(s.Dir, "diary", date[:4], date[5:7], date[5:]+".md")
}

const entryColumns = "id,date,created,updated,keywords,summary,location,content,file_path"

type scanner interface{ Scan(...any) error }

func scanEntry(row scanner) (int64, Entry, error) {
	var id int64
	var e Entry
	var created, updated, keywords, summary, location sql.NullString
	if err := row.Scan(&id, &e.Date, &created, &updated, &keywords, &summary, &location, &e.Body, &e.FilePath); err != nil {
		return 0, e, err
	}
	e.Created, e.Updated, e.Summary, e.Location = created.String, updated.String, summary.String, location.String
	e.Keywords = []string{}
	if keywords.String != "" {
		if err := json.Unmarshal([]byte(keywords.String), &e.Keywords); err != nil {
			return 0, e, err
		}
	}
	return id, e, nil
}

// Get reads one entry from the index; Markdown is only touched by writes and rebuilds.
func (s *Service) Get(ctx context.Context, date string) (Entry, error) {
	if !ValidDate(date) {
		return Entry{}, apperr.Invalid("Date must be yyyy-MM-dd.")
	}
	_, e, err := scanEntry(s.DB.QueryRowContext(ctx, "SELECT "+entryColumns+" FROM diary WHERE date=? ORDER BY id DESC LIMIT 1", date))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, apperr.Missing("Diary entry not found.")
	}
	return e, err
}
func (s *Service) files() ([]string, error) {
	var paths []string
	err := filepath.WalkDir(filepath.Join(s.Dir, "diary"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

// Latest returns the newest n indexed entries, oldest first.
func (s *Service) Latest(ctx context.Context, q LatestQuery) ([]Entry, error) {
	if q.N < 1 {
		return nil, apperr.Invalid("Recent must be positive.")
	}
	if q.Until != nil && !ValidDate(*q.Until) {
		return nil, apperr.Invalid("Invalid until date.")
	}
	query := "SELECT " + entryColumns + " FROM diary"
	args := []any{}
	if q.Until != nil {
		query += " WHERE date<=?"
		args = append(args, *q.Until)
	}
	query += " ORDER BY date DESC LIMIT ?"
	args = append(args, q.N)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		_, e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
func Markdown(entries []Entry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.Markdown()
	}
	return strings.Join(parts, "\n")
}
func atomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func (s *Service) Add(ctx context.Context, date string, r WriteRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ValidDate(date) {
		return apperr.Invalid("Date must be yyyy-MM-dd.")
	}
	if strings.TrimSpace(r.Content) == "" {
		return apperr.Invalid("Content is required.")
	}
	start, _ := time.ParseInLocation("2006-01-02", date, s.Location)
	now := s.Now()
	if start.After(now.Add(s.Config.FutureWindow)) {
		return apperr.New(400, "DATE_TOO_FAR_AHEAD", "Diary date is too far ahead.")
	}
	p := s.path(date)
	if _, err := os.Stat(p); err == nil {
		return apperr.New(409, "CONFLICT", "Diary entry for "+date+" already exists.")
	} else if !os.IsNotExist(err) {
		return err
	}
	loc := strings.TrimSpace(r.Meta.Location)
	if utf8.RuneCountInString(loc) > 200 {
		return apperr.Invalid("Location exceeds 200 characters.")
	}
	stamp := now.UTC().Format(Timestamp)
	e := Entry{date, stamp, stamp, r.Meta.Keywords, strings.TrimSpace(r.Meta.Summary), loc, strings.TrimSpace(r.Content), p}
	if err := atomicWrite(p, []byte(e.Markdown())); err != nil {
		return err
	}
	if err := s.index(ctx, e, true); err != nil {
		return err
	}
	s.activeNormalize(ctx)
	if s.OnAdded != nil {
		s.OnAdded(date)
	}
	return nil
}
func (s *Service) Patch(ctx context.Context, r WriteRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Meta.Location != "" {
		return apperr.Invalid("Location is not patchable.")
	}
	es, err := s.Latest(ctx, LatestQuery{N: 1})
	if err != nil {
		return err
	}
	if len(es) == 0 {
		return apperr.Missing("No diary entries exist.")
	}
	// The index only locates the target; the patch itself starts from its Markdown source.
	e, err := Read(es[0].FilePath)
	if err != nil {
		return err
	}
	created, err := time.Parse(time.RFC3339Nano, e.Created)
	if err != nil || s.Now().Sub(created) > s.Config.PatchWindow {
		return apperr.New(409, "PATCH_WINDOW_EXPIRED", fmt.Sprintf("Diary %s was created at %s; patch window expired.", e.Date, e.Created))
	}
	if body := strings.TrimSpace(r.Content); body != "" {
		e.Body += "\n\n---\n\n" + body
	}
	seen := map[string]bool{}
	keywords := []string{}
	for _, k := range append(e.Keywords, r.Meta.Keywords...) {
		if !seen[k] {
			keywords = append(keywords, k)
			seen[k] = true
		}
	}
	e.Keywords = keywords
	oldSummary := e.Summary
	if summary := strings.TrimSpace(r.Meta.Summary); summary != "" {
		e.Summary = summary
	}
	e.Updated = s.Now().UTC().Format(Timestamp)
	if err = atomicWrite(e.FilePath, []byte(e.Markdown())); err != nil {
		return err
	}
	if err = s.index(ctx, e, e.Summary != oldSummary); err != nil {
		return err
	}
	s.activeNormalize(ctx)
	return nil
}

// activeNormalize runs after the entry's own row is indexed, so a failure here
// only means other external edits stay unsynced; the write itself has succeeded.
func (s *Service) activeNormalize(ctx context.Context) {
	if !s.Config.ActiveNormalize {
		return
	}
	if err := s.rebuild(ctx, false); err != nil {
		slog.Warn("Active diary normalize failed; the written entry is indexed", "error", err)
	}
}

// index upserts one entry into the derived tables. When reembed is set, the old
// vector is dropped and the summary embedded again; an embedding failure leaves the
// vector empty (similar skips the entry) until normalize backfills it.
func (s *Service) index(ctx context.Context, e Entry, reembed bool) error {
	var vector []byte
	if reembed && e.Summary != "" {
		vector = s.embed(ctx, e)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ks, _ := json.Marshal(e.Keywords)
	var id int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM diary WHERE file_path=?", e.FilePath).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, err := tx.ExecContext(ctx, "INSERT INTO diary(date,created,updated,keywords,summary,location,content,file_path) VALUES(?,?,?,?,?,?,?,?)", e.Date, e.Created, e.Updated, string(ks), e.Summary, e.Location, e.Body, e.FilePath)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if _, err = tx.ExecContext(ctx, "UPDATE diary SET date=?,created=?,updated=?,keywords=?,summary=?,location=?,content=? WHERE id=?", e.Date, e.Created, e.Updated, string(ks), e.Summary, e.Location, e.Body, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM diary_fts WHERE rowid=?", id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO diary_fts(rowid,content,keywords) VALUES(?,?,?)", id, e.Body, string(ks)); err != nil {
		return err
	}
	if reembed {
		if _, err = tx.ExecContext(ctx, "DELETE FROM diary_vec WHERE id=?", id); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if vector != nil {
		// Kept outside the row transaction so a vector problem cannot hide the entry from reads.
		if _, err = s.DB.ExecContext(ctx, "INSERT INTO diary_vec(id,embedding) VALUES(?,?)", id, vector); err != nil {
			slog.Warn("Diary vector not stored; run diary normalize to backfill", "date", e.Date, "error", err)
		}
	}
	return nil
}
func (s *Service) embed(ctx context.Context, e Entry) []byte {
	vs, err := s.Embedder.Embed(ctx, []string{e.Summary})
	if err == nil && len(vs) != 1 {
		err = fmt.Errorf("embedding returned %d vectors", len(vs))
	}
	var b []byte
	if err == nil {
		b, err = sqlitevec.SerializeFloat32(vs[0])
	}
	if err != nil {
		slog.Warn("Diary summary not embedded; run diary normalize to backfill", "date", e.Date, "error", err)
		return nil
	}
	return b
}
func (s *Service) Normalize(ctx context.Context, full bool) error {
	if !s.mu.TryLock() {
		return apperr.Busy("DIARY")
	}
	defer s.mu.Unlock()
	return s.rebuild(ctx, full)
}

type status struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
}

func (s *Service) validStatus() bool {
	b, e := os.ReadFile(filepath.Join(s.Dir, "dbStatus.json"))
	var st status
	if e != nil || json.Unmarshal(b, &st) != nil {
		return false
	}
	var n int
	if err := s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('diary_fts','diary_vec')").Scan(&n); err != nil || n != 2 {
		return false
	}
	return st.BaseURL == s.Config.Embedding.BaseURL && st.Model == s.Config.Embedding.Model
}
func (s *Service) rebuild(ctx context.Context, full bool) error {
	full = full || !s.validStatus()
	paths, err := s.files()
	if err != nil {
		return err
	}
	entries := make([]Entry, 0, len(paths))
	for _, p := range paths {
		e, err := Read(p)
		if err != nil {
			return err
		}
		entries = append(entries, e)
	}
	type cached struct {
		summary string
		vector  []byte
	}
	cache := map[string]cached{}
	if !full {
		rows, err := s.DB.QueryContext(ctx, "SELECT d.file_path,d.summary,v.embedding FROM diary d JOIN diary_vec v ON v.id=d.id")
		if err != nil {
			return err
		}
		for rows.Next() {
			var p string
			var c cached
			if err = rows.Scan(&p, &c.summary, &c.vector); err != nil {
				rows.Close()
				return err
			}
			cache[p] = c
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	vectors := map[string][]byte{}
	var inputs []string
	var pending []string
	dim := 0
	for _, e := range entries {
		if e.Summary == "" {
			continue
		}
		if c, ok := cache[e.FilePath]; ok && c.summary == e.Summary {
			vectors[e.FilePath] = c.vector
			dim = len(c.vector) / 4
		} else {
			inputs = append(inputs, e.Summary)
			pending = append(pending, e.FilePath)
		}
	}
	if len(inputs) > 0 {
		vs, err := s.Embedder.Embed(ctx, inputs)
		if err != nil {
			return err
		}
		for i, v := range vs {
			if dim != 0 && len(v) != dim {
				return apperr.New(502, "EMBEDDING_RESPONSE_ERROR", "Embedding dimensions changed; run diary init.")
			}
			dim = len(v)
			b, err := sqlitevec.SerializeFloat32(v)
			if err != nil {
				return err
			}
			vectors[pending[i]] = b
		}
	}
	if dim == 0 {
		dim = s.Config.Embedding.Dimensions
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The derived index is swapped transactionally, retaining unchanged vectors on normalize.
	_, err = tx.ExecContext(ctx, "DROP TABLE IF EXISTS diary_fts;DROP TABLE IF EXISTS diary_vec;DROP TABLE IF EXISTS diary;"+schema(dim))
	if err != nil {
		return err
	}
	for _, e := range entries {
		ks, _ := json.Marshal(e.Keywords)
		res, err := tx.ExecContext(ctx, "INSERT INTO diary(date,created,updated,keywords,summary,location,content,file_path) VALUES(?,?,?,?,?,?,?,?)", e.Date, e.Created, e.Updated, string(ks), e.Summary, e.Location, e.Body, e.FilePath)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO diary_fts(rowid,content,keywords) VALUES(?,?,?)", id, e.Body, string(ks)); err != nil {
			return err
		}
		if v, ok := vectors[e.FilePath]; ok {
			if _, err = tx.ExecContext(ctx, "INSERT INTO diary_vec(id,embedding) VALUES(?,?)", id, v); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	b, _ := json.Marshal(status{s.Config.Embedding.BaseURL, s.Config.Embedding.Model})
	return atomicWrite(filepath.Join(s.Dir, "dbStatus.json"), b)
}
func (s *Service) Search(ctx context.Context, from, to string, limit int, terms []string) ([]string, error) {
	if !ValidDate(from) || !ValidDate(to) || from > to {
		return nil, apperr.Invalid("Valid from and to dates are required.")
	}
	if limit < 1 {
		return nil, apperr.Invalid("Limit must be positive.")
	}
	query := "SELECT d.date,d.keywords,d.summary FROM diary d JOIN diary_fts f ON d.id=f.rowid WHERE d.date>=? AND d.date<=?"
	args := []any{from, to}
	for _, term := range terms {
		query += " AND (f.content LIKE ? OR f.keywords LIKE ?)"
		args = append(args, "%"+term+"%", "%"+term+"%")
	}
	query += " ORDER BY d.date DESC LIMIT ?"
	args = append(args, limit)
	return s.compactRows(ctx, query, args...)
}
func (s *Service) compactRows(ctx context.Context, q string, args ...any) ([]string, error) {
	out := []string{}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		var ks string
		if err := rows.Scan(&e.Date, &ks, &e.Summary); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(ks), &e.Keywords); err != nil {
			return nil, err
		}
		out = append(out, e.Compact())
	}
	return out, rows.Err()
}
func BuildQueryText(r SimilarRequest, maxChars int) (string, error) {
	text := r.UserMessage
	if len(r.Recent) > 0 {
		lines := []string{}
		for _, m := range r.Recent {
			if strings.TrimSpace(m.Role) == "" || m.Text == nil {
				return "", apperr.Invalid("Recent messages require role and string text.")
			}
			lines = append(lines, m.Role+": "+*m.Text)
		}
		text = strings.Join(lines, "\n")
	}
	if strings.TrimSpace(text) == "" {
		return "", apperr.Invalid("A user_message or recent messages are required.")
	}
	rs := []rune(text)
	if len(rs) > maxChars {
		rs = rs[len(rs)-maxChars:]
	}
	return string(rs), nil
}
func (s *Service) Similar(ctx context.Context, r SimilarRequest) ([]string, error) {
	text, err := BuildQueryText(r, s.Config.SimilarQueryMaxChars)
	if err != nil {
		return nil, err
	}
	vs, err := s.Embedder.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	v, err := sqlitevec.SerializeFloat32(vs[0])
	if err != nil {
		return nil, err
	}
	var exists int
	err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='diary_vec'").Scan(&exists)
	if err != nil {
		return nil, err
	}
	if exists == 0 {
		return []string{}, nil
	}
	var count int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM diary_vec").Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		return []string{}, nil
	}
	out, err := s.compactRows(ctx, "SELECT d.date,d.keywords,d.summary FROM diary_vec v JOIN diary d ON d.id=v.id WHERE v.embedding MATCH ? AND k = ? ORDER BY v.distance ASC", v, s.Config.SimilarLimit)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{}, nil
	}
	if err == nil {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, err
}
