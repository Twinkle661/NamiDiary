package mcp

import "testing"

func TestStrictInput(t *testing.T) {
	fields := map[string]string{"content": "string", "aliases": "array"}
	for _, raw := range []string{`null`, `{"content":"fact","date":"forbidden"}`, `{"content":null}`, `{"aliases":[]}`, `{"content":"fact","aliases":[null]}`} {
		if validateInput([]byte(raw), fields, []string{"content"}) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
