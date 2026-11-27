# API

[openapi.yaml](../openapi.yaml) is the authoritative schema for every REST route.

## REST

All routes except `/healthz` require `Authorization: Bearer TOKEN`. Bodies are JSON with a 1 MiB limit. Unknown object fields are rejected. Read responses use `{"content": ...}`; writes use `{"status":"success","message":"Completed."}`, with `id` for a new memory entry. Background jobs return HTTP 202 with `{"status":"accepted","job":"..."}`. See [openapi.yaml](openapi.yaml) for schemas, parameters, response codes, and all registered REST routes.

| Method | Path | Input / behavior |
|---|---|---|
| GET | `/healthz` | Unauthenticated database ping |
| GET | `/diary/search` | Required `from`, `to`; optional `limit`, repeated `query`; every term matches body or keywords |
| GET | `/diary/latest` | `recent=1`, up to configured maximum; oldest first; full Markdown only when `recent=1`, summary lines otherwise |
| GET | `/diary/{date}` | Full Markdown |
| POST | `/diary/{date}` | `content`, optional `meta: {keywords,summary,location}`; HTTP 201 |
| POST | `/diary/patch` | `content`, optional `meta: {keywords,summary}`; latest entry only |
| POST | `/diary/similar` | `user_message`, optional `recent: [{role,text}]`; most similar last |
| POST | `/diary/normalize` | Safe synchronous index repair; reuses vectors |
| POST | `/diary/init` | Full synchronous index rebuild; REST only |
| GET | `/memory/topics` | Directory including empty kinds, excluding archived projects |
| GET | `/memory/entries` | Optional repeated `topic` names or aliases; none means all |
| POST | `/memory/entries` | `topic`, `content`, optional `kind`, `sources`, `aliases`; HTTP 201 |
| PUT | `/memory/entries/{id}` | Replace `content`; optional `sources` are unioned |
| DELETE | `/memory/entries/{id}` | Delete entry, clean up empty non-core topic |
| PATCH | `/memory/topics/{topic}` | Replace `aliases` array |
| POST | `/memory/topics/{topic}/archive` | Mark a project for archive maintenance |
| POST | `/memory/normalize` | Background repair/archive/compact/snapshot/prune |
| POST | `/memory/dream` | `date=yyyy-MM-dd` (default: newest diary, not today); run a dream now as of that date, e.g. to retry last night's failed dream. REST only |
| POST | `/memory/init` | `recent=0..365`; safety snapshot, reset, sequential historical replay |
| POST | `/memory/restore` | `{"snapshot":"TIMESTAMP"}` or `{}` for newest; background job |
| GET | `/memory/status` | Busy state, pending dream, last 20 jobs, violations, archives, latest snapshot |
| GET | `/memory/snapshots` | Names, newest first; REST only |
| GET | `/context/static` | `recent=0..max_recent`; summaries, core facts, directory |
| POST | `/context/dynamic` | Same body as similar; `limit=0..max_limit` (default `diary.similar_limit`) results; related diary section |

Similar uses nonempty `recent` in preference to `user_message`, formats each turn as `role: text`, and keeps the last configured number of Unicode code points. Role is any nonblank string. Static and dynamic context do not deduplicate against each other.

Errors use `{"status":"error","code":"...","message":"...","details":{...}}`. Details are optional. Busy errors (`MEMORY_BUSY`, `DIARY_BUSY`) return HTTP 503 and `Retry-After: 10`. Other codes distinguish validation (400), authentication (401), origin rejection (403), missing resources (404), conflicts/caps (409), provider failures (502), and internal/parse failures (500). Debug mode adds diagnostic information to server errors. Job failures appear in `/memory/status`, not in the already-returned 202 response. Provider rejections (LLM or embedding) are logged with the HTTP status and up to 2000 code points of the provider's response body, and the same detail appears in the failed job's `error`; to retry a failed dream, call `POST /memory/dream`.

## MCP

Connect a streamable HTTP MCP client to `http://localhost:8188/mcp`, supplying the bearer token. The handler is stateless. `mcp.allowed_hosts` and `mcp.allowed_origins` contain hostnames without scheme/port; configure public proxy hostnames explicitly. No-Origin clients are accepted; browser origins must be allow-listed.

| Tools | Behavior |
|---|---|
| `diary_search`, `diary_latest`, `diary_get` | Shared diary reads |
| `diary_add`, `diary_patch` | Flat metadata fields; patch has no date or location |
| `diary_normalize` | Safe index repair |
| `memory_list_topics`, `memory_get_entries` | Directory and facts; get accepts `topics` array |
| `memory_add`, `memory_update`, `memory_drop` | Fact mutation; same semantics as REST |
| `memory_set_topic`, `memory_archive_project` | Alias replacement / project completion |
| `memory_normalize`, `memory_status` | Maintenance and progress |

Init, restore, snapshots and similar are not MCP tools. Each tool returns one text content item; structured results are JSON text. Tool errors set `isError=true` with the REST error shape. The server identifies as `namidiary`; build version defaults to `dev`.
