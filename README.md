# NamiDiary

A single-user Go service for Markdown diaries, keyword and vector retrieval, and a small structured memory library. REST and MCP share the same services. New diaries trigger background memory extraction; finished projects are distilled into areas, and over-limit topics are compacted.

There is no UI, notes module, or `/prompt` compatibility endpoint.

## Quick start

Requirements: Go 1.25+, a C compiler, SQLite development headers, and credentials for OpenAI-compatible embedding and chat-completion providers. On Debian, install `build-essential libsqlite3-dev`. The official MCP SDK requires Go 1.25. Build with CGO and FTS5 enabled.

```sh
cp config.example.yml config.yml
cp .env.example .env
# Edit .env with three separate credentials; adjust config.yml for your providers.
make build
./bin/namidiary
```

The default listener is port 8188. Set the same API token in your client environment before using the examples:

```sh
export NAMIDIARY_APP__API_KEY='your-configured-api-token'
curl http://localhost:8188/healthz
curl -H "Authorization: Bearer $NAMIDIARY_APP__API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"content":"A diary entry.","meta":{"keywords":["reading"],"summary":"A day spent reading.","location":"Home"}}' \
  http://localhost:8188/diary/2026-01-01
curl -H "Authorization: Bearer $NAMIDIARY_APP__API_KEY" \
  'http://localhost:8188/context/static?recent=7'
```

Dates can be backdated. Future dates are checked against midnight in the configured timezone. Patch always appends to the newest dated entry, and its window is measured from that entry's creation timestamp, not its date. A successful add returns before memory extraction finishes. Poll `/memory/status` for background progress and failures.

## Storage and recovery

```text
data/
  diary/YYYY/MM/MM-DD.md   # canonical diary files
  diary.db                # derived FTS5/vector index
  dbStatus.json           # embedding URL/model used for the index
  memory.db               # canonical structured memory
  memory/TIMESTAMP/
    topics.csv
    entries.csv
```

Markdown front matter contains `date`, `created`, `updated`, `keywords`, `summary`, and optional `location`. Legacy scalar keywords, YAML dates, and absent metadata are accepted. Get/latest return full Markdown; search/similar return `DATE-[keywords]summary` strings.

Markdown is the source of truth, but only add/patch touch it; every read (get, latest, search, similar) is served from `diary.db`. If embedding fails during a write, the entry is still stored and readable but has no vector until `POST /diary/normalize` backfills it. Normalize reuses unchanged summary vectors. `POST /diary/init` forces fresh embeddings and never touches memory. With `diary.active_normalize` (default `true`) the index is also re-synced from Markdown at startup and after every add/patch, reusing unchanged vectors, so external edits appear after the next restart or write; set it to `false` to have writes upsert only their own row. Either way, `POST /diary/normalize` makes external edits visible immediately.

Memory lives in a separate database. Every worker-controlled committed change writes a CSV snapshot. Memory restore validates both CSV files, takes a safety snapshot, then replaces the library atomically, preserving ids. A missing `memory.db` is restored from the newest snapshot on startup; an existing corrupt database is not silently replaced. Keep the whole data directory backed up, including Markdown; memory CSVs do not contain diary bodies.

Snapshot I/O failures are surfaced after the committed memory change; the SQL commit cannot be undone by a filesystem failure. Normalize retries a snapshot if the latest change has not been snapshotted. Retention defaults to seven days and always keeps the newest snapshot. Restore can preserve entries exceeding newly lowered configuration caps; normalize then handles entry/topic count violations.

## Configuration

Precedence: process environment > `.env` > YAML > defaults. Missing YAML is allowed when all required secrets/settings are supplied elsewhere. Configuration is read once at startup. Invalid fields are reported together. `NAMIDIARY_CONFIG` selects YAML (default `./config.yml`); `NAMIDIARY_ENV_FILE` selects dotenv (default `./.env`). Dotenv supports comments, `KEY=VALUE`, and quoted values; it never overwrites an existing environment variable.

Every scalar has a `NAMIDIARY_` environment name using double underscores between path segments. Lists use comma-separated values. Durations use Go notation, such as `90m` or `24h`. Optional display names appear only in generated prompts; leave them empty to use neutral roles.

