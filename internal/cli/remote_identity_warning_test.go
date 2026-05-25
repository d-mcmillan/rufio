package cli

import (
	"strings"
	"testing"
)

// TestEmitIdentityMismatchWarning_Fires asserts the warning emits the
// canonical one-line stderr message the spec mandates when --server is in
// play and RUFIO_AGENT_ID differs from the server-resolved bound agent.
// (Field feedback / Joey #213 — primer foot-gun where the env var is
// silently ignored.)
func TestEmitIdentityMismatchWarning_Fires(t *testing.T) {
	t.Setenv("RUFIO_AGENT_ID", "alice")
	resetIdentityMismatchOnceForTest()

	out := captureStderr(t, func() {
		emitIdentityMismatchWarning(map[string]interface{}{"agent": "bob"})
	})

	if !strings.Contains(out, "note: --token is bound to agent=bob") {
		t.Fatalf("expected bound-agent line, got %q", out)
	}
	if !strings.Contains(out, "RUFIO_AGENT_ID=alice is ignored") {
		t.Fatalf("expected ignored-env line, got %q", out)
	}
	if !strings.Contains(out, "server-authoritative identity") {
		t.Fatalf("expected server-authoritative identity hint, got %q", out)
	}
}

// TestEmitIdentityMismatchWarning_OnlyOnce confirms the sync.Once gate
// keeps the warning to a single line even across many --server-routed
// verbs in a single process.
func TestEmitIdentityMismatchWarning_OnlyOnce(t *testing.T) {
	t.Setenv("RUFIO_AGENT_ID", "alice")
	resetIdentityMismatchOnceForTest()

	out := captureStderr(t, func() {
		for i := 0; i < 5; i++ {
			emitIdentityMismatchWarning(map[string]interface{}{"agent": "bob"})
		}
	})

	n := strings.Count(out, "note: --token is bound to agent=bob")
	if n != 1 {
		t.Fatalf("expected exactly one warning line, got %d in %q", n, out)
	}
}

// TestEmitIdentityMismatchWarning_SilentWhenMatching asserts no warning
// when env and bound agent agree (the common case for cleanly-configured
// scripts).
func TestEmitIdentityMismatchWarning_SilentWhenMatching(t *testing.T) {
	t.Setenv("RUFIO_AGENT_ID", "alice")
	resetIdentityMismatchOnceForTest()

	out := captureStderr(t, func() {
		emitIdentityMismatchWarning(map[string]interface{}{"agent": "alice"})
	})

	if out != "" {
		t.Fatalf("expected no output when env == bound agent, got %q", out)
	}
}

// TestEmitIdentityMismatchWarning_SilentWhenNoEnv asserts no warning when
// RUFIO_AGENT_ID is unset — there's nothing being ignored, so nothing to
// surface.
func TestEmitIdentityMismatchWarning_SilentWhenNoEnv(t *testing.T) {
	t.Setenv("RUFIO_AGENT_ID", "")
	resetIdentityMismatchOnceForTest()

	out := captureStderr(t, func() {
		emitIdentityMismatchWarning(map[string]interface{}{"agent": "bob"})
	})

	if out != "" {
		t.Fatalf("expected no output when RUFIO_AGENT_ID is unset, got %q", out)
	}
}

// TestEmitIdentityMismatchWarning_SilentWhenNoBoundAgent asserts no
// warning when the server response carries no `agent` field — we can't
// determine the bound identity from this response, so we stay quiet and
// let the next response that does carry it fire the warning.
func TestEmitIdentityMismatchWarning_SilentWhenNoBoundAgent(t *testing.T) {
	t.Setenv("RUFIO_AGENT_ID", "alice")
	resetIdentityMismatchOnceForTest()

	out := captureStderr(t, func() {
		emitIdentityMismatchWarning(map[string]interface{}{"some_other_field": "x"})
	})

	if out != "" {
		t.Fatalf("expected no output when response lacks `agent`, got %q", out)
	}
}
