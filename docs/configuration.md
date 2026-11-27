# Configuration

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

See [config.example.yml](../config.example.yml) for an annotated example.
