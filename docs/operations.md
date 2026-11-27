# Operations

## Diary write semantics

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

Markdown front matter contains `date`, `created`, `updated`, `keywords`, `summary`, and optional `location`. Legacy scalar keywords, YAML dates, and absent metadata are accepted. Get and latest with `recent=1` return full Markdown; latest with `recent>1`, search and similar return `DATE-[keywords]summary` strings.

Markdown is the source of truth, but only add/patch touch it; every read (get, latest, search, similar) is served from `diary.db`. If embedding fails during a write, the entry is still stored and readable but has no vector until `POST /diary/normalize` backfills it. Normalize reuses unchanged summary vectors. `POST /diary/init` forces fresh embeddings and never touches memory. With `diary.active_normalize` (default `true`) the index is also re-synced from Markdown at startup and after every add/patch, reusing unchanged vectors, so external edits appear after the next restart or write; set it to `false` to have writes upsert only their own row. Either way, `POST /diary/normalize` makes external edits visible immediately.

Memory lives in a separate database. Every worker-controlled committed change writes a CSV snapshot. Memory restore validates both CSV files, takes a safety snapshot, then replaces the library atomically, preserving ids. A missing `memory.db` is restored from the newest snapshot on startup; an existing corrupt database is not silently replaced. Keep the whole data directory backed up, including Markdown; memory CSVs do not contain diary bodies.

Snapshot I/O failures are surfaced after the committed memory change; the SQL commit cannot be undone by a filesystem failure. Normalize retries a snapshot if the latest change has not been snapshotted. Retention defaults to seven days and always keeps the newest snapshot. Restore can preserve entries exceeding newly lowered configuration caps; normalize then handles entry/topic count violations.

## Memory model and concurrency

- **core:** fixed `profile` (who the user is), `preference` (the user's tastes, habits, how they want to be treated, and agent–user agreements) and `self` (the agent's own identity, self-understanding and principles); never deleted.
- **people:** one topic per other person.
- **area:** ongoing domain with no endpoint.
- **project:** a finite goal; completion marks it for distillation into area topics.

Topic names and aliases are trimmed, limited to 64 characters, and globally unique ignoring case. Topics allow up to ten aliases. A fact's sources are sorted unique diary dates. New-topic caps are hard errors; per-topic entry caps are soft and enqueue maintenance. REST writes use `updated_by=user`, MCP uses `agent`, and every internal LLM uses `dream`.

One goroutine runs all memory jobs. External memory mutations and job requests fail fast when a job owns the write lock. Reads remain available through WAL snapshots. Dreams coalesce into at most one pending request. Every LLM action batch is atomic, including archive/compact restrictions and their post-checks. Invalid batches are retried with feedback; archive failures do not prevent subsequent maintenance steps. Init stops at the first failed replay pass and preserves earlier commits. Shutdown cancels background provider requests and uncommitted transactions.

## Containers

```sh
cp -r docker_compose/copy-this-and-rename ./deployment
cp docker_compose/compose.example.yml deployment/compose.yml
cp deployment/config.example.yml deployment/config.yml
cp deployment/.env.example deployment/.env
# Fill deployment/.env; configure deployment/config.yml.
# Make deployment/data writable by container UID 1000, GID 100.
docker compose -f deployment/compose.yml up -d --build
```

The image runs as `1000:100` on Debian with CA certificates and timezone data. Compose mounts data read-write and configuration read-only, and injects secrets via `env_file`. The default host port is loopback-only. The binary's `healthcheck` subcommand avoids installing a separate HTTP client. If changing the internal port, also set `NAMIDIARY_APP__PORT` so the health probe uses the same port.

## Importing existing diaries

1. Copy an existing `diary/YYYY/MM/MM-DD.md` tree into the new data directory. Front matter with legacy scalar keywords, YAML dates or no metadata at all is accepted.
2. Configure the providers and start the service. A missing `diary.db`, a missing or outdated embedding status, or `diary.active_normalize` triggers an index rebuild from Markdown at startup. Startup fails if the embedding provider is unreachable.
3. Optionally seed memory with `POST /memory/init?recent=N` (replays the last N diaries one dream at a time), then poll `/memory/status`.

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

## Security and privacy

This is a single-user service for a trusted network. Put TLS and rate limiting in a reverse proxy before exposing it. Authentication uses a constant-time token comparison. MCP validates hosts and origins. The service never logs Authorization headers or HTTP request bodies. Debug mode deliberately logs full LLM prompts/replies, which include private diaries and memory; keep debug disabled for routine use. Provider error bodies are restricted to diagnostic logs, not client responses.

Local `config.yml`, `.env*`, and data are ignored by Git; example files contain placeholders only. Runtime display names are configuration-only. Implementation docs, prompts and fixtures use neutral roles.
