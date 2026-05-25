#!/bin/bash
# Run 2 pre-flight — execute when ready to launch the 4 harnesses
# This is the same shape as Run 1, with the #180 + #181 affordances active.
#
# Run from the rufio repo root, OR set RUFIO_REPO to the repo path:
#   cd <rufio-repo>; ./docs/cross-harness-prompts/run2-preflight.sh
#   RUFIO_REPO=/path/to/rufio ./docs/cross-harness-prompts/run2-preflight.sh

set -e
RUFIO_REPO="${RUFIO_REPO:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
export RUN2_DIR=/tmp/rufio-cross-harness-2026-05-21-run2
rm -rf "$RUN2_DIR"
mkdir -p "$RUN2_DIR"
cd "$RUN2_DIR"

# init rufio
rufio init

# git init for codex compatibility
git init -q

# seed lead's attention
RUFIO_AGENT_ID=lead rufio attend \
  --intent="moderating cross-harness Run 2 — v1.3 priority decision" \
  --entities=rufio:substrate \
  --topics=cross-harness-test,v1-3-roadmap \
  --scope=fleet

# seed scenario.md from the canonical doc
mkdir -p given
cp "$RUFIO_REPO/docs/cross-harness-prompts/scenario-run2.md" given/scenario.md

# push to live (skips approve/promote workflow)
RUFIO_AGENT_ID=lead rufio push given/scenario.md --stage=live

# start daemon (one for the whole session)
( rufio dev > /tmp/rufio-cx-run2-dev.log 2>&1 & )
sleep 2

# diary dir
mkdir -p ~/rufio-cross-harness-2026-05-21-run2

echo ""
echo "=== Run 2 substrate ready at: $RUN2_DIR ==="
rufio fleet
echo ""
echo "Next: launch the 4 harnesses. Same per-vendor prompts as Run 1, with two changes:"
echo "  - cd path is /tmp/rufio-cross-harness-2026-05-21-run2"
echo "  - diary dir is ~/rufio-cross-harness-2026-05-21-run2/"
echo "  - new affordances: rufio recall --topics=, rufio confirms <id>"
