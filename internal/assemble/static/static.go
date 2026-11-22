package static

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Twinkle661/NamiDiary/internal/diary"
	"github.com/Twinkle661/NamiDiary/internal/memory"
)

type Section func(context.Context) (string, error)

func Assemble(ctx context.Context, sections ...Section) (string, error) {
	parts := []string{}
	for _, section := range sections {
		s, err := section(ctx)
		if err != nil {
			return "", err
		}
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}
func Recent(d *diary.Service, n int) Section {
	return func(ctx context.Context) (string, error) {
		if n == 0 {
			return "", nil
		}
		es, err := d.Latest(ctx, diary.LatestQuery{N: n})
		if err != nil {
			return "", err
		}
		parts := []string{}
		for _, e := range es {
			parts = append(parts, e.Compact())
		}
		if len(parts) == 0 {
			return "", nil
		}
		return "<recent_diaries>\n" + strings.Join(parts, "\n") + "\n</recent_diaries>", nil
	}
}
func Core(lib memory.Library) Section {
	return func(context.Context) (string, error) {
		parts := []string{}
		for _, name := range []string{"profile", "preference"} {
			idx := lib.Resolve(name)
			if idx < 0 || len(lib[idx].Entries) == 0 {
				continue
			}
			lines := []string{}
			for _, e := range lib[idx].Entries {
				lines = append(lines, "- "+e.Content)
			}
			parts = append(parts, "<"+name+">\n"+strings.Join(lines, "\n")+"\n</"+name+">")
		}
		if len(parts) == 0 {
			return "", nil
		}
		return "<core_memory>\n" + strings.Join(parts, "\n") + "\n</core_memory>", nil
	}
}
func Index(lib memory.Library) Section {
	return func(context.Context) (string, error) {
		b, err := json.Marshal(memory.Directory{Library: lib})
		return "<memory_index>\n" + string(b) + "\n</memory_index>", err
	}
}
func Build(ctx context.Context, d *diary.Service, m *memory.Store, n int) (string, error) {
	lib, err := m.Library(ctx)
	if err != nil {
		return "", err
	}
	return Assemble(ctx, Recent(d, n), Core(lib), Index(lib))
}
