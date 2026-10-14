# git prompted

**The AI prompts you back.**

Coding agents are great at taking work off your plate, including the work you'd actually like to keep getting better at. git prompted lets you name the topics you want to practise (concurrency, architecture, SQL, whatever). When the agent is about to write code in one of those areas, it pauses and asks:

> This touches **Concurrency**, one of your learning goals. Want to think it through first?
> 1. Yes, let me think · 2. Not this one · 3. Not this session · 4. Stop asking about this

Say yes and the agent frames the problem without giving away its answer, asks you one focused question, then compares your answer with its plan and grades it honestly. Say no and it gets on with the job.

Every answer is tagged with the concepts it tested, so git prompted knows your **weak spots**. Concepts you miss come back for **spaced review**, and your progress follows you across every agent you use.

```
$ git prompted stats
Topics
  architecture         60% →  (2 nailed / 2 partial / 1 missed)
  concurrency          50% ↑  (1 nailed / 2 partial / 1 missed)

Weak spots
  cancellation             concurrency     33% (missed 2)
  testing-seams            architecture     0% (missed 1)

Due for review
  cancellation             concurrency
```

Works with **Claude Code**, **OpenAI Codex CLI** and **Gemini CLI**.

## No extra AI bill

git prompted is a ~2 MB Go binary with **no model calls and no network access** after install. Its hooks only decide *when* to pause, and they hand instructions back to the agent session you're already running. The questioning and grading happen there, on your existing plan or API key.

## Install

**Claude Code**
```
/plugin marketplace add ryangovender/git-prompted
/plugin install git-prompted@git-prompted
```

**Gemini CLI** (v0.26+, where hooks are on by default)
```sh
gemini extensions install https://github.com/ryangovender/git-prompted
```

**Codex CLI**: see [integrations/codex](integrations/codex/README.md):
```sh
go install github.com/ryangovender/git-prompted/cmd/git-prompted@latest
git prompted install codex
```

Then pick your topics. The first time you start a session, your agent asks which topics you want to practise (from a built-in catalog, or describe your own and it drafts the topic for you) and saves them. Change them any time with `/git-prompted:learn init` in Claude Code, or ask your agent to "run git prompted onboarding". From a terminal:
```sh
git prompted topics list                     # built-in catalog; * = already in your goals
git prompted topics list --repo .            # ...with how many files here each topic matches
git prompted topics add sql error-handling
git prompted topics remove architecture
git prompted init --defaults                 # skip picking: concurrency + architecture
```

### Getting the `git prompted` command

Git runs any `git-<name>` on your PATH as a subcommand, so once the binary is on PATH, `git prompted` just works.
- With `go install` it already is.
- With the Claude plugin or Gemini extension, the launcher keeps a stable link at `~/.git-prompted/bin/git-prompted`. Add that folder to your PATH:
  ```sh
  export PATH="$HOME/.git-prompted/bin:$PATH"
  ```

The launcher downloads the binary for your platform from this repo's GitHub Releases on first use and **verifies its SHA-256 checksum** before running it. If that ever fails, hooks quietly do nothing rather than break your session. To use your own build, set `GIT_PROMPTED_BIN=/path/to/git-prompted`.

## See your progress

- **Claude Code status line:** run `git prompted install statusline`. Your weakest topics and reviews due then sit at the bottom of every session: `🧠 concurrency 50%↑ · architecture 60% · 2 due`. If you already have a status line, it's kept and shown alongside, and `git prompted uninstall statusline` restores it exactly.
- **Terminal:** `git prompted stats`, or `/git-prompted:learn stats` inside Claude Code.
- **Markdown:** `git prompted progress` writes `~/.git-prompted/progress.md`.
- **Your own UI:** `git prompted stats --json` outputs everything (topics, trends, per-concept accuracy, review schedule) for dashboards. A built-in local dashboard is on the roadmap.

To practise on purpose, run `/git-prompted:quiz weak` in Claude Code. It quizzes you on your weakest concepts using real code from your repo.

## How it works

| When | Claude Code | Codex | Gemini CLI | What happens |
|---|---|---|---|---|
| Session start | `SessionStart` | `SessionStart` | `SessionStart` | With `start_prompt: "ask"`, the agent asks once whether checkpoints are on this session. |
| Before an edit | `PreToolUse` | `PreToolUse` | `BeforeTool` | Scans the code about to be written. For an `ask-first` topic it blocks the edit and the agent offers the checkpoint, aiming at concepts due for review. Your choice is recorded and the retried edit goes through. |
| End of turn | `Stop` | `Stop` | `AfterAgent` | For `quiz-after` topics touched this turn, the agent offers a 2–3 question quiz on the code it just wrote. |

