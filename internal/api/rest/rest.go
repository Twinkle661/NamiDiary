package rest

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/Twinkle661/NamiDiary/internal/apperr"
	"github.com/Twinkle661/NamiDiary/internal/assemble/dynamic"
	"github.com/Twinkle661/NamiDiary/internal/assemble/static"
	"github.com/Twinkle661/NamiDiary/internal/config"
	"github.com/Twinkle661/NamiDiary/internal/diary"
	"github.com/Twinkle661/NamiDiary/internal/httpx"
	"github.com/Twinkle661/NamiDiary/internal/memory"
	"github.com/Twinkle661/NamiDiary/internal/memory/backup"
	"github.com/Twinkle661/NamiDiary/internal/memory/worker"
)

type API struct {
	Config config.Config
	Diary  *diary.Service
	Memory *memory.Store
	Worker *worker.Worker
	Backup *backup.Manager
}

var Routes = []string{"GET /healthz", "GET /diary/search", "GET /diary/latest", "GET /diary/{date}", "POST /diary/{date}", "POST /diary/patch", "POST /diary/similar", "POST /diary/normalize", "POST /diary/init", "GET /memory/topics", "GET /memory/entries", "POST /memory/entries", "PUT /memory/entries/{id}", "DELETE /memory/entries/{id}", "PATCH /memory/topics/{topic}", "POST /memory/topics/{topic}/archive", "POST /memory/normalize", "POST /memory/dream", "POST /memory/init", "POST /memory/restore", "GET /memory/status", "GET /memory/snapshots", "GET /context/static", "POST /context/dynamic"}

