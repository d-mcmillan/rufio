package cli

import (
	"errors"
	"strings"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

// TestValidateOpenSubject_RejectsThoughtID_HintsLineage pins the
// cross-verb breadcrumb: an arg shaped like a thought id (digits-dash-
// random6) is rejected with a hint at `rufio lineage <id>`, the right
// verb for thought-history queries.
func TestValidateOpenSubject_RejectsThoughtID_HintsLineage(t *testing.T) {
	err := validateOpenSubject("1779345848015-cxkzz1")
	if err == nil {
		t.Fatal("expected error on thought-id-shaped subject, got nil")
	}
	var usage *rufioerr.UsageError
	if !errors.As(err, &usage) {
		t.Errorf("expected *UsageError (exit 2), got %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "lineage") {
		t.Errorf("error must hint at rufio lineage; got %q", err.Error())
	}
}

// TestValidateOpenSubject_RejectsMalformed pins that an arg failing
// the namespace:local regex returns the standard InvalidSubjectError
// (exit 2) — same envelope every write verb returns.
func TestValidateOpenSubject_RejectsMalformed(t *testing.T) {
	err := validateOpenSubject("badformat")
	if err == nil {
		t.Fatal("expected error on malformed subject, got nil")
	}
	var invSub *rufioerr.InvalidSubjectError
	if !errors.As(err, &invSub) {
		t.Errorf("expected *InvalidSubjectError, got %T %v", err, err)
	}
}

// TestValidateOpenSubject_AcceptsValid pins that a valid namespace:local
// passes the validator unchanged. The CLI front door trusts the
// validator's verdict; runOpen never re-validates.
func TestValidateOpenSubject_AcceptsValid(t *testing.T) {
	for _, sub := range []string{"test:1", "customer:5821", "deployment:rufio-core-1"} {
		if err := validateOpenSubject(sub); err != nil {
			t.Errorf("validateOpenSubject(%q) returned error %v; want nil", sub, err)
		}
	}
}
