package diary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

func Open(c config.Config, emb Embedder) (*Service, error) {
	if err := os.MkdirAll(filepath.Join(c.App.DataDir, "diary"), 0700); err != nil {
		return nil, err
	}
	db, err := sqlitex.Open(filepath.Join(c.App.DataDir, "diary.db"))
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(c.App.Timezone)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &Service{DB: db, Config: c.Diary, Dir: c.App.DataDir, Location: loc, Embedder: emb, Now: time.Now}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS diary (id INTEGER PRIMARY KEY AUTOINCREMENT,date TEXT NOT NULL,created TEXT,updated TEXT,keywords TEXT,summary TEXT,location TEXT,content TEXT NOT NULL,file_path TEXT NOT NULL UNIQUE)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	_, err = db.Exec(fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS diary_fts USING fts5(content,keywords,tokenize='trigram');CREATE VIRTUAL TABLE IF NOT EXISTS diary_vec USING vec0(id INTEGER PRIMARY KEY,embedding float[%d]);`, c.Diary.Embedding.Dimensions))
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Service) path(date string) string {
	return filepath.Join(s.Dir, "diary", date[:4], date[5:7], date[5:]+".md")
}
func (s *Service) Get(ctx context.Context, date string) (Entry, error) {
	if !ValidDate(date) {
		return Entry{}, apperr.Invalid("Date must be yyyy-MM-dd.")
	}
	e, err := Read(s.path(date))
	if os.IsNotExist(err) {
		return e, apperr.Missing("Diary entry not found.")
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
func (s *Service) Latest(ctx context.Context, q LatestQuery) ([]Entry, error) {
	if q.N < 1 {
		return nil, apperr.Invalid("Recent must be positive.")
	}
	if q.Until != nil && !ValidDate(*q.Until) {
		return nil, apperr.Invalid("Invalid until date.")
	}
	paths, err := s.files()
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e, err := Read(p)
		if err != nil {
			return nil, err
		}
		if q.Until == nil || e.Date <= *q.Until {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	if len(out) > q.N {
		out = out[len(out)-q.N:]
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
	if err := s.rebuild(ctx, false); err != nil {
		return err
	}
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
	e := es[0]
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
	if summary := strings.TrimSpace(r.Meta.Summary); summary != "" {
		e.Summary = summary
	}
	e.Updated = s.Now().UTC().Format(Timestamp)
	if err = atomicWrite(e.FilePath, []byte(e.Markdown())); err != nil {
		return err
	}
	return s.rebuild(ctx, false)
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
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS diary_fts;DROP TABLE IF EXISTS diary_vec;DROP TABLE IF EXISTS diary;CREATE TABLE diary(id INTEGER PRIMARY KEY AUTOINCREMENT,date TEXT NOT NULL,created TEXT,updated TEXT,keywords TEXT,summary TEXT,location TEXT,content TEXT NOT NULL,file_path TEXT NOT NULL UNIQUE);CREATE VIRTUAL TABLE diary_fts USING fts5(content,keywords,tokenize='trigram');CREATE VIRTUAL TABLE diary_vec USING vec0(id INTEGER PRIMARY KEY,embedding float[%d]);`, dim))
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
