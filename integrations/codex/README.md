# git-prompted for OpenAI Codex CLI

Codex has no plugin format, so the binary registers itself in `~/.codex/hooks.json`
(other hooks are preserved and the old file is backed up):

```sh
go install github.com/ryangovender/git-prompted/cmd/git-prompted@latest
# or download a release binary and put it on your PATH
git-prompted install codex
git-prompted init          # creates ~/.git-prompted/goals.json if you don't have one
```

Then make sure hooks are enabled in your Codex config (see https://developers.openai.com/codex/hooks),
start a new session, and approve the hooks if Codex asks you to trust them.

Remove with `git-prompted uninstall codex`.

Notes:
- Codex edits arrive as `apply_patch` text; git-prompted reads the added lines and file paths from the patch.
- The coaching guide is injected at session start, since Codex has no skill bundled with the hooks.
- If you move or upgrade the binary, re-run `git prompted install codex` so the hook path is current.