"Not this one", "not this session" and "mute topic" are offered every time. A per-topic cooldown and a per-session cap stop it from nagging.

**Weak spots and reviews.** Each graded answer records concept tags (`--got cancellation --missed goroutine-leak`). The agent is given the topic's vocabulary so tags stay consistent. Accuracy is tracked per concept over its last five outcomes. Missed concepts enter a Leitner schedule: due again after 1, 3, 7, 14, 30, then 60 days, and a miss resets to 1 day. Due concepts are fed into the next checkpoint for that topic.

```
cmd/git-prompted/      CLI, hook entrypoint, status line
internal/core/         agent-agnostic engine: config, matching, state, analytics, prompts
internal/adapter/      one adapter per agent: payload in, response out
assets/coaching.md     the teaching style, single source for every agent
bin/git-prompted       launcher (copied into integrations/claude by scripts/sync.sh)
hooks/ + gemini-extension.json + GEMINI.md   Gemini extension (must live at repo root)
integrations/claude/   Claude Code plugin (hooks, skill, slash commands)
integrations/codex/    Codex install notes
```

## Configuration

`~/.git-prompted/goals.json` holds your personal goals. A repo can add or override topics in a committed `.git-prompted.json`. This is handy for sharing team topics like "our hexagonal architecture rules".

```jsonc
{
  "start_prompt": "ask",              // ask | on | off
  "cooldown_minutes": 45,             // per topic, after a checkpoint, skip or quiz
  "max_checkpoints_per_session": 3,
  "topics": [
    {
      "id": "concurrency",
      "name": "Concurrency",
      "mode": "ask-first",            // ask-first (pause before) | quiz-after
      "level": "intermediate",
      "goals": ["Reason about goroutine lifetimes", "..."],
      "patterns": ["\\bgo func\\b", "sync\\.Mutex"],  // Go RE2 regex on the code being written
      "paths": ["internal/**/*.go"],  // optional glob filter; paths alone also work
      "concepts": ["cancellation", "goroutine-leak", "channel-ownership"]  // tag vocabulary
    }
  ]
}
```
(Comments are for explanation only; the real file is plain JSON. Patterns use Go's RE2 syntax, so there are no lookarounds.)

## Commands

```
git prompted init [--defaults|--empty] | topics list|add|remove | status | stats [--json] | progress | coaching | version
git prompted session on|off
git prompted decide --choice skip|session-off|mute [--topic ID]
git prompted done --topic ID --result nailed|partial|missed [--got TAGS] [--missed TAGS] [--note TEXT]
git prompted unmute --topic ID
git prompted statusline [--plain] [--with CMD]
git prompted install|uninstall codex|statusline
```

In Claude Code you also get `/git-prompted:learn on|off|status|stats|mute|progress` and `/git-prompted:quiz [topic|weak]`. In Codex and Gemini you can just ask, e.g. "turn git prompted off for this session". The agent knows the command from its session context.

## Data

Everything stays local in `~/.git-prompted/`:
- `goals.json`
- `state.json`: sessions, cooldowns, mutes; sessions older than 7 days are pruned
- `log.jsonl`: your results and concept tags
- `progress.md`
- `bin/`: binaries and the stable link

Nothing is written into your repositories, and the state is shared, so progress follows you between agents.

## Design principles

1. **Opt-in, always escapable.** Every interruption offers a way out, and "no" is never argued with.
2. **Never break the session.** Bad config, bad regexes, malformed payloads, panics and failed downloads all degrade to a silent no-op. Hooks always exit 0.
3. **Real code, not trivia.** Questions come from the change in front of you.
4. **Honest feedback.** Vague praise defeats the purpose of practising.

## Known limitations

- **Native Windows:** the launcher is a POSIX shell script, so it needs Git Bash or WSL for now. Native `.exe` binaries are already built; a `.cmd` launcher is a welcome PR.
- **Hook payloads change:** Codex and Gemini hook APIs are young, and the adapters are the place to fix drift.
- **Matching is crude:** pattern matching is cheap and predictable by design.
- **Tags depend on the agent:** tag quality depends on the agent following the coaching guide. The vocabulary in the prompt keeps them consistent, but expect some cleanup.

## Development

```sh
make build    # dist/git-prompted, which the launchers pick up automatically
make test     # go vet + go test + shell syntax check
make sync     # regenerate copies after editing bin/git-prompted, assets/coaching.md or VERSION
make dist     # cross-compile all platforms + checksums.txt
```

Release: bump `VERSION`, run `make sync`, commit, then tag `vX.Y.Z`. The release workflow tests, builds and publishes binaries plus checksums.

MIT licensed. PRs welcome.
