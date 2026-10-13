# Topic onboarding: let users pick what they practise

Date: 2026-10-13. Status: approved design, awaiting spec review. Target version: 0.4.0.

## Problem

`git prompted init` copies `assets/goals.example.json` (Concurrency + Architecture) into
`~/.git-prompted/goals.json` without asking anything. Users never choose their topics, and changing
them means hand-writing JSON with RE2 regexes. A fresh plugin user who never runs `init` gets nothing:
`LoadConfig` returns nil with no topics and every hook is a silent no-op.

## Goals

- A new user picks their own topics inside the agent they already use, without opening `goals.json`.
- Topics not in a built-in catalog can be drafted by the agent, confirmed by the user, and validated
  by the binary before they are saved.
- Onboarding runs automatically once, on the first fresh session with no user config, and again on
  demand via `/git-prompted:learn init`.

## Constraints (unchanged non-negotiables)

- No model calls or network in the binary; the agent runs the conversation.
- Standard library only.
- `git-prompted hook ...` always exits 0 with a valid response.
- Opt-in and escapable: onboarding offers "not now", and declining never re-prompts automatically.

## Non-goals

- A terminal wizard.
- Editing individual fields of a topic (re-adding the topic by id replaces it).
- Choosing mode or level during onboarding (catalog defaults apply; custom topics may set them via JSON).

## Design

### 1. Topic catalog

- New embedded asset `assets/topics.json`: a JSON array of `core.Topic` objects (same shape as entries
  in `goals.json`: `id`, `name`, `mode`, `level`, `goals`, `patterns`, `paths`, `concepts`).