| YAML key | Default | Environment override |
|---|---|---|
| `app.port` | `8188` | `NAMIDIARY_APP__PORT` |
| `app.api_key` | `required secret` | `NAMIDIARY_APP__API_KEY` |
| `app.debug` | `false` | `NAMIDIARY_APP__DEBUG` |
| `app.data_dir` | `./data` | `NAMIDIARY_APP__DATA_DIR` |
| `app.timezone` | `UTC` | `NAMIDIARY_APP__TIMEZONE` |
| `mcp.allowed_hosts` | `[localhost 127.0.0.1 [::1]]` | `NAMIDIARY_MCP__ALLOWED_HOSTS` |
| `mcp.allowed_origins` | `[localhost 127.0.0.1 [::1]]` | `NAMIDIARY_MCP__ALLOWED_ORIGINS` |
| `diary.search_limit` | `20` | `NAMIDIARY_DIARY__SEARCH_LIMIT` |
| `diary.latest_max` | `31` | `NAMIDIARY_DIARY__LATEST_MAX` |
| `diary.similar_limit` | `15` | `NAMIDIARY_DIARY__SIMILAR_LIMIT` |
| `diary.similar_query_max_chars` | `4000` | `NAMIDIARY_DIARY__SIMILAR_QUERY_MAX_CHARS` |
| `diary.patch_window` | `24h0m0s` | `NAMIDIARY_DIARY__PATCH_WINDOW` |
| `diary.future_window` | `24h0m0s` | `NAMIDIARY_DIARY__FUTURE_WINDOW` |
| `diary.active_normalize` | `true` | `NAMIDIARY_DIARY__ACTIVE_NORMALIZE` |
| `diary.embedding.base_url` | `https://openrouter.ai/api/v1` | `NAMIDIARY_DIARY__EMBEDDING__BASE_URL` |
| `diary.embedding.model` | `qwen/qwen3-embedding-8b` | `NAMIDIARY_DIARY__EMBEDDING__MODEL` |
| `diary.embedding.api_key` | `required secret` | `NAMIDIARY_DIARY__EMBEDDING__API_KEY` |
| `diary.embedding.dimensions` | `1024` | `NAMIDIARY_DIARY__EMBEDDING__DIMENSIONS` |
| `diary.embedding.batch_size` | `64` | `NAMIDIARY_DIARY__EMBEDDING__BATCH_SIZE` |
| `diary.embedding.concurrency` | `4` | `NAMIDIARY_DIARY__EMBEDDING__CONCURRENCY` |
| `diary.embedding.timeout` | `30s` | `NAMIDIARY_DIARY__EMBEDDING__TIMEOUT` |
| `context.static.default_recent` | `7` | `NAMIDIARY_CONTEXT__STATIC__DEFAULT_RECENT` |
| `context.static.max_recent` | `60` | `NAMIDIARY_CONTEXT__STATIC__MAX_RECENT` |
| `context.dynamic.max_limit` | `60` | `NAMIDIARY_CONTEXT__DYNAMIC__MAX_LIMIT` |
| `memory.entry_max_chars` | `200` | `NAMIDIARY_MEMORY__ENTRY_MAX_CHARS` |
| `memory.language` | `empty` | `NAMIDIARY_MEMORY__LANGUAGE` |
| `memory.names.agent` | `empty` | `NAMIDIARY_MEMORY__NAMES__AGENT` |
| `memory.names.user` | `empty` | `NAMIDIARY_MEMORY__NAMES__USER` |
| `memory.limits.profile.max_topics` | `0` | `NAMIDIARY_MEMORY__LIMITS__PROFILE__MAX_TOPICS` |
| `memory.limits.profile.max_entries` | `30` | `NAMIDIARY_MEMORY__LIMITS__PROFILE__MAX_ENTRIES` |
| `memory.limits.preference.max_topics` | `0` | `NAMIDIARY_MEMORY__LIMITS__PREFERENCE__MAX_TOPICS` |
| `memory.limits.preference.max_entries` | `30` | `NAMIDIARY_MEMORY__LIMITS__PREFERENCE__MAX_ENTRIES` |
| `memory.limits.self.max_topics` | `0` | `NAMIDIARY_MEMORY__LIMITS__SELF__MAX_TOPICS` |
| `memory.limits.self.max_entries` | `20` | `NAMIDIARY_MEMORY__LIMITS__SELF__MAX_ENTRIES` |
| `memory.limits.people.max_topics` | `20` | `NAMIDIARY_MEMORY__LIMITS__PEOPLE__MAX_TOPICS` |
| `memory.limits.people.max_entries` | `20` | `NAMIDIARY_MEMORY__LIMITS__PEOPLE__MAX_ENTRIES` |
| `memory.limits.area.max_topics` | `15` | `NAMIDIARY_MEMORY__LIMITS__AREA__MAX_TOPICS` |
| `memory.limits.area.max_entries` | `20` | `NAMIDIARY_MEMORY__LIMITS__AREA__MAX_ENTRIES` |
| `memory.limits.project.max_topics` | `7` | `NAMIDIARY_MEMORY__LIMITS__PROJECT__MAX_TOPICS` |
| `memory.limits.project.max_entries` | `15` | `NAMIDIARY_MEMORY__LIMITS__PROJECT__MAX_ENTRIES` |
| `memory.dream.context_diaries` | `3` | `NAMIDIARY_MEMORY__DREAM__CONTEXT_DIARIES` |
| `memory.maintenance.interval` | `0s` | `NAMIDIARY_MEMORY__MAINTENANCE__INTERVAL` |
| `memory.backup.retention.policy` | `max_age` | `NAMIDIARY_MEMORY__BACKUP__RETENTION__POLICY` |
| `memory.backup.retention.max_age` | `168h0m0s` | `NAMIDIARY_MEMORY__BACKUP__RETENTION__MAX_AGE` |
| `memory.llm.base_url` | `https://openrouter.ai/api/v1` | `NAMIDIARY_MEMORY__LLM__BASE_URL` |
| `memory.llm.model` | `google/gemini-2.5-flash` | `NAMIDIARY_MEMORY__LLM__MODEL` |
| `memory.llm.api_key` | `required secret` | `NAMIDIARY_MEMORY__LLM__API_KEY` |
| `memory.llm.temperature` | `0.2` | `NAMIDIARY_MEMORY__LLM__TEMPERATURE` |
| `memory.llm.max_tokens` | `8192` | `NAMIDIARY_MEMORY__LLM__MAX_TOKENS` |
| `memory.llm.timeout` | `3m0s` | `NAMIDIARY_MEMORY__LLM__TIMEOUT` |
| `memory.llm.json_mode` | `true` | `NAMIDIARY_MEMORY__LLM__JSON_MODE` |
| `memory.llm.max_attempts` | `3` | `NAMIDIARY_MEMORY__LLM__MAX_ATTEMPTS` |
| `memory.llm.agents.dream.model` | `empty` | `NAMIDIARY_MEMORY__LLM__AGENTS__DREAM__MODEL` |
| `memory.llm.agents.dream.temperature` | `inherit` | `NAMIDIARY_MEMORY__LLM__AGENTS__DREAM__TEMPERATURE` |
| `memory.llm.agents.archive.model` | `empty` | `NAMIDIARY_MEMORY__LLM__AGENTS__ARCHIVE__MODEL` |
| `memory.llm.agents.archive.temperature` | `inherit` | `NAMIDIARY_MEMORY__LLM__AGENTS__ARCHIVE__TEMPERATURE` |
| `memory.llm.agents.compact.model` | `empty` | `NAMIDIARY_MEMORY__LLM__AGENTS__COMPACT__MODEL` |
| `memory.llm.agents.compact.temperature` | `inherit` | `NAMIDIARY_MEMORY__LLM__AGENTS__COMPACT__TEMPERATURE` |
| `memory.prompts_dir` | `empty` | `NAMIDIARY_MEMORY__PROMPTS_DIR` |

