---
description: Quiz me now on one of my git prompted topics (or my weakest spots), using this codebase
argument-hint: [topic | weak]
allowed-tools: Bash, Read, Grep, Glob
---
Run a git prompted quiz on: $ARGUMENTS

CLI: `"${CLAUDE_PLUGIN_ROOT}/bin/git-prompted"` (or the command path from the [git-prompted] session context).

- If the argument is empty or `weak`, run `stats --json` and quiz on the weakest concepts and anything due for review.
- Otherwise quiz on that topic, still preferring its due or weak concepts.

Follow the git prompted coaching skill. Pick real code from this repository that exercises those concepts, ask 2-3 questions one at a time, grade honestly, then record it with `done --topic <id> --result <nailed|partial|missed> --got <tags> --missed <tags> --note "<one line>"`.
