package agents

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/Twinkle661/NamiDiary/internal/config"
	"github.com/Twinkle661/NamiDiary/internal/diary"
	"github.com/Twinkle661/NamiDiary/internal/llm"
	"github.com/Twinkle661/NamiDiary/internal/memory"
)

//go:embed prompts/*.md
var prompts embed.FS

type Completer interface {
	Complete(context.Context, string, []llm.Message) (string, error)
}
type DiaryReader interface {
	Latest(context.Context, diary.LatestQuery) ([]diary.Entry, error)
}
type Runner struct {
	Store       *memory.Store
	Diary       DiaryReader
	Client      Completer
	Config      config.Memory
	Location    *time.Location
	AfterChange func(context.Context) error
	Progress    func(string, int)
	Backoff     time.Duration
}

func Parse(raw string) ([]memory.Action, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		_, rest, ok := strings.Cut(s, "\n")
		if !ok || !strings.HasSuffix(rest, "```") {
			return nil, fmt.Errorf("invalid code fence")
		}
		s = strings.TrimSpace(strings.TrimSuffix(rest, "```"))
	}
	var out struct {
		Actions *[]memory.Action `json:"actions"`
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected one JSON object")
	}
	if out.Actions == nil {
		return nil, fmt.Errorf("actions must be an array")
	}
	return *out.Actions, nil
}
func (r *Runner) prompt(agent, today string) (string, error) {
	load := func(name string) (string, error) {
		if r.Config.PromptsDir != "" {
			b, err := os.ReadFile(filepath.Join(r.Config.PromptsDir, name+".md"))
			if err == nil {
				return string(b), nil
			}
			if !os.IsNotExist(err) {
				return "", err
			}
		}
		b, err := prompts.ReadFile("prompts/" + name + ".md")
		return string(b), err
	}
	shared, err := load("shared_rules")
	if err != nil {
		return "", err
	}
	main, err := load(agent)
	if err != nil {
		return "", err
	}
	t, err := template.New(agent).Parse(main)
	if err != nil {
		return "", err
	}
	if _, err = t.New("shared_rules").Parse(shared); err != nil {
		return "", err
	}
	an, un := r.Config.Names.Agent, r.Config.Names.User
	if an == "" {
		an = "the agent"
	}
	if un == "" {
		un = "the user"
	}
	data := map[string]any{"AgentName": an, "UserName": un, "Language": r.Config.Language, "EntryMaxChars": r.Config.EntryMaxChars, "Limits": r.Config.Limits, "Today": today}
	var b bytes.Buffer
	err = t.Execute(&b, data)
	return b.String(), err
}
func section(tag string, v any) string {
	b, _ := json.Marshal(v)
	return "<" + tag + ">" + string(b) + "</" + tag + ">\n"
}
func (r *Runner) Run(ctx context.Context, agent string, until *string, project string) error {
	lib, err := r.Store.Library(ctx)
	if err != nil {
		return err
	}
	today := time.Now().In(r.Location).Format("2006-01-02")
	if until != nil {
		today = *until
	}
	system, err := r.prompt(agent, today)
	if err != nil {
		return err
	}
	limitJSON, _ := json.Marshal(r.Config.Limits)
	var limits map[string]any
	_ = json.Unmarshal(limitJSON, &limits)
	limits["entry_max_chars"] = r.Config.EntryMaxChars
	data := "<today>" + today + "</today>\n" + section("limits", limits) + section("counts", lib.Counts())
	opts := memory.ApplyOptions{}
	opts.AllowedOps = map[string]bool{"add": true, "update": true, "drop": true, "set_topic": true, "archive_project": true}
	switch agent {
	case "dream":
		es, err := r.Diary.Latest(ctx, diary.LatestQuery{N: r.Config.Dream.ContextDiaries, Until: until})
		if err != nil {
			return err
		}
		data += section("memory_library", lib) + "<diaries>\n"
		for _, e := range es {
			data += "<diary date=\"" + e.Date + "\">\n" + e.Markdown() + "</diary>\n"
		}
		data += "</diaries>"
	case "archive":
		subset := memory.Library{}
		for _, t := range lib {
			if t.Kind == "area" || t.Topic == project {
				subset = append(subset, t)
			}
		}
		data += section("memory_library", subset)
		opts.AllowedOps = map[string]bool{"add": true, "update": true}
		opts.AllowedKindsForWrite = map[string]bool{"area": true}
		opts.PostCheck = func(l *memory.Library) error {
			i := l.Resolve(project)
			if i < 0 || (*l)[i].Kind != "project" || (*l)[i].Archived == nil {
				return fmt.Errorf("archive target is not an archived project")
			}
			*l = append((*l)[:i], (*l)[i+1:]...)
			return nil
		}
	case "compact":
		data += section("memory_library", lib) + section("violations", memory.Violations(lib, r.Config.Limits))
		delete(opts.AllowedOps, "archive_project")
		opts.PostCheck = func(l *memory.Library) error {
			v := memory.Violations(*l, r.Config.Limits)
			if len(v) > 0 {
				return fmt.Errorf("still over limit: %s", section("violations", v))
			}
			return nil
		}
	default:
		return fmt.Errorf("unknown agent")
	}
	original := []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: data}}
	messages := original
	var last error
	for attempt := 1; attempt <= r.Config.LLM.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.Progress != nil {
			r.Progress(agent+"_agent", attempt)
		}
		slog.Info("Memory agent attempt", "agent", agent, "attempt", attempt)
		raw, err := r.Client.Complete(ctx, agent, messages)
		if err != nil {
			last = err
			messages = original
			if attempt < r.Config.LLM.MaxAttempts {
				delay := r.Backoff
				if delay == 0 {
					delay = 2 * time.Second
				}
				timer := time.NewTimer(delay * time.Duration(1<<min(attempt-1, 5)))
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
			continue
		}
		actions, err := Parse(raw)
		if err == nil {
			var result memory.BatchResult
			result, err = r.Store.Apply(ctx, memory.Dream, actions, opts)
			if err == nil {
				counts := map[string]int{}
				for _, action := range actions {
					counts[action.Op]++
				}
				slog.Info("Memory actions applied", "agent", agent, "actions", len(actions), "operations", counts)
				if result.Changed && r.AfterChange != nil {
					return r.AfterChange(ctx)
				}
				return nil
			}
		}
		last = err
		messages = append(append([]llm.Message{}, original...), llm.Message{Role: "assistant", Content: raw}, llm.Message{Role: "user", Content: "Your previous output was rejected: " + err.Error() + ". Nothing was applied. Return the complete corrected JSON object."})
	}
	return fmt.Errorf("%s agent failed after %d attempts: %w", agent, r.Config.LLM.MaxAttempts, last)
}