- Initial entries: `concurrency`, `architecture` (both copied verbatim from today's example),
  `error-handling`, `sql`, `testing`, `security`, `performance`, `api-design`. Patterns cover Go,
  Python and TypeScript, and target code that exercises the skill rather than code present in most edits
  (e.g. error wrapping with `%w`, not every `if err != nil`).
- `assets/goals.example.json` is deleted. `assets.GoalsExample` is replaced by `assets.Topics []byte`.
- `core.Catalog() ([]*Topic, error)` parses the embed. Hooks never call it.

### 2. Validation

`core.ValidateTopic(t *Topic) error` is the only gate for writes:

- `id` is non-empty and equals `NormalizeTag(id)` (kebab-case).
- `mode` is empty, `ask-first`, `think-first` or `quiz-after`.
- At least one pattern or one path.
- Every pattern compiles as `regexp.Compile("(?m)" + p)`, matching `LoadConfig`.
- Concepts are normalised through `NormalizeTag`.

Errors name the topic id, the field and the reason, e.g.
`topic react-hooks: pattern 2 "use(State" does not compile: missing closing )`.

### 3. `topics` CLI

New file `cmd/git-prompted/topics.go`; file logic in `internal/core/topics.go`.

```
git-prompted topics list [--json]   # catalog entries, each marked with whether it is in goals.json,
                                    # plus custom topics already in goals.json
git-prompted topics add <id>...     # copy catalog entries into goals.json
git-prompted topics add --json -    # one topic object or an array, read from stdin
git-prompted topics remove <id>...  # remove from goals.json
```

- `--json` output for `list`: `[{"id","name","mode","level","goals","source":"catalog|custom","selected":bool}]`.
- `add` with an unknown catalog id fails and lists valid ids.
- `add` with an id already in `goals.json` replaces that topic in place (keeps its position).
- `remove` with an id not present is reported but is not an error.
- Every topic in a command is validated first; if any fails, nothing is written (all or nothing).
- Writes: hold the existing state file lock, copy the current file to `goals.json.bak`, write to a temp
  file and rename. Top-level keys other than `topics` (`start_prompt`, `cooldown_minutes`,
  `max_checkpoints_per_session`) are preserved. If `goals.json` does not exist, `add` creates it with
  only `topics`.
- `remove` touches config only. `log.jsonl`, scores and weak spots are kept, so re-adding restores history.

### 4. `init`

| Invocation | Config missing | Config exists |
|---|---|---|
| `init` | Print catalog names and say: run `/git-prompted:learn init` in your agent, or `git prompted topics add <id>...` | "Config already exists: <path>" (unchanged) |
| `init --defaults` | Write `concurrency` + `architecture` from the catalog | unchanged message, no write |
| `init --empty` | Write `{"topics": []}` | unchanged message, no write |

### 5. Onboarding trigger (hooks)

In `runHook`, for `session-start` only, before the `cfg == nil` early return:

- Condition: `ev.Source` is `""`, `startup` or `clear`, **and** `core.UserConfigPath()` does not exist.
- Action: `eng.Onboarding(ev)` returns the onboarding text; respond with `ad.Context(text)`.
- If a project `.git-prompted.json` has topics, `cfg` is non-nil: the normal SessionStart text is
  returned with the onboarding text appended, so project topics keep working.
- Declining runs `init --empty`; the file then exists and the trigger never fires again.
- The branch only does a stat and string building, inside the existing recover/exit-0 guard.

Onboarding text (in `engine.go`, agent-aware via `askHint()`; Codex gets the coaching guide inlined as
today):

> [git-prompted] Installed, but no learning topics are set up yet. Before handling the user's first
> request, run onboarding: follow the "Onboarding" section of the git-prompted coaching guide.
> Catalog: `<cli> topics list --json`. Save picks: `<cli> topics add <id>...`; custom topics:
> `<cli> topics add --json -`. If they say not now: `<cli> init --empty`.

After topics are saved in that session, checkpoints are on for it (the session was created with mode
`on`, and the On/Off question is not asked again in that session).

### 6. Conversation (coaching guide)

New "Onboarding" section in `assets/coaching.md` (synced to the Claude skill and `GEMINI.md`):

1. Ask once which topics the user wants to practise instead of having them written for them:
   multi-select (`AskUserQuestion` on Claude, numbered list elsewhere), catalog names plus
   "Something else" and "Not now". On re-runs, mark current topics.
2. "Something else": draft one topic JSON (name, 2-3 goals, 4-8 patterns aimed at code that shows
   the skill, concepts), show a short summary, save on confirmation via `topics add --json -`.
   If the binary rejects it, fix the reported field and retry.
3. Re-runs apply a diff: `topics add` for new picks, `topics remove` for dropped ones.
4. Confirm in one line ("Practising: X, Y. Change any time with /git-prompted:learn init.") and
   continue with the user's request. No lecture.

`integrations/claude/commands/learn.md`: `init` maps to "run onboarding per the coaching guide"
instead of passing through to the CLI.

### 7. Ignore prose files

Found while writing this spec: a checkpoint fired on this markdown file because it quotes example
patterns. With more topics in the catalog, prose edits (design docs, READMEs) will trigger often.

- In `Topic.Matches`, a topic with no `paths` ignores an edit whose files all have a prose extension:
  `.md`, `.mdx`, `.txt`, `.rst`, `.adoc`.
- Topics that set `paths` keep full control (a user can still target `docs/**` explicitly).
- Mixed edits (prose and code) still match on the code.

## Testing

All tests use a temp `GIT_PROMPTED_HOME`.

`internal/core/topics_test.go`:
- Every catalog entry passes `ValidateTopic`; ids are unique.
- Per catalog topic: fixture snippets that must match, and plain code (`x := 1`,
  `if err != nil { return err }`, `return nil`) that must not match any topic.
- Validation rejects: bad regex, non-kebab id, no patterns or paths, unknown mode.
- `add` replaces by id in place; preserves top-level keys; creates the file when missing; a failing
  topic in a batch writes nothing; `.bak` is written.
- `remove` keeps other topics and keys.

`internal/core/config_test.go`:
- A matching snippet in `notes.md` does not match a topic without `paths`; the same snippet in
  `w.go` does; a topic with `paths: ["docs/**"]` still matches `docs/a.md`.

`cmd/git-prompted/main_test.go`:
- No user config: session-start returns onboarding text for claude, codex and gemini.
- `{"topics": []}`: session-start is a no-op.
- `resume` / `compact` source with no config: no onboarding.
- Project config with topics and no user config: normal text plus onboarding.
- `init`, `init --defaults`, `init --empty` per the table; existing config never overwritten.
- `topics add --json -` with invalid JSON exits non-zero with a parse error.

## Docs

- README: replace "run init, then edit it" with the onboarding flow and the `topics` commands.
- `CLAUDE.md`: layout table (`assets/topics.json` instead of `goals.example.json`), commands.
- `make sync` after editing `assets/coaching.md` and `VERSION`.

## Rollout

Branch `feature/topic-onboarding` → PR → bump `VERSION` to 0.4.0 → `make sync` → tag and release.
Existing users with a `goals.json` see no change until they run `/git-prompted:learn init`.
