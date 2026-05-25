#!/bin/bash
# Run 3 pre-flight — multi-step planning cognitive shape test
# Tests generalization beyond Runs 1+2 (decision-prioritization shape).
#
# Run from the rufio repo root, OR set RUFIO_REPO to the repo path:
#   cd <rufio-repo>; ./docs/cross-harness-prompts/run3-preflight.sh
#   RUFIO_REPO=/path/to/rufio ./docs/cross-harness-prompts/run3-preflight.sh

set -e
RUFIO_REPO="${RUFIO_REPO:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
export RUN3_DIR=/tmp/rufio-cross-harness-2026-05-21-run3
rm -rf "$RUN3_DIR"
mkdir -p "$RUN3_DIR"
cd "$RUN3_DIR"

# init rufio
rufio init

# git init for codex compatibility
git init -q

# seed lead's attention
RUFIO_AGENT_ID=lead rufio attend \
  --intent="moderating cross-harness Run 3 — rufio open implementation plan" \
  --entities=rufio:v1-2-roadmap \
  --topics=cross-harness-test,v1-2-roadmap,rufio-open-impl \
  --scope=fleet

# seed scenario.md
mkdir -p given
cp "$RUFIO_REPO/docs/cross-harness-prompts/scenario-run3.md" given/scenario.md

# push to live (skips approve/promote workflow)
RUFIO_AGENT_ID=lead rufio push given/scenario.md --stage=live

# seed the Run 1 decision as context — copy the promoted observation
# (so agents can read it via recall in addition to seeing the file)
mkdir -p learned/roadmap/v1-2
cp "$RUFIO_REPO/captures/2026-05-21-cross-harness-live/learned/roadmap/v1-2/1779333639324-t60kgb.gdlm" \
   learned/roadmap/v1-2/ 2>/dev/null || echo "  (Run 1 decision artifact unavailable — agents will read via scenario.md instead)"

# start daemon
( rufio dev > /tmp/rufio-cx-run3-dev.log 2>&1 & )
sleep 2

# diary dir
mkdir -p ~/rufio-cross-harness-2026-05-21-run3

echo ""
echo "=== Run 3 substrate ready at: $RUN3_DIR ==="
rufio fleet
echo ""
echo "Scenario shape: multi-step planning (different from Run 1+2 decision-prioritization)"
echo "Expected: agents collaboratively construct a structured rufio open implementation plan"
echo ""
echo "Next: launch the 4 harnesses with the *-run3.md prompts"
