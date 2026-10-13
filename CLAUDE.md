# git prompted

Opt-in learning checkpoints for coding agents (Claude Code, Codex CLI, Gemini CLI). When an agent is about to write code in a topic the user wants to practise, a hook pauses it and hands back instructions to ask the user first or quiz them afterwards, and the results are tracked as weak spots and spaced reviews. Single Go binary `git-prompted`; on PATH, git exposes it as `git prompted`.

## Non-negotiables

- **Never break the user's agent session.** `git-prompted hook ...` must always exit 0 and print a valid (possibly no-op) response, whatever goes wrong: bad config, bad regex, malformed stdin, lock timeout, panic. `runHook` in `cmd/git-prompted/main.go` enforces this; keep it that way and test any new failure path.
- **No model calls and no network** in the binary. The binary decides *when* to interrupt; the agent the user is already running does the teaching. That is the "no extra AI bill" promise. Only the launcher (`bin/git-prompted`) touches the network, to download a checksum-verified release.
- **Standard library only.** No third-party Go modules. Keep the binary small and the supply chain empty.
- **Opt-in, always escapable.** Every checkpoint prompt must offer skip / not this session / mute. Don't add interruptions without a cooldown or cap.
- **Hooks are on the hot path** (every edit). Keep them fast: no expensive work beyond reading config, state and log.

## Layout

```
cmd/git-prompted/      main.go: hook dispatch + CLI commands + Codex installer
                       ui.go: stats output, status line, Claude status line installer
internal/core/         agent-agnostic engine
  config.go            goals.json + .git-prompted.json merge, topic matching, ** globs
  state.go             ~/.git-prompted/state.json under a file lock; log.jsonl
  engine.go            SessionStart / BeforeEdit / AfterTurn decisions + prompt text
  analytics.go         Summarize(): topic scores/trends, per-concept accuracy, Leitner reviews,
                       progress.md, StatusLine()
internal/adapter/      one Adapter per agent: parse its hook payload -> core.Event, format its response
assets/                embedded into the binary: goals.example.json, coaching.md
bin/git-prompted       POSIX launcher: local dist build > cached release > download+verify; keeps
                       ~/.git-prompted/bin/git-prompted symlinked to the current binary
hooks/hooks.json, gemini-extension.json, GEMINI.md   Gemini extension (Gemini requires repo root)
integrations/claude/   Claude Code plugin (marketplace at .claude-plugin/ points here)
integrations/codex/    Codex docs; Codex is installed via `git-prompted install codex`
```

Normalised hook kinds: `session-start`, `before-edit`, `after-turn`. Each agent's hook config maps its native events onto these (Claude/Codex: SessionStart/PreToolUse/Stop; Gemini: SessionStart/BeforeTool/AfterAgent).

## Generated files: edit the source, then `make sync`

| Source | Generated copies |
|---|---|
| `bin/git-prompted` | `integrations/claude/bin/git-prompted` (Claude plugins can only see their own dir) |
| `assets/coaching.md` | `integrations/claude/skills/coaching/SKILL.md`, `GEMINI.md` |
| `VERSION` | launcher `VERSION=`, `gemini-extension.json`, Claude `plugin.json` |

Never hand-edit the generated copies; CI runs `make check` and fails on drift.

Note: the root `GEMINI.md` is the extension's coaching context for *users*, not instructions for working on this repo.

## Commands

```sh
make build     # dist/git-prompted; both launchers prefer a local dist build automatically
make test      # go vet + go test ./... + launcher syntax check; run before every commit
make sync      # after editing bin/git-prompted, assets/coaching.md or VERSION
make check     # sync + fail on diff (CI)
make dist      # cross-compile 6 platforms + checksums.txt
gofmt -w .
```

Try hooks by hand without touching your real data:

```sh
export GIT_PROMPTED_HOME=$(mktemp -d)
./dist/git-prompted init
printf '%s' '{"session_id":"t","cwd":"/p","tool_name":"Write","tool_input":{"file_path":"/p/w.go","content":"go func(){}()"}}' \
  | ./dist/git-prompted hook claude before-edit
```

Use `printf '%s'`, not `echo`: `sh`'s echo turns `\n` inside the JSON into raw newlines and the payload silently becomes invalid.

## Conventions

- Tests live next to code. `cmd/git-prompted/main_test.go` has the end-to-end flows per agent; always set `GIT_PROMPTED_HOME` (and `CLAUDE_CONFIG_DIR` / `CODEX_HOME` for installers) to temp dirs. Never let tests touch the real home directory.
- Prompt text the agent sees lives in `internal/core/engine.go`. Keep it imperative, include the exact command to run, and keep `askHint()` agent-aware (only Claude has `AskUserQuestion`).
- Teaching behaviour (how to question, grade and tag) belongs in `assets/coaching.md`, not in Go strings.
- Concept tags are normalised kebab-case (`core.NormalizeTag`). Any new tag-producing path must go through `SplitTags` / `NormalizeTag`.
- Installers (`install codex|statusline`) must be idempotent, preserve other users' entries, back up the file first, and fully reverse on uninstall. Follow the existing pattern and add a test.
- State changes go through `core.WithState` (file lock + atomic write). Don't write `state.json` directly.
- Topic mode values: `ask-first` (default; legacy alias `think-first`) and `quiz-after`.

## Adding an agent

1. Add an `Adapter` in `internal/adapter/adapter.go`: `Parse` (filter to edit tools, extract files + added code), `Context`, `Deny`, `Continue`, `Noop`.
2. Register it in `adapter.Get`.
3. Add its hook config / installer, plus a case in `TestAdaptersParseEdits` and `TestFullFlowPerAgent`.

## Known gaps / verify before trusting

- Hook payloads were built from docs and **simulated in tests**. Claude Code is verified live (PreToolUse deny, Stop block, `${CLAUDE_PLUGIN_ROOT}` in hooks and slash-command markdown); Gemini and Codex are not yet. Most likely to drift: Gemini edit tool names (`write_file`, `replace`), Codex `apply_patch` payload fields and how Codex hooks are enabled. When you verify one, capture a real payload as a test fixture.
- Native Windows: the launcher is POSIX sh (works under Git Bash/WSL). A `.cmd` launcher is open work.
- Placeholder to replace before publishing: `YOUR_NAME` (manifests, LICENSE). GitHub Actions are unpinned; pin them to commit SHAs.

## Roadmap (next up)

- `git prompted dashboard`: local read-only web UI (React/Vite build embedded with `go:embed`, served on 127.0.0.1) over the data from `stats --json`.

## Release

Bump `VERSION` → `make sync` → commit → tag `vX.Y.Z` → push the tag. `.github/workflows/release.yml` checks the tag matches `VERSION`, tests, builds all platforms, and publishes binaries + `checksums.txt` with `gh release`. The launcher's pinned `VERSION` must match a published release, or first-run downloads fail (gracefully).
