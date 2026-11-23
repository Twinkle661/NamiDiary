package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Twinkle661/NamiDiary/internal/api/rest"
	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/diary"
	"github.com/Twinkle661/NamiDiary/internal/httpx"
	"github.com/Twinkle661/NamiDiary/internal/memory"
)

var ToolNames = []string{"diary_search", "diary_latest", "diary_get", "diary_add", "diary_patch", "diary_normalize", "memory_list_topics", "memory_get_entries", "memory_add", "memory_update", "memory_drop", "memory_set_topic", "memory_archive_project", "memory_normalize", "memory_status"}

type Input struct {
	Date     string   `json:"date,omitempty"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Limit    *int     `json:"limit,omitempty"`
	Query    []string `json:"query,omitempty"`
	Recent   *int     `json:"recent,omitempty"`
	Content  string   `json:"content,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Location string   `json:"location,omitempty"`
	Topic    string   `json:"topic,omitempty"`
	Topics   []string `json:"topics,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	ID       int64    `json:"id,omitempty"`
	Sources  []string `json:"sources,omitempty"`
	Aliases  []string `json:"aliases,omitempty"`
}

func New(a *rest.API, version string) (*sdk.Server, http.Handler) {
	server := sdk.NewServer(&sdk.Implementation{Name: "namidiary", Version: version}, nil)
	for _, name := range ToolNames {
		name := name
		fields := map[string]string{}
		required := []string{}
		description := ""
		readOnly, idempotent, destructive := false, false, false
		switch name {
		case "diary_search":
			fields = map[string]string{"from": "string", "to": "string", "limit": "integer", "query": "array"}
			required = []string{"from", "to"}
			description = "Search diary bodies and keywords within an inclusive date range; all query terms must match."
			readOnly = true
		case "diary_latest":
			fields = map[string]string{"recent": "integer"}
			description = fmt.Sprintf("Read the newest entries as full Markdown, oldest first. recent defaults to 1; maximum %d.", a.Config.Diary.LatestMax)
			readOnly = true
		case "diary_get":
			fields = map[string]string{"date": "string"}
			required = []string{"date"}
			description = "Read one diary by yyyy-MM-dd date, including YAML front matter."
			readOnly = true
		case "diary_add":
			fields = map[string]string{"date": "string", "content": "string", "keywords": "array", "summary": "string", "location": "string"}
			required = []string{"date", "content"}
			description = "Write a new dated diary. Memory is updated afterwards in the background. Summary supports semantic retrieval."
		case "diary_patch":
			fields = map[string]string{"content": "string", "keywords": "array", "summary": "string"}
			required = []string{"content"}
			description = fmt.Sprintf("Append to the latest diary; fails if it was created more than %s ago. Summary replaces the previous summary; location cannot be changed.", a.Config.Diary.PatchWindow)
		case "diary_normalize":
			description = "Repairs the search index from the diary files. Safe to run at any time."
			idempotent = true
		case "memory_list_topics":
			description = "List memory topics and their aliases, excluding projects pending archive."
			readOnly = true
		case "memory_get_entries":
			fields = map[string]string{"topics": "array"}
			description = "Read entries by topic names or aliases; omit topics to read the full library."
			readOnly = true
		case "memory_add":
			fields = map[string]string{"topic": "string", "kind": "string", "content": "string", "sources": "array", "aliases": "array"}
			required = []string{"topic", "content"}
			description = "Add a durable fact. Kind is required only for a new topic; aliases are accepted only when creating a topic."
		case "memory_update":
			fields = map[string]string{"id": "integer", "content": "string", "sources": "array"}
			required = []string{"id", "content"}
			description = "Replace the full content of an entry; sources are merged with existing dates."
			destructive = true
		case "memory_drop":
			fields = map[string]string{"id": "integer"}
			required = []string{"id"}
			description = "Delete one memory entry by id."
			destructive = true
		case "memory_set_topic":
			fields = map[string]string{"topic": "string", "aliases": "array"}
			required = []string{"topic", "aliases"}
			description = "Replace a topic's alias list. Names cannot be changed."
			idempotent = true
		case "memory_archive_project":
			fields = map[string]string{"topic": "string"}
			required = []string{"topic"}
			description = "Mark a completed project for background distillation into area topics."
			idempotent = true
		case "memory_normalize":
			description = "Run integrity repair, project archiving, compaction and backup retention in the background."
			idempotent = true
		case "memory_status":
			description = "Read background job progress, recent results and pending work."
			readOnly = true
		}
		if strings.HasPrefix(name, "memory_") {
			description += fmt.Sprintf("\nEntries are short facts, at most %d Unicode characters.\ncore: fixed profile (who the user is) and preference (the user's tastes, habits and agreements).\npeople: a specific other person.\narea: an ongoing domain without an end date.\nproject: a goal with an endpoint.", a.Config.Memory.EntryMaxChars)
		}
		description += " A busy error means retry in a moment."
		props := map[string]any{}
		for field, typ := range fields {
			p := map[string]any{"type": typ}
			if typ == "array" {
				p["items"] = map[string]string{"type": "string"}
			}
			props[field] = p
		}
		schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false, "required": required}
		destructiveCopy := destructive
		server.AddTool(&sdk.Tool{Name: name, Description: description, InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: readOnly, IdempotentHint: idempotent, DestructiveHint: &destructiveCopy}}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			var in Input
			raw := req.Params.Arguments
			if len(raw) == 0 {
				raw = json.RawMessage(`{}`)
			}
			err := validateInput(raw, fields, required)
			if err == nil {
				err = json.Unmarshal(raw, &in)
			}
			var out any
			if err == nil {
				out, err = call(ctx, a, name, in)
			}
			var text string
			if err != nil {
				_, body := httpx.ErrorBody(err, a.Config.App.Debug)
				b, _ := json.Marshal(body)
				text = string(b)
			} else if s, ok := out.(string); ok {
				text = s
			} else {
				b, _ := json.Marshal(out)
				text = string(b)
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}, IsError: err != nil}, nil
		})
	}
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true, MaxRequestBodyBytes: 1 << 20})
	return server, handler
}
func call(ctx context.Context, a *rest.API, name string, in Input) (any, error) {
	switch name {
	case "diary_get":
		e, err := a.Diary.Get(ctx, in.Date)
		return e.Markdown(), err
	case "diary_latest":
		n := 1
		if in.Recent != nil {
			n = *in.Recent
		}
		if n < 1 || n > a.Config.Diary.LatestMax {
			return nil, apperr.Invalid("Recent out of bounds.")
		}
		es, err := a.Diary.Latest(ctx, diary.LatestQuery{N: n})
		return diary.Markdown(es), err
	case "diary_search":
		n := a.Config.Diary.SearchLimit
		if in.Limit != nil {
			n = *in.Limit
		}
		return a.Diary.Search(ctx, in.From, in.To, n, in.Query)
	case "diary_add":
		return rest.Success(), a.Diary.Add(ctx, in.Date, diary.WriteRequest{Content: in.Content, Meta: diary.Meta{Keywords: in.Keywords, Summary: in.Summary, Location: in.Location}})
	case "diary_patch":
		return rest.Success(), a.Diary.Patch(ctx, diary.WriteRequest{Content: in.Content, Meta: diary.Meta{Keywords: in.Keywords, Summary: in.Summary}})
	case "diary_normalize":
		return rest.Success(), a.Diary.Normalize(ctx, false)
	case "memory_list_topics", "memory_get_entries":
		l, err := a.Memory.Library(ctx)
		if err != nil {
			return nil, err
		}
		if name == "memory_list_topics" {
			return memory.Directory{Library: l}, nil
		}
		return l.Select(in.Topics)
	case "memory_status":
		return a.Worker.Status(ctx)
	case "memory_normalize":
		return map[string]string{"status": "accepted", "job": "maintenance"}, a.Worker.Request("maintenance", 0, "")
	}
	action := memory.Action{Op: strings.TrimPrefix(name, "memory_"), Topic: in.Topic, Kind: in.Kind, ID: in.ID, Content: in.Content, Sources: in.Sources, Aliases: in.Aliases}
	result, err := a.Worker.Apply(ctx, memory.Agent, []memory.Action{action})
	out := rest.Success()
	if len(result.AddedIDs) > 0 {
		out["id"] = result.AddedIDs[0]
	}
	return out, err
}

func validateInput(raw json.RawMessage, fields map[string]string, required []string) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return apperr.Invalid("Tool input must be an object.")
	}
	for _, key := range required {
		if _, ok := obj[key]; !ok {
			return apperr.Invalid("Missing required field: " + key)
		}
	}
	for key, value := range obj {
		typ, ok := fields[key]
		if !ok {
			return apperr.Invalid("Unknown tool field: " + key)
		}
		if string(value) == "null" {
			return apperr.Invalid("Null is not allowed for " + key)
		}
		var err error
		switch typ {
		case "string":
			var v string
			err = json.Unmarshal(value, &v)
		case "integer":
			var v int64
			err = json.Unmarshal(value, &v)
		case "array":
			var v []json.RawMessage
			err = json.Unmarshal(value, &v)
			if err == nil {
				for _, item := range v {
					var text string
					if string(item) == "null" {
						return apperr.Invalid("Array items must be strings.")
					}
					if err = json.Unmarshal(item, &text); err != nil {
						break
					}
				}
			}
		}
		if err != nil {
			return apperr.Invalid("Invalid type for " + key)
		}
	}
	return nil
}
