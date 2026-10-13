#!/bin/sh
# Regenerates the per-agent copies from their single sources of truth:
#   bin/git-prompted      -> integrations/claude/bin/git-prompted  (Claude plugins only see their own dir)
#   assets/coaching.md  -> integrations/claude/skills/coaching/SKILL.md, GEMINI.md
#   VERSION             -> shim, manifests
# CI runs this and fails if it produces a diff.
set -eu
cd "$(dirname "$0")/.."
v=$(tr -d ' \n' < VERSION)

sed -i.bak "s/^VERSION=\".*\"/VERSION=\"$v\"/" bin/git-prompted && rm -f bin/git-prompted.bak
for f in gemini-extension.json integrations/claude/.claude-plugin/plugin.json; do
  sed -i.bak "s/\"version\": \"[^\"]*\"/\"version\": \"$v\"/" "$f" && rm -f "$f.bak"
done

cp bin/git-prompted integrations/claude/bin/git-prompted
chmod +x bin/git-prompted integrations/claude/bin/git-prompted

{
  printf '%s\n' '---' 'name: coaching'
  printf '%s\n' 'description: How to run git prompted onboarding, learning checkpoints and quizzes, including grading with concept tags. Use this whenever a [git-prompted] hook message appears (a blocked edit marked "Learning checkpoint", a stop message offering a quiz, or a request to run onboarding and pick topics), whenever the user runs /git-prompted:quiz or /git-prompted:learn init, or whenever the user says they want to think something through themselves, be tested, or practise a topic instead of having the code written for them.'
  printf '%s\n\n' '---'
  printf '%s\n' '<!-- Generated from assets/coaching.md by scripts/sync.sh. Edit that file instead. -->'
  cat assets/coaching.md
} > integrations/claude/skills/coaching/SKILL.md

{
  printf '%s\n\n' '<!-- Generated from assets/coaching.md by scripts/sync.sh. Edit that file instead. -->'
  cat assets/coaching.md
} > GEMINI.md
echo "synced v$v"
