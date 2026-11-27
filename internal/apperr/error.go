package apperr

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type Error struct {
	Status        int
	Code, Message string
	Details       any
	Cause         error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return e.Code + ": " + e.Message
}
func (e *Error) Unwrap() error { return e.Cause }
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}
func Invalid(message string) *Error { return New(400, "INVALID_REQUEST", message) }
func Missing(message string) *Error { return New(404, "NOT_FOUND", message) }
func Busy(module string) *Error {
	return New(503, module+"_BUSY", "Work is in progress; retry in a moment.")
}

// ExcerptMax bounds provider response bodies copied into logs and job errors.
const ExcerptMax = 2000

// Excerpt returns a provider response body verbatim (trimmed, invalid UTF-8
// replaced) up to ExcerptMax code points, for diagnostics.
func Excerpt(b []byte) string {
	s := strings.TrimSpace(strings.ToValidUTF8(string(b), "\uFFFD"))
	if s == "" {
		return "(empty body)"
	}
	if utf8.RuneCountInString(s) <= ExcerptMax {
		return s
	}
	return string([]rune(s)[:ExcerptMax]) + "…(truncated)"
}
