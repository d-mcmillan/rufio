package cli

import (
	"testing"
)

// TestDefaultEventHandler_RoutesChannelMessage asserts that add events
// on live/channels/active/<ch>/messages/<msg>.gdl route through to
// routing.RouteChannelMessage. Non-existent file → non-nil error
// (proves the closure reached RouteChannelMessage rather than no-op'd).
func TestDefaultEventHandler_RoutesChannelMessage(t *testing.T) {
	root := t.TempDir()
	h := defaultEventHandler(root)
	err := h(FileEvent{Kind: "add", Path: "live/channels/active/ch-xyz/messages/msg-abc.gdl"})
	if err == nil {
		t.Fatal("expected non-nil error from RouteChannelMessage on missing file")
	}
}

// TestDefaultEventHandler_IgnoresMetaGdl asserts that meta.gdl events
// (which fire when AppendLeave/AppendClose rewrite the file) are NOT
// routed as messages — the basename check short-circuits the handler.
func TestDefaultEventHandler_IgnoresMetaGdl(t *testing.T) {
	root := t.TempDir()
	h := defaultEventHandler(root)
	err := h(FileEvent{Kind: "add", Path: "live/channels/active/ch-xyz/meta.gdl"})
	if err != nil {
		t.Errorf("expected nil (meta.gdl skipped), got %v", err)
	}
}

// TestDefaultEventHandler_IgnoresClosedChannelEvents — channels/closed
// is watched for defense-in-depth pre-create only; no engine consumes
// events from there.
func TestDefaultEventHandler_IgnoresClosedChannelEvents(t *testing.T) {
	root := t.TempDir()
	h := defaultEventHandler(root)
	err := h(FileEvent{Kind: "add", Path: "live/channels/closed/ch-xyz/meta.gdl"})
	if err != nil {
		t.Errorf("expected nil (closed/ is watch-only), got %v", err)
	}
}
