package diary

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
)

const Timestamp = "2006-01-02T15:04:05.000Z"

type Entry struct {
	Date     string   `yaml:"date"`
	Created  string   `yaml:"created"`
	Updated  string   `yaml:"updated"`
	Keywords []string `yaml:"keywords,flow"`
	Summary  string   `yaml:"summary"`
	Location string   `yaml:"location,omitempty"`
	Body     string   `yaml:"-"`
	FilePath string   `yaml:"-"`
}

func (e Entry) Markdown() string {
	if e.Keywords == nil {
		e.Keywords = []string{}
	}
	b, _ := yaml.Marshal(e)
	return "---\n" + string(b) + "---\n" + strings.TrimSpace(e.Body) + "\n"
}
func (e Entry) Compact() string {
	return e.Date + "-[" + strings.Join(e.Keywords, ",") + "]" + e.Summary
}
func ValidDate(s string) bool {
	d, e := time.Parse("2006-01-02", s)
	return e == nil && d.Format("2006-01-02") == s
}
func Parse(b []byte) (Entry, error) {
	var e Entry
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return e, fmt.Errorf("missing front matter")
	}
	head, body, ok := strings.Cut(s[4:], "\n---\n")
	if !ok {
		return e, fmt.Errorf("unterminated front matter")
	}
	var n map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(head), &n); err != nil {
		return e, err
	}
	for k, dst := range map[string]*string{"date": &e.Date, "created": &e.Created, "updated": &e.Updated, "summary": &e.Summary, "location": &e.Location} {
		if node, ok := n[k]; ok && node.Tag != "!!null" {
			if node.Kind != yaml.ScalarNode {
				return e, fmt.Errorf("%s must be a scalar", k)
			}
			*dst = node.Value
		}
	}
	e.Keywords = []string{}
	if node, ok := n["keywords"]; ok && node.Tag != "!!null" {
		switch node.Kind {
		case yaml.ScalarNode:
			e.Keywords = append(e.Keywords, node.Value)
		case yaml.SequenceNode:
			if err := node.Decode(&e.Keywords); err != nil {
				return e, err
			}
		default:
			return e, fmt.Errorf("invalid keywords")
		}
	}
	if !ValidDate(e.Date) {
		return e, fmt.Errorf("invalid diary date")
	}
	e.Body = strings.TrimSpace(body)
	return e, nil
}
func Read(path string) (Entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	e, err := Parse(b)
	if err != nil {
		return e, &apperr.Error{Status: 500, Code: "DIARY_PARSE_ERROR", Message: "Unable to parse diary file.", Cause: fmt.Errorf("%s: %w", path, err)}
	}
	e.FilePath = path
	return e, nil
}
