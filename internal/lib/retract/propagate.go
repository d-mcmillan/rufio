package retract

import (
	"os"
	"path/filepath"
	"strings"
)

// PropagateRetract reads live/retracted/<targetID>.gdl, then scans
// live/inbox/*/<targetID>.gdl and atomically appends an @retract line to
// each match. Idempotent: skips inboxes that already contain @retract
// for this target (D8.7). Per design §2.I engine #5.
func PropagateRetract(root, targetID string) error {
	retractPath := filepath.Join(root, "live", "retracted", targetID+".gdl")
	retractBytes, err := os.ReadFile(retractPath)
	if err != nil {
		return err
	}
	// The retracted file contains a single @retract record; the rendered
	// line is direct-appendable.
	retractLine := strings.TrimRight(string(retractBytes), "\n")

	pattern := filepath.Join(root, "live", "inbox", "*", targetID+".gdl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, m := range matches {
		if err := appendRetractIfMissing(m, retractLine, targetID); err != nil {
			return err
		}
	}
	return nil
}

// appendRetractIfMissing reads the inbox file, checks for an existing
// @retract|target:<id> line (idempotency guard per D8.7), and if absent
// appends the retract line atomically via .tmp + rename.
func appendRetractIfMissing(inboxPath, retractLine, targetID string) error {
	bs, err := os.ReadFile(inboxPath)
	if err != nil {
		return err
	}
	marker := "@retract|target:" + targetID
	if strings.Contains(string(bs), marker) {
		return nil // already retracted; idempotent no-op
	}
	content := strings.TrimRight(string(bs), "\n") + "\n" + retractLine + "\n"
	tmp := inboxPath + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// <inbox>.tmp under live/inbox/ (#141). Success path: Rename already
	// moved tmp, so this Remove is a harmless no-op. Idempotency guard
	// and ordering above are unchanged.
	defer func() { _ = os.Remove(tmp) }()
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, inboxPath)
}
