package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/config"
)

type Source string

const (
	Dream Source = "dream"
	Agent Source = "agent"
	User  Source = "user"
)

type Entry struct {
	ID        int64    `json:"id"`
	Content   string   `json:"content"`
	Sources   []string `json:"sources"`
	Updated   string   `json:"updated"`
	UpdatedBy Source   `json:"updated_by"`
}
type Topic struct {
	Topic    string   `json:"topic"`
	Kind     string   `json:"kind"`
	Aliases  []string `json:"aliases"`
	Archived *string  `json:"archived"`
	Entries  []Entry  `json:"entries"`
}
type Library []Topic
type Action struct {
	Op      string   `json:"op"`
	Topic   string   `json:"topic,omitempty"`
	Kind    string   `json:"kind,omitempty"`
	ID      int64    `json:"id,omitempty"`
	Content string   `json:"content,omitempty"`
	Sources []string `json:"sources,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}
type BatchResult struct {
	AddedIDs []int64 `json:"added_ids"`
	Changed  bool    `json:"changed"`
}
type ApplyOptions struct {
	AllowedOps           map[string]bool
	AllowedKindsForWrite map[string]bool
	PostCheck            func(*Library) error
	ForbidCreateTopic    bool
}
type Violation struct {
	Topic   string `json:"topic,omitempty"`
	Kind    string `json:"kind"`
	Entries int    `json:"entries,omitempty"`
	Topics  int    `json:"topics,omitempty"`
	Max     int    `json:"max"`
}

func Violations(lib Library, limits config.Limits) []Violation {
	out := []Violation{}
	counts := map[string]int{}
	for _, t := range lib {
		counts[t.Kind]++
		limit := limits.For(t.Kind, t.Topic)
		if len(t.Entries) > limit.MaxEntries {
			out = append(out, Violation{Topic: t.Topic, Kind: t.Kind, Entries: len(t.Entries), Max: limit.MaxEntries})
		}
	}
	for _, k := range []string{"people", "area", "project"} {
		max := limits.For(k, "").MaxTopics
		if counts[k] > max {
			out = append(out, Violation{Kind: k, Topics: counts[k], Max: max})
		}
	}
	return out
}
func (l Library) Counts() map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, k := range []string{"core", "people", "area", "project"} {
		out[k] = map[string]int{"topics": 0, "entries": 0}
	}
	for _, t := range l {
		out[t.Kind]["topics"]++
		out[t.Kind]["entries"] += len(t.Entries)
	}
	return out
}
func (l Library) Resolve(ref string) int {
	ref = strings.TrimSpace(ref)
	for i, t := range l {
		if t.Topic == ref {
			return i
		}
	}
	ref = strings.ToLower(ref)
	for i, t := range l {
		if strings.ToLower(t.Topic) == ref {
			return i
		}
	}
	for i, t := range l {
		for _, a := range t.Aliases {
			if strings.ToLower(a) == ref {
				return i
			}
		}
	}
	return -1
}
func (l Library) Find(id int64) (int, int) {
	for i, t := range l {
		for j, e := range t.Entries {
			if e.ID == id {
				return i, j
			}
		}
	}
	return -1, -1
}
func (l Library) Sort() {
	ranks := map[string]int{"core": 0, "people": 1, "area": 2, "project": 3}
	sort.Slice(l, func(i, j int) bool {
		if l[i].Kind != l[j].Kind {
			return ranks[l[i].Kind] < ranks[l[j].Kind]
		}
		return strings.ToLower(l[i].Topic) < strings.ToLower(l[j].Topic)
	})
	for i := range l {
		sort.Slice(l[i].Entries, func(a, b int) bool { return l[i].Entries[a].ID < l[i].Entries[b].ID })
	}
}
func (l Library) Select(refs []string) (Library, error) {
	if len(refs) == 0 {
		return l, nil
	}
	selected := map[int]bool{}
	unknown := []string{}
	for _, r := range refs {
		i := l.Resolve(r)
		if i < 0 {
			unknown = append(unknown, r)
		} else {
			selected[i] = true
		}
	}
	if len(unknown) > 0 {
		return nil, apperr.Missing("Unknown topics: " + strings.Join(unknown, ", "))
	}
	out := Library{}
	for i, t := range l {
		if selected[i] {
			out = append(out, t)
		}
	}
	return out, nil
}

type Directory struct{ Library Library }

func (d Directory) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for n, k := range []string{"core", "people", "area", "project"} {
		if n > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		b.Write(key)
		b.WriteString(":{")
		first := true
		for _, t := range d.Library {
			if t.Kind != k || t.Archived != nil {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			name, _ := json.Marshal(t.Topic)
			aliases := t.Aliases
			if aliases == nil {
				aliases = []string{}
			}
			v, _ := json.Marshal(aliases)
			b.Write(name)
			b.WriteByte(':')
			b.Write(v)
		}
		b.WriteByte('}')
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
func name(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || utf8.RuneCountInString(s) > 64 {
		return "", apperr.Invalid("Topic names and aliases must contain 1..64 characters.")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", apperr.Invalid("Names must not contain control characters.")
		}
	}
	return s, nil
}
func aliases(l Library, idx int, raw []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{strings.ToLower(l[idx].Topic): true}
	for _, r := range raw {
		s, err := name(r)
		if err != nil {
			return nil, err
		}
		low := strings.ToLower(s)
		if seen[low] {
			continue
		}
		seen[low] = true
		if other := l.Resolve(s); other >= 0 && other != idx {
			return nil, apperr.New(409, "ALIAS_CONFLICT", "Alias conflicts with another topic.")
		}
		out = append(out, s)
	}
	if len(out) > 10 {
		return nil, apperr.Invalid("At most 10 aliases are allowed.")
	}
	return out, nil
}
func NormalizeSources(xs []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range xs {
		t, err := time.Parse("2006-01-02", s)
		if err != nil || t.Format("2006-01-02") != s {
			return nil, apperr.Invalid("Sources must be yyyy-MM-dd dates.")
		}
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	sort.Strings(out)
	return out, nil
}
func ValidateLibrary(l Library) error {
	seen := map[string]bool{}
	ids := map[int64]bool{}
	core := map[string]bool{}
	for _, t := range l {
		n, err := name(t.Topic)
		if err != nil || n != t.Topic {
			return apperr.Invalid("Invalid snapshot topic name.")
		}
		if t.Kind != "core" && t.Kind != "people" && t.Kind != "area" && t.Kind != "project" {
			return apperr.Invalid("Invalid topic kind.")
		}
		if t.Kind == "core" {
			if t.Topic != "profile" && t.Topic != "preference" {
				return apperr.Invalid("Invalid core topic.")
			}
			core[t.Topic] = true
		} else if t.Topic == "profile" || t.Topic == "preference" {
			return apperr.Invalid("Reserved core topic name.")
		}
		if t.Archived != nil {
			if t.Kind != "project" {
				return apperr.Invalid("Only projects may be archived.")
			}
			if _, err := time.Parse(time.RFC3339Nano, *t.Archived); err != nil {
				return apperr.Invalid("Invalid archive time.")
			}
		}
		if len(t.Aliases) > 10 {
			return apperr.Invalid("Too many aliases.")
		}
		for _, s := range append([]string{t.Topic}, t.Aliases...) {
			n, err := name(s)
			if err != nil || n != s || seen[strings.ToLower(s)] {
				return apperr.Invalid("Invalid or conflicting names and aliases.")
			}
			seen[strings.ToLower(s)] = true
		}
		for _, e := range t.Entries {
			if e.ID < 1 || ids[e.ID] || strings.TrimSpace(e.Content) == "" {
				return apperr.Invalid("Invalid entry id or content.")
			}
			ids[e.ID] = true
			if _, err := NormalizeSources(e.Sources); err != nil {
				return err
			}
			if e.UpdatedBy != Dream && e.UpdatedBy != Agent && e.UpdatedBy != User {
				return apperr.Invalid("Invalid updated_by.")
			}
			if _, err := time.Parse(time.RFC3339Nano, e.Updated); err != nil {
				return apperr.Invalid("Invalid updated timestamp.")
			}
		}
	}
	if !core["profile"] || !core["preference"] {
		return fmt.Errorf("both core topics are required")
	}
	return nil
}
