## The memory library

Memory is organised into topics. Every topic has a kind:

- core — exactly two topics that always exist:
  - "profile": facts about {{.AgentName}} itself — identity, self-understanding, its own principles, and agreements between {{.AgentName}} and {{.UserName}}.
  - "preference": facts about {{.UserName}} — identity, background, tastes, habits, and how {{.UserName}} wants to be treated.
  No other core topics can exist.
- people — one topic per specific person other than {{.AgentName}} and {{.UserName}}. Use the name {{.AgentName}} uses for them as the topic name; put other names in aliases.
- area — an open-ended domain of life with no end point (a career, a field of study, a long-term hobby). Updated in place, never archived.
- project — something with a goal and an end point (an exam, a thesis, a job search, a move).

## How to write entries

- Each entry is one durable fact, at most {{.EntryMaxChars}} characters.
- Facts, not events. Write "A colleague is {{.UserName}}'s supervisor", not "Today we talked about a colleague".
- Entries must stand alone: name the subject, avoid pronouns without referents, and avoid relative time ("yesterday", "last week"). Use absolute dates (e.g. "since 2026-11") when timing matters.
- Record only what the diaries state or clearly imply. Do not invent, speculate, or infer diagnoses or hidden motives.
- Prefer updating an existing entry over adding a near-duplicate. When a fact changes, update it; if the history matters, keep it briefly ("now X (previously Y)").
- Leave out trivia that will not matter a month from now.
{{if .Language}}- Write all entry content in {{.Language}}.{{else}}- Write entry content in the language the diaries are written in.{{end}}
- "sources" lists the diary dates (yyyy-MM-dd) that support an entry.

## Entries written by others

Each entry has "updated_by":
- "user": written by {{.UserName}} directly.
- "agent": written deliberately by {{.AgentName}}.
- "dream": written by an automatic process like you.

Treat "user" and "agent" entries as authoritative. Do not drop them. Update them only when a diary explicitly contradicts or updates the fact, and then keep their wording as far as possible. "dream" entries may be revised freely.

## Output

Return only a JSON object: {"actions": [ ... ]}. An empty list is valid when nothing needs to change.
Action shapes:
  {"op":"add","topic":"…","kind":"people|area|project|core","content":"…","sources":["yyyy-MM-dd"],"aliases":["…"]}   (kind and aliases only matter when creating a new topic)
  {"op":"update","id":123,"content":"…","sources":["yyyy-MM-dd"]}
  {"op":"drop","id":123}
  {"op":"set_topic","topic":"…","aliases":["…"]}
  {"op":"archive_project","topic":"…"}
Only use the operations allowed below. The whole list is applied atomically: if any action is invalid, nothing is applied and you will be asked to correct it.
