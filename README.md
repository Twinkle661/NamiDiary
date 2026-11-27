```text
      .  *        .        ☾        .     *
   *     _   _                 _ ____  _                     .
        | \ | | __ _ _ __ ___ (_)  _ \(_) __ _ _ __ _   _
   .    |  \| |/ _` | '_ ` _ \| | | | | |/ _` | '__| | | |   *
        | |\  | (_| | | | | | | | |_| | | (_| | |  | |_| |
    *   |_| \_|\__,_|_| |_| |_|_|____/|_|\__,_|_|   \__, |  .
                            z z z                   |___/
```

<p align="center"><b>A diary-first memory system for long-lived companion agents.</b><br>
<i>The agent writes a diary. While it "sleeps", a dream turns the days into things it simply knows.</i> (˘ω˘)ᶻᶻ</p>

---

NamiDiary is a small single-user Go service. Your agent keeps a daily diary in its own words. After each new entry, a background **dream** reads it and distils what lasts into a small, **bounded** library of facts. Diaries and memory then come back out as context blocks, ready to be injected into a prompt. It offers REST and MCP over the same core.

## ✦ The idea

Most agent memory works turn by turn: it pulls "facts" out of every message and adds them to a store that only grows. NamiDiary starts from how people remember instead: we live through a day, we reflect on it, and while we sleep the parts that matter turn into what we simply *know*.

✎ **The diary is the source of truth.** Only the agent writes it, in the first person, as plain Markdown you can read and edit. Memory never writes to the diary. It is always *derived* from it, so the whole memory library can be thrown away and rebuilt by replaying the diaries.

⇅ **Events stay in the diary; facts go to memory.** "Today we talked about her supervisor" belongs in the diary. "Her supervisor is Dr. K" belongs in memory. Keeping the two layers apart keeps memory short, standalone and free of timestamps that go stale.

☾ **Dreaming, not per-turn extraction.** Memory updates once, after a new diary entry is written, not after every message. The dream agent sees the newest entry (with a few earlier ones for context) and the current library, and returns one atomic batch of edits.

❦ **Memory is bounded, so it has to forget.** Each fact is a single short entry (200 characters by default), and every kind of topic has caps. When a topic overflows, a compaction pass merges and drops entries until it is comfortably under its limit. When a project ends, it is distilled into one or two lasting facts about the relevant area and then removed.

◎ **It is the agent's memory, not a user profile.** Memory is built from what the agent wrote, from the agent's point of view, including an evolving `self` topic for its own identity and principles. We accept the occasional misreading that comes with that as the price of authenticity.

※ **Provenance and authority.** Every fact cites the diary dates it came from and records who last wrote it: `user`, `agent`, or `dream`. Automatic passes revise their own entries freely. They handle what the user or the agent wrote on purpose more carefully: they merge it, rewrite it or retire it only when a diary contradicts or supersedes it, or when it duplicates another entry, never just to save space.

## ✦ How it works

```text
   ✎ agent writes ──▶  diary/2026/11/11-26.md      plain Markdown, the source of truth
                            │
                            ├──▶  diary.db         keyword (FTS5) + vector index
                            │
                            └──▶  ☾ dream  ──▶  memory.db   topics · facts · sources
                                                   │   ├─ compact   over limits → merge & forget
                                                   │   └─ archive   finished project → area
                                                   ▼
               ┌──────────────── ready-to-inject context ────────────────┐
               │  /context/static    once per session                    │
               │  /context/dynamic   every turn                          │
               └─────────────────────────────────────────────────────────┘
                                                   ▼
                                     your agent's prompt  (◕‿◕)
```

- **Writing.** The agent writes one diary per day with `diary_add` (body plus keywords, a one-line summary and an optional location). It can append afterthoughts to the newest entry with `diary_patch`. Patches update the index but do not trigger a dream.
- **Dreaming.** Dreams run on a single background worker. Requests that arrive while one is running are merged into one pending dream. If the dream model returns an invalid batch, it is told why and asked again. Nothing is applied until a batch passes validation, and every committed change writes a CSV snapshot.
- **Remembering.** Context comes back out in two ready-made blocks (see below). For anything deeper, the agent uses MCP tools to read full diaries, open memory topics, or record and correct facts deliberately.

## ✦ Ready-to-inject context

NamiDiary does the assembly so your harness doesn't have to. Both endpoints return `{"content": "..."}`, where `content` is plain tagged text you can paste straight into a prompt.

| Endpoint | When | What you get |
|---|---|---|
| `GET /context/static?recent=7` | once, at session start | recent diary summaries, the full core memory, and an index of every other topic |
| `POST /context/dynamic` | on every user message | past diaries most similar to the conversation |

**Static** goes at the end of the system prompt:

```text
<recent_diaries>
2026-11-24-[library,thesis]Spent the afternoon in the library; the second chapter finally has a shape.
2026-11-25-[rain,piano]Rainy day at home. She played the piece she has been practising for weeks.
2026-11-26-[call,supervisor]Supervision meeting went well; the deadline moved to January.
</recent_diaries>

<core_memory>
<profile>
- The user is a PhD student in philosophy.
</profile>
<preference>
- The user prefers explanations of underlying principles over analogies.
- The agent and the user agreed that the agent writes a diary every night.
</preference>
<self>
- The agent values honesty over reassurance.
</self>
</core_memory>

<memory_index>
{"core":{"preference":[],"profile":[],"self":[]},"people":{"Dr. K":["supervisor"]},"area":{"Philosophy research":[],"Piano":[]},"project":{"Thesis chapter 2":["ch2"]}}
</memory_index>
```

The core topics are always present in full. Other topics appear only by name, and the agent opens them with `memory_get_entries` when a conversation calls for them.

**Dynamic** goes after the latest message as an ephemeral tail, so it is never stored in history. Send `{"user_message": "...", "recent": [{"role": "user", "text": "..."}]}` and you get back:

```text
<related_diaries>
2026-10-02-[supervisor,deadline]First meeting about chapter two; agreed on a December draft.
2026-11-26-[call,supervisor]Supervision meeting went well; the deadline moved to January.
</related_diaries>
```

### Plug it in with a prompt assembler

Any harness that can call a URL before a request can use these blocks. For [pi](https://github.com/earendil-works/pi), **[in-context](https://github.com/Twinkle661/in-context)** fits them exactly. Its *system* slot injects once per session, and its *tail* slot is fetched on every request and never saved. Its default POST body is already the shape `/context/dynamic` expects:

```jsonc
// ~/.pi/agent/in-context.json
{
  "system": { "sources": [{
    "url": "http://localhost:8188/context/static",
    "headers": { "Authorization": "Bearer ${env:NAMIDIARY_TOKEN}" }
  }]},
  "tail": { "sources": [{
    "url": "http://localhost:8188/context/dynamic",
    "method": "POST",
    "headers": { "Authorization": "Bearer ${env:NAMIDIARY_TOKEN}" }
  }]}
}
```

Then connect the MCP server at `http://localhost:8188/mcp` (same bearer token) and have the agent write its diary every day. That diary is the only input the memory system needs. ✧*｡

## ✦ Memory model

| Kind | Topics | What goes there |
|---|---|---|
| `core` | exactly `profile`, `preference`, `self` | Who the user is · the user's tastes, habits and agent–user agreements · the agent's own identity and principles. Always injected, never deleted. |
| `people` | one per person | Facts about someone other than the agent and the user. Other names go in aliases. |
| `area` | open-ended | Ongoing domains of life with no end point, such as a career, a field or a hobby. |
| `project` | finite | Something with a goal and an end. When it finishes, it is archived and distilled into areas. |

Every entry is one standalone fact with absolute dates, sources (diary dates) and an `updated_by`. The default caps are listed in [docs/configuration.md](docs/configuration.md). The dream, archive and compact prompts are embedded in the binary and can be overridden one file at a time.

## ✦ Quick start

Requirements: Go 1.25+, a C compiler and SQLite headers (CGO with FTS5), plus API keys for an OpenAI-compatible embedding endpoint and chat-completion endpoint. On Debian, install `build-essential libsqlite3-dev`; on NixOS, `nix-shell` provides everything.

```sh
cp config.example.yml config.yml
cp .env.example .env        # fill in the API token and the two provider keys
make build
./bin/namidiary             # listens on :8188
```

```sh
export TOKEN='your-api-token'
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"content":"A quiet day of reading.","meta":{"keywords":["reading"],"summary":"A day spent reading."}}' \
  http://localhost:8188/diary/2026-01-01
curl -H "Authorization: Bearer $TOKEN" http://localhost:8188/memory/status   # watch it dream (˘ω˘)ᶻᶻ
curl -H "Authorization: Bearer $TOKEN" http://localhost:8188/context/static
```

To run it in Docker, follow [docs/operations.md](docs/operations.md#containers).

## ✦ Design choices and non-goals

- **Single user, trusted network.** There is bearer-token auth and MCP host/origin checks, but no multi-tenancy. Put TLS and rate limiting in a reverse proxy.
- **No UI.** The diary is Markdown on disk, and the memory library is reachable over REST and MCP.
- **No per-turn memory writes from the system itself.** Only the dream and the agent's own explicit tool calls change memory.
- **Private by default.** Request bodies and auth headers are never logged. Debug mode does log full LLM prompts, which contain diaries, so keep it off in normal use.

## ✦ Documentation

- [docs/configuration.md](docs/configuration.md): every setting, its default and its environment override
- [docs/api.md](docs/api.md): REST routes, MCP tools and error format ([openapi.yaml](openapi.yaml) is authoritative)
- [docs/operations.md](docs/operations.md): storage layout, backup and restore, concurrency, containers, importing existing diaries, development

## ✦ License

[MIT](LICENSE) ♡
