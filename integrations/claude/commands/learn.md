---
description: Control git prompted learning checkpoints (on, off, status, stats, mute <topic>, unmute <topic>, progress, init)
argument-hint: on | off | status | stats | mute <topic> | unmute <topic> | progress | init
allowed-tools: Bash
---
Run the git prompted CLI for the user and report the result briefly.

CLI: `"${CLAUDE_PLUGIN_ROOT}/bin/git-prompted"` (if that variable is not expanded here, use the command path from the [git-prompted] session context).

Map the arguments:
- `on` / `off` -> `session on` / `session off`
- `mute <topic>` -> `decide --topic <topic> --choice mute`
- `unmute <topic>` -> `unmute --topic <topic>`
- `stats` -> `stats`; show the output as-is, then in one or two sentences point out the weakest spot and suggest `/git-prompted:quiz` on it.
- `status`, `progress`, `init` -> same name. For `progress`, also show the user the contents of the file it prints.
- no arguments -> `status`

Arguments: $ARGUMENTS
