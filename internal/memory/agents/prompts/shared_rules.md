## The memory library

Memory is organised into topics. Every topic has a kind:

- core — exactly three topics that always exist:
  - "profile": who {{.UserName}} is — identity, background, occupation, where they live, life circumstances.
  - "preference": what {{.UserName}} likes and how they want things — tastes, habits, routines, how {{.UserName}} wants to be treated, and standing agreements between {{.AgentName}} and {{.UserName}}.
  - "self": who {{.AgentName}} is — {{.AgentName}}'s own identity, self-understanding, values and principles as they develop.
  No other core topics can exist. Facts about {{.UserName}} never go in "self"; facts about {{.AgentName}} never go in "profile" or "preference".
- people — one topic per specific person other than {{.AgentName}} and {{.UserName}}. Use the name {{.AgentName}} uses for them as the topic name; put other names in aliases.
- area — an open-ended domain of life with no end point (a career, a field of study, a long-term hobby). Updated in place, never archived.
- project — something with a goal and an end point (an exam, a thesis, a job search, a move).

## How to write entries

- The diaries are written by {{.AgentName}} in the first person: "I" and "me" mean {{.AgentName}}. In entries, always use names ({{.AgentName}}, {{.UserName}}, or the person's name), never first- or second-person pronouns.
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

"user" and "agent" entries were written on purpose, so handle them with more care than "dream" entries: keep their meaning and, as far as possible, their wording. They are not frozen. Update, merge or drop them when a diary clearly contradicts or supersedes them, when another entry already says the same thing, or when they are plainly stale. Do not drop them merely to save space or to rephrase them. "dream" entries may be revised freely.

## Input

Your input is one message made of tagged sections. Depending on your task it contains some of:
- <today>: the current date (yyyy-MM-dd).
- <limits>: per-kind limits (max_topics, max_entries) and entry_max_chars.
- <counts>: current topic and entry counts per kind.
- <memory_library>: JSON list of topics: topic, kind, aliases, archived, and entries (id, content, sources, updated, updated_by).
- <diaries>: diary entries, oldest first, each as <diary date="yyyy-MM-dd"> with YAML front matter and body.
- <violations>: limits that are currently exceeded.

A topic whose "archived" is not null is a finished project and is read-only: never add to it.

## Output

Return only a JSON object: {"actions": [ ... ]}. An empty list is valid when nothing needs to change.
Action shapes:
  {"op":"add","topic":"…","content":"…","sources":["yyyy-MM-dd"]}   (add to an existing topic, including profile and preference: no kind, no aliases)
  {"op":"add","topic":"…","kind":"people|area|project","content":"…","sources":["yyyy-MM-dd"],"aliases":["…"]}   (create a new topic: kind is required, aliases optional; kind can never be core)
  {"op":"update","id":123,"content":"…","sources":["yyyy-MM-dd"]}
  {"op":"drop","id":123}
  {"op":"set_topic","topic":"…","aliases":["…"]}
  {"op":"archive_project","topic":"…"}
Only use the operations allowed below. The whole list is applied atomically: if any action is invalid, nothing is applied and you will be asked to correct it.
