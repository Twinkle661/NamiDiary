package static

import (
	"context"
	"strings"
	"testing"

	"github.com/Twinkle661/NamiDiary/internal/memory"
)

func TestSectionsOrderAndEmptyOmission(t *testing.T) {
	l := memory.Empty()
	l[0].Entries = []memory.Entry{{ID: 1, Content: "identity"}}
	out, err := Assemble(context.Background(), func(context.Context) (string, error) { return "<recent_diaries>summary</recent_diaries>", nil }, Core(l), Index(l))
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := strings.Index(out, "<recent_diaries>"), strings.Index(out, "<core_memory>"), strings.Index(out, "<memory_index>")
	if a < 0 || b <= a || c <= b || strings.Contains(out, "<preference>") {
		t.Fatal(out)
	}
	out, err = Assemble(context.Background(), Core(memory.Empty()))
	if err != nil || out != "" {
		t.Fatal("empty section included")
	}
}
