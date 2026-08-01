#!/usr/bin/env bash
# SessionStart hook: report which pipeline gate is currently open.
#
# Reads the single source of truth — the "**Current gate:**" line in CLAUDE.md —
# and emits hook JSON so both the user and the model start the session knowing
# what stage the project is in. Never fails the session.

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GATE="$(sed -n 's/^\*\*Current gate:\*\* //p' "$REPO_ROOT/CLAUDE.md" 2>/dev/null | head -1)"

[ -z "$GATE" ] && GATE="unknown (no 'Current gate' line found in CLAUDE.md)"

DOCKER="down"
docker info >/dev/null 2>&1 && DOCKER="up"

jq -n --arg gate "$GATE" --arg docker "$DOCKER" '
{
  systemMessage: ("GitCherryPick — open gate: " + $gate + "  |  docker: " + $docker),
  suppressOutput: true,
  hookSpecificOutput: {
    hookEventName: "SessionStart",
    additionalContext: ("Pipeline state: the currently open gate is \"" + $gate
      + "\". Docker daemon is " + $docker
      + ". Do not begin work belonging to a later stage until the owner approves this one."
      + " Stage order and rules are in CLAUDE.md.")
  }
}'
