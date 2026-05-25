package mcp

import (
	"errors"
	"strings"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

func TestToolErr_Nil(t *testing.T) {
	if got := toolErr(nil); got != nil {
		t.Fatalf("toolErr(nil) = %v, want nil", got)
	}
}

func TestToolErr_RuntimeErrorPrefixedExit1(t *testing.T) {
	got := toolErr(&rufioerr.NoSuchThoughtError{ID: "x"})
	if got == nil || !strings.HasPrefix(got.Error(), "[rufio:1] ") {
		t.Fatalf("toolErr(NoSuchThoughtError) = %q, want prefix %q", got, "[rufio:1] ")
	}
}

func TestToolErr_UsageErrorPrefixedExit2(t *testing.T) {
	got := toolErr(&rufioerr.InvalidSubjectError{Subject: "x"})
	if got == nil || !strings.HasPrefix(got.Error(), "[rufio:2] ") {
		t.Fatalf("toolErr(InvalidSubjectError) = %q, want prefix %q", got, "[rufio:2] ")
	}
}

func TestToolErr_UnknownErrorDefaultsExit1(t *testing.T) {
	got := toolErr(errors.New("boom"))
	if got == nil || !strings.HasPrefix(got.Error(), "[rufio:1] ") {
		t.Fatalf("toolErr(plain error) = %q, want prefix %q", got, "[rufio:1] ")
	}
	if !strings.Contains(got.Error(), "boom") {
		t.Fatalf("toolErr(plain error) = %q, want it to contain original message", got)
	}
}
