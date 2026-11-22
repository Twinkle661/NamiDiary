package backup

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/config"
	"github.com/Twinkle661/NamiDiary/internal/memory"
)

type Snapshot struct {
	Name     string
	Time     time.Time
	Sequence int
}
type RetentionPolicy interface {
	Prune([]Snapshot, time.Time) []Snapshot
}
type MaxAge struct{ Age time.Duration }

func (p MaxAge) Prune(ss []Snapshot, now time.Time) []Snapshot {
	out := []Snapshot{}
	for i, s := range ss {
		if i > 0 && now.Sub(s.Time) > p.Age {
			out = append(out, s)
		}
	}
	return out
}

var Policies = map[string]func(config.Retention) RetentionPolicy{"max_age": func(c config.Retention) RetentionPolicy { return MaxAge{c.MaxAge} }}
var validName = regexp.MustCompile(`^([0-9]{8}T[0-9]{6}Z)(?:-([2-9]|[1-9][0-9]+))?$`)

type Manager struct {
	Store  *memory.Store
	Dir    string
	Policy RetentionPolicy
	mu     sync.Mutex
}

func New(s *memory.Store, dir string, c config.Retention) *Manager {
	return &Manager{Store: s, Dir: filepath.Join(dir, "memory"), Policy: Policies[c.Policy](c)}
}
func (m *Manager) List() ([]Snapshot, error) {
	dirs, err := os.ReadDir(m.Dir)
	if os.IsNotExist(err) {
		return []Snapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Snapshot{}
	for _, d := range dirs {
		parts := validName.FindStringSubmatch(d.Name())
		if !d.IsDir() || parts == nil {
			continue
		}
		stamp, err := time.Parse("20060102T150405Z", parts[1])
		if err != nil {
			continue
		}
		seq := 1
		if parts[2] != "" {
			seq, _ = strconv.Atoi(parts[2])
		}
		if _, err := os.Stat(filepath.Join(m.Dir, d.Name(), "topics.csv")); err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(m.Dir, d.Name(), "entries.csv")); err != nil {
			continue
		}
		out = append(out, Snapshot{d.Name(), stamp, seq})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time.Equal(out[j].Time) {
			return out[i].Sequence > out[j].Sequence
		}
		return out[i].Time.After(out[j].Time)
	})
	return out, nil
}
func writeCSV(path string, rows [][]string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	err = w.WriteAll(rows)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func (m *Manager) Snapshot(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	change := m.Store.Meta(ctx, "last_change")
	l, err := m.Store.Library(ctx)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(m.Dir, 0700); err != nil {
		return "", err
	}
	base := time.Now().UTC().Format("20060102T150405Z")
	name := base
	for i := 2; ; i++ {
		_, err = os.Stat(filepath.Join(m.Dir, name))
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", err
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
	tmp, err := os.MkdirTemp(m.Dir, ".tmp-"+base+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	topics := [][]string{{"topic", "kind", "aliases", "archived"}}
	entries := [][]string{{"id", "topic", "content", "sources", "updated", "updated_by"}}
	for _, t := range l {
		a, _ := json.Marshal(t.Aliases)
		arch := ""
		if t.Archived != nil {
			arch = *t.Archived
		}
		topics = append(topics, []string{t.Topic, t.Kind, string(a), arch})
		for _, e := range t.Entries {
			src, _ := json.Marshal(e.Sources)
			entries = append(entries, []string{strconv.FormatInt(e.ID, 10), t.Topic, e.Content, string(src), e.Updated, string(e.UpdatedBy)})
		}
	}
	if err = writeCSV(filepath.Join(tmp, "topics.csv"), topics); err != nil {
		return "", err
	}
	if err = writeCSV(filepath.Join(tmp, "entries.csv"), entries); err != nil {
		return "", err
	}
	if err = os.Rename(tmp, filepath.Join(m.Dir, name)); err != nil {
		return "", err
	}
	if err = m.Store.SetMeta(ctx, "last_snapshot", name); err != nil {
		return name, err
	}
	return name, m.Store.SetMeta(ctx, "last_snapshot_change", change)
}
func readCSV(path string, header []string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || strings.Join(rows[0], "\x00") != strings.Join(header, "\x00") {
		return nil, apperr.Invalid("Invalid snapshot CSV header.")
	}
	return rows[1:], nil
}
func (m *Manager) Read(name string) (memory.Library, error) {
	if validName.FindStringSubmatch(name) == nil {
		return nil, apperr.Invalid("Invalid snapshot name.")
	}
	dir := filepath.Join(m.Dir, name)
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil, apperr.Missing("Snapshot not found.")
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, apperr.Invalid("Invalid snapshot directory.")
	}
	topics, err := readCSV(filepath.Join(dir, "topics.csv"), []string{"topic", "kind", "aliases", "archived"})
	if err != nil {
		return nil, err
	}
	entries, err := readCSV(filepath.Join(dir, "entries.csv"), []string{"id", "topic", "content", "sources", "updated", "updated_by"})
	if err != nil {
		return nil, err
	}
	lib := memory.Library{}
	for _, row := range topics {
		t := memory.Topic{Topic: row[0], Kind: row[1], Entries: []memory.Entry{}}
		if err = json.Unmarshal([]byte(row[2]), &t.Aliases); err != nil || t.Aliases == nil {
			return nil, apperr.Invalid("Invalid aliases JSON.")
		}
		if row[3] != "" {
			a := row[3]
			t.Archived = &a
		}
		lib = append(lib, t)
	}
	for _, row := range entries {
		id, err := strconv.ParseInt(row[0], 10, 64)
		if err != nil {
			return nil, apperr.Invalid("Invalid snapshot entry id.")
		}
		idx := -1
		for i, t := range lib {
			if t.Topic == row[1] {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, apperr.Invalid("Snapshot entry references missing topic.")
		}
		e := memory.Entry{ID: id, Content: row[2], Updated: row[4], UpdatedBy: memory.Source(row[5])}
		if err = json.Unmarshal([]byte(row[3]), &e.Sources); err != nil || e.Sources == nil {
			return nil, apperr.Invalid("Invalid sources JSON.")
		}
		e.Sources, err = memory.NormalizeSources(e.Sources)
		if err != nil {
			return nil, err
		}
		lib[idx].Entries = append(lib[idx].Entries, e)
	}
	if err = memory.ValidateLibrary(lib); err != nil {
		return nil, err
	}
	lib.Sort()
	return lib, nil
}
func (m *Manager) Restore(ctx context.Context, name string, safety bool) error {
	if name == "" {
		ss, err := m.List()
		if err != nil {
			return err
		}
		if len(ss) == 0 {
			return apperr.Missing("No snapshots available.")
		}
		name = ss[0].Name
	}
	lib, err := m.Read(name)
	if err != nil {
		return err
	}
	if safety {
		if _, err = m.Snapshot(ctx); err != nil {
			return err
		}
	}
	if err = m.Store.Replace(ctx, lib); err != nil {
		return err
	}
	_, err = m.Snapshot(ctx)
	return err
}
func (m *Manager) Prune(ctx context.Context) error {
	ss, err := m.List()
	if err != nil {
		return err
	}
	for _, s := range m.Policy.Prune(ss, time.Now()) {
		if err = ctx.Err(); err != nil {
			return err
		}
		if len(ss) > 0 && s.Name == ss[0].Name {
			continue
		}
		if err = os.RemoveAll(filepath.Join(m.Dir, s.Name)); err != nil {
			return err
		}
	}
	return nil
}
