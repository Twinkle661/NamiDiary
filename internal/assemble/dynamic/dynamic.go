package dynamic

import (
	"context"
	"strings"

	"github.com/Twinkle661/NamiDiary/internal/diary"
)

type Section func(context.Context, diary.SimilarRequest) (string, error)

// Related retrieves the n most similar diary summaries.
func Related(d *diary.Service, n int) Section {
	return func(ctx context.Context, r diary.SimilarRequest) (string, error) {
		lines, err := d.SimilarN(ctx, r, n)
		if err != nil {
			return "", err
		}
		if len(lines) == 0 {
			return "", nil
		}
		return "<related_diaries>\n" + strings.Join(lines, "\n") + "\n</related_diaries>", nil
	}
}
func Assemble(ctx context.Context, r diary.SimilarRequest, sections ...Section) (string, error) {
	out := []string{}
	for _, s := range sections {
		part, err := s(ctx, r)
		if err != nil {
			return "", err
		}
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "\n\n"), nil
}