func Number(r *http.Request, key string, def, lo, hi int) (int, error) {
	raw, ok := r.URL.Query()[key]
	if !ok {
		return def, nil
	}
	if len(raw) != 1 {
		return 0, apperr.Invalid(key + " must occur once.")
	}
	n, err := strconv.Atoi(raw[0])
	if err != nil || n < lo || n > hi {
		return 0, apperr.Invalid(fmt.Sprintf("%s must be an integer in %d..%d.", key, lo, hi))
	}
	return n, nil
}
func Content(v any) map[string]any { return map[string]any{"content": v} }
func Success() map[string]any      { return map[string]any{"status": "success", "message": "Completed."} }
func (a *API) Register(mux *http.ServeMux) {
	for _, route := range Routes {
		route := route
		mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			status, result, err := a.handle(route, r)
			if err != nil {
				httpx.Error(w, err, a.Config.App.Debug)
				return
			}
			httpx.JSON(w, status, result)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, apperr.Missing("Route not found."), a.Config.App.Debug)
	})
}
func (a *API) handle(route string, r *http.Request) (int, any, error) {
	ctx := r.Context()
	switch route {
	case "GET /healthz":
		if err := a.Diary.DB.PingContext(ctx); err != nil {
			return 0, nil, err
		}
		if err := a.Memory.DB.PingContext(ctx); err != nil {
			return 0, nil, err
		}
		return 200, map[string]string{"status": "ok"}, nil
	case "GET /diary/{date}":
		e, err := a.Diary.Get(ctx, r.PathValue("date"))
		return 200, Content(e.Markdown()), err
	case "GET /diary/latest":
		n, err := Number(r, "recent", 1, 1, a.Config.Diary.LatestMax)
		if err != nil {
			return 0, nil, err
		}
		es, err := a.Diary.Latest(ctx, diary.LatestQuery{N: n})
		return 200, Content(diary.LatestView(es)), err
	case "GET /diary/search":
		n, err := Number(r, "limit", a.Config.Diary.SearchLimit, 1, 10000)
		if err != nil {
			return 0, nil, err
		}
		out, err := a.Diary.Search(ctx, r.URL.Query().Get("from"), r.URL.Query().Get("to"), n, r.URL.Query()["query"])
		return 200, Content(out), err
	case "POST /diary/{date}":
		var b diary.WriteRequest
		if err := httpx.Body(r, &b); err != nil {
			return 0, nil, err
		}
		return 201, Success(), a.Diary.Add(ctx, r.PathValue("date"), b)
	case "POST /diary/patch":
		var b struct {
			Content string `json:"content"`
			Meta    struct {
				Keywords []string `json:"keywords"`
				Summary  string   `json:"summary"`
			} `json:"meta"`
		}
		if err := httpx.Body(r, &b); err != nil {
			return 0, nil, err
		}
		return 200, Success(), a.Diary.Patch(ctx, diary.WriteRequest{Content: b.Content, Meta: diary.Meta{Keywords: b.Meta.Keywords, Summary: b.Meta.Summary}})
	case "POST /diary/normalize", "POST /diary/init":
		return 200, Success(), a.Diary.Normalize(ctx, route == "POST /diary/init")
	case "POST /diary/similar", "POST /context/dynamic":
		n := a.Config.Diary.SimilarLimit
		if route == "POST /context/dynamic" {
			var err error
			if n, err = Number(r, "limit", n, 0, a.Config.Context.Dynamic.MaxLimit); err != nil {
				return 0, nil, err
			}
		}
		var b diary.SimilarRequest
		if err := httpx.Body(r, &b); err != nil {
			return 0, nil, err
		}
		if route == "POST /diary/similar" {
			out, err := a.Diary.Similar(ctx, b)
			return 200, Content(out), err
		}
		out, err := dynamic.Assemble(ctx, b, dynamic.Related(a.Diary, n))
		return 200, Content(out), err
	case "GET /context/static":
		n, err := Number(r, "recent", a.Config.Context.Static.DefaultRecent, 0, a.Config.Context.Static.MaxRecent)
		if err != nil {
			return 0, nil, err
		}
		out, err := static.Build(ctx, a.Diary, a.Memory, n)
		return 200, Content(out), err
	case "GET /memory/topics", "GET /memory/entries":
		l, err := a.Memory.Library(ctx)
		if err != nil {
			return 0, nil, err
		}
		if route == "GET /memory/topics" {
			return 200, Content(memory.Directory{Library: l}), nil
		}
		l, err = l.Select(r.URL.Query()["topic"])
		return 200, Content(l), err
	case "GET /memory/status":
		s, err := a.Worker.Status(ctx)
		return 200, Content(s), err
	case "GET /memory/snapshots":
		ss, err := a.Backup.List()
		out := []string{}
		for _, s := range ss {
			out = append(out, s.Name)
		}
		return 200, Content(out), err
	case "POST /memory/dream":
		date := r.URL.Query().Get("date")
		if raw := r.URL.Query()["date"]; len(raw) > 1 {
			return 0, nil, apperr.Invalid("date must occur once.")
		}
		if date != "" {
			if _, err := a.Diary.Get(ctx, date); err != nil {
				return 0, nil, err
			}
		} else {
			es, err := a.Diary.Latest(ctx, diary.LatestQuery{N: 1})
			if err != nil {
				return 0, nil, err
			}
			if len(es) == 0 {
				return 0, nil, apperr.Missing("No diary entries to dream over.")
			}
			// Default to the newest diary's date, not today: the usual retry is
			// the morning after a failed late-night entry.
			date = es[len(es)-1].Date
		}
		if err := a.Worker.RequestDream(date); err != nil {
			return 0, nil, err
		}
		return 202, map[string]string{"status": "accepted", "job": "dream"}, nil
	case "POST /memory/normalize", "POST /memory/init", "POST /memory/restore":
		job := "maintenance"
		recent := 0
		snapshot := ""
		if route == "POST /memory/init" {
			job = "init"
			var err error
			recent, err = Number(r, "recent", 0, 0, 365)
			if err != nil {
				return 0, nil, err
			}
		}
		if route == "POST /memory/restore" {
			job = "restore"
			var b struct {
				Snapshot string `json:"snapshot"`
			}
			if err := httpx.Body(r, &b); err != nil {
				return 0, nil, err
			}
			snapshot = b.Snapshot
		}
		err := a.Worker.Request(job, recent, snapshot)
		return 202, map[string]string{"status": "accepted", "job": job}, err
	}
	action := memory.Action{}
	code := 200
	switch route {
	case "POST /memory/entries":
		var b struct {
			Topic   string   `json:"topic"`
			Kind    string   `json:"kind"`
			Content string   `json:"content"`
			Sources []string `json:"sources"`
			Aliases []string `json:"aliases"`
		}
		if err := httpx.Body(r, &b); err != nil {
			return 0, nil, err
		}
		action = memory.Action{Op: "add", Topic: b.Topic, Kind: b.Kind, Content: b.Content, Sources: b.Sources, Aliases: b.Aliases}
		code = 201
	case "PUT /memory/entries/{id}", "DELETE /memory/entries/{id}":
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			return 0, nil, apperr.Invalid("ID must be positive.")
		}
		action.ID = id
		action.Op = "drop"
		if r.Method == "PUT" {
			var b struct {
				Content string   `json:"content"`
				Sources []string `json:"sources"`
			}
			if err := httpx.Body(r, &b); err != nil {
				return 0, nil, err
			}
			action.Op = "update"
			action.Content = b.Content
			action.Sources = b.Sources
		}
	case "PATCH /memory/topics/{topic}":
		var b struct {
			Aliases *[]string `json:"aliases"`
		}
		if err := httpx.Body(r, &b); err != nil {
			return 0, nil, err
		}
		if b.Aliases == nil {
			return 0, nil, apperr.Invalid("Aliases array is required.")
		}
		action = memory.Action{Op: "set_topic", Topic: r.PathValue("topic"), Aliases: *b.Aliases}
	case "POST /memory/topics/{topic}/archive":
		action = memory.Action{Op: "archive_project", Topic: r.PathValue("topic")}
	default:
		return 0, nil, apperr.Missing("Route not found.")
	}
	result, err := a.Worker.Apply(ctx, memory.User, []memory.Action{action})
	out := Success()
	if len(result.AddedIDs) > 0 {
		out["id"] = result.AddedIDs[0]
	}
	return code, out, err
}