`memory.entry_max_chars` is 20..2000, counted as Unicode code points. All counts are positive except static default recent (may be zero); maintenance interval may be zero to disable the timer. Per-agent temperatures inherit when null. URLs must be absolute HTTP(S) URLs. Custom prompt files in `memory.prompts_dir` override embedded `dream.md`, `archive.md`, `compact.md`, and `shared_rules.md` individually. Templates receive `AgentName`, `UserName`, `Language`, `EntryMaxChars`, `Limits`, and `Today`.

## REST API

All routes except `/healthz` require `Authorization: Bearer TOKEN`. Bodies are JSON with a 1 MiB limit. Unknown object fields are rejected. Read responses use `{"content": ...}`; writes use `{"status":"success","message":"Completed."}`, with `id` for a new memory entry. Background jobs return HTTP 202 with `{"status":"accepted","job":"..."}`. See [openapi.yaml](openapi.yaml) for schemas, parameters, response codes, and all registered REST routes.

| Method | Path | Input / behavior |
|---|---|---|
| GET | `/healthz` | Unauthenticated database ping |
| GET | `/diary/search` | Required `from`, `to`; optional `limit`, repeated `query`; every term matches body or keywords |
| GET | `/diary/latest` | `recent=1`, up to configured maximum; oldest first |
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
| POST | `/memory/init` | `recent=0..365`; safety snapshot, reset, sequential historical replay |
| POST | `/memory/restore` | `{"snapshot":"TIMESTAMP"}` or `{}` for newest; background job |
| GET | `/memory/status` | Busy state, pending dream, last 20 jobs, violations, archives, latest snapshot |
| GET | `/memory/snapshots` | Names, newest first; REST only |
| GET | `/context/static` | `recent=0..max_recent`; summaries, core facts, directory |
| POST | `/context/dynamic` | Same body as similar; `limit=0..max_limit` (default `diary.similar_limit`) results; related diary section |

Similar uses nonempty `recent` in preference to `user_message`, formats each turn as `role: text`, and keeps the last configured number of Unicode code points. Role is any nonblank string. Static and dynamic context do not deduplicate against each other.

