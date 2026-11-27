package apperr

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExcerpt(t *testing.T) {
	if got := Excerpt([]byte("  \n")); got != "(empty body)" {
		t.Fatalf("empty: %q", got)
	}
	if got := Excerpt([]byte(" {\"a\":\n1} ")); got != "{\"a\":\n1}" {
		t.Fatalf("verbatim: %q", got)
	}
	long := Excerpt([]byte(strings.Repeat("凪", ExcerptMax+10)))
	if !strings.HasSuffix(long, "…(truncated)") || utf8.RuneCountInString(long) != ExcerptMax+len([]rune("…(truncated)")) {
		t.Fatalf("truncation: %d", utf8.RuneCountInString(long))
	}
	if got := Excerpt([]byte{0xff, 'x'}); !utf8.ValidString(got) {
		t.Fatal("invalid UTF-8 kept")
	}
}