Errors use `{"status":"error","code":"...","message":"...","details":{...}}`. Details are optional. Busy errors (`MEMORY_BUSY`, `DIARY_BUSY`) return HTTP 503 and `Retry-After: 10`. Other codes distinguish validation (400), authentication (401), origin rejection (403), missing resources (404), conflicts/caps (409), provider failures (502), and internal/parse failures (500). Debug mode adds diagnostic information to server errors. Job failures appear in `/memory/status`, not in the already-returned 202 response.

## Memory model and concurrency

- **core:** fixed `profile` (who the user is), `preference` (the user's tastes, habits, how they want to be treated, and agent–user agreements) and `self` (the agent's own identity, self-understanding and principles); never deleted.
- **people:** one topic per other person.
- **area:** ongoing domain with no endpoint.
- **project:** a finite goal; completion marks it for distillation into area topics.

Topic names and aliases are trimmed, limited to 64 characters, and globally unique ignoring case. Topics allow up to ten aliases. A fact's sources are sorted unique diary dates. New-topic caps are hard errors; per-topic entry caps are soft and enqueue maintenance. REST writes use `updated_by=user`, MCP uses `agent`, and every internal LLM uses `dream`.

One goroutine runs all memory jobs. External memory mutations and job requests fail fast when a job owns the write lock. Reads remain available through WAL snapshots. Dreams coalesce into at most one pending request. Every LLM action batch is atomic, including archive/compact restrictions and their post-checks. Invalid batches are retried with feedback; archive failures do not prevent subsequent maintenance steps. Init stops at the first failed replay pass and preserves earlier commits. Shutdown cancels background provider requests and uncommitted transactions.

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

## Containers

```sh
docker build --build-arg VERSION=local -t namidiary:local .
cp -r docker_compose/copy-this-and-rename ./deployment
cp docker_compose/compose.example.yml deployment/compose.yml
cp deployment/config.example.yml deployment/config.yml
cp deployment/.env.example deployment/.env
# Fill deployment/.env; configure deployment/config.yml.
# Make deployment/data writable by container UID 1000, GID 100.
docker compose -f deployment/compose.yml up -d
```

The image runs as `1000:100` on Debian with CA certificates and timezone data. Compose mounts data read-write and configuration read-only, and injects secrets via `env_file`. The default host port is loopback-only. The binary's `healthcheck` subcommand avoids installing a separate HTTP client. If changing the internal port, also set `NAMIDIARY_APP__PORT` so the health probe uses the same port.

## Migration

1. Copy the old `data/diary/` tree into the new `data/` directory.
2. Configure providers and start the service. A missing `diary.db`, an old/missing embedding status, or `diary.active_normalize` triggers an index rebuild from Markdown at startup (startup fails if the embedding provider is unreachable); `POST /diary/normalize` or `POST /diary/init` remain available explicitly.
3. Optionally seed memory with `POST /memory/init?recent=N`, then poll status.
4. `notesValues.json` is unused. There is no notes migration.
5. Replace `/prompt` callers with session-start `/context/static` and per-message `/context/dynamic`.
6. Update dated patch calls to `/diary/patch`; update verify calls/tools to normalize. Get/latest now include front matter. Destructive index rebuild is REST-only.

Compatibility fixtures cover the documented legacy Markdown variants. The predecessor source was not accessible during this implementation, so undocumented behavior has not been independently compared.

## Development and verification

```sh
export CGO_ENABLED=1
export GOFLAGS=-tags=sqlite_fts5
go test ./...
go test -race ./...
go vet ./...
gofmt -l cmd internal
# Or: make check
```

Tests use temporary databases and local fake HTTP providers. The integration test adds three diaries through REST, waits for background work, verifies memory/context, exercises literal-route precedence, and inspects the official SDK's actual MCP tool registry. Tests also cover configuration, legacy Markdown, time windows, index repair, vector ordering, atomic memory batches, alias and provenance rules, CSV validation, agent retries and worker coalescing. An OpenAPI test checks every registered REST route. C toolchain and header locations may need explicit `CC`/`CGO_CFLAGS` on systems without conventional development paths.

On NixOS, `nix-shell` supplies Go, a C compiler, SQLite headers and make using the included `shell.nix`.

The implementation choices and deviations from illustrative PRD defaults are in [decisions.md](decisions.md).

## Security and privacy

This is a single-user service for a trusted network. Put TLS and rate limiting in a reverse proxy before exposing it. Authentication uses a constant-time token comparison. MCP validates hosts and origins. The service never logs Authorization headers or HTTP request bodies. Debug mode deliberately logs full LLM prompts/replies, which include private diaries and memory; keep debug disabled for routine use. Provider error bodies are restricted to diagnostic logs, not client responses.

Local `config.yml`, `.env*`, and data are ignored by Git; example files contain placeholders only. Runtime display names are configuration-only. The supplied PRD is retained as the original specification, while implementation docs, prompts and fixtures use neutral roles.
