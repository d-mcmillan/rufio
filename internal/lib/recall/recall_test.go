package recall

import (
	"errors"
	"testing"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

// --- ValidateTypes ---

func TestValidateTypes_EmptyReturnsAll(t *testing.T) {
	got, err := ValidateTypes("")
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if len(got) != len(AllTypes) {
		t.Errorf("got %d types, want %d (AllTypes)", len(got), len(AllTypes))
	}
}

func TestValidateTypes_AcceptsSingleAndCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"thought", []string{"thought"}},
		{"thought,observation", []string{"thought", "observation"}},
		// Full CSV of every AllTypes member round-trips intact. K2/#R28
		// extended AllTypes with confirm/refute/retract; v1.0.3 extended
		// further with auto-promote (for the listen surface). The
		// canonical fixture lives next to the enum.
		{"given,learned,thought,observation,reason,summon,confirm,refute,retract,channel-message,goal,auto-promote", AllTypes},
	}
	for _, tc := range cases {
		got, err := ValidateTypes(tc.in)
		if err != nil {
			t.Errorf("ValidateTypes(%q): unexpected %v", tc.in, err)
			continue
		}
		if !equalStrings(got, tc.want) {
			t.Errorf("ValidateTypes(%q)=%v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidateTypes_RejectsUnknown(t *testing.T) {
	cases := []string{"bogus", "thought,bogus", "GIVEN", "thought,GIVEN"}
	for _, in := range cases {
		_, err := ValidateTypes(in)
		var got *rufioerr.InvalidTypesError
		if !errors.As(err, &got) {
			t.Errorf("ValidateTypes(%q): want *InvalidTypesError, got %T %v", in, err, err)
		}
	}
}

func TestValidateTypes_TrimsWhitespace(t *testing.T) {
	got, err := ValidateTypes(" thought , observation ")
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	want := []string{"thought", "observation"}
	if !equalStrings(got, want) {
		t.Errorf("got=%v want=%v", got, want)
	}
}

// --- ParseSince ---

func TestParseSince_EmptyReturnsZero(t *testing.T) {
	got, err := ParseSince("")
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if got != 0 {
		t.Errorf("got=%v want 0", got)
	}
}

func TestParseSince_AcceptsValid(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"10m", 10 * time.Minute},
		{"2h", 2 * time.Hour},
		{"24h", 24 * time.Hour},
		{"500ms", 500 * time.Millisecond},
	}
	for _, tc := range cases {
		got, err := ParseSince(tc.in)
		if err != nil {
			t.Errorf("ParseSince(%q): unexpected %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSince(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseSince_RejectsMalformed(t *testing.T) {
	for _, raw := range []string{"abc", "10", "10x", "-5m"} {
		_, err := ParseSince(raw)
		var got *rufioerr.InvalidDurationError
		if !errors.As(err, &got) {
			t.Errorf("ParseSince(%q): want *InvalidDurationError, got %T %v", raw, err, err)
		}
	}
}

// --- ParseAsOf ---

func TestParseAsOf_EmptyReturnsZeroTime(t *testing.T) {
	got, err := ParseAsOf("")
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if !got.IsZero() {
		t.Errorf("got=%v want zero time", got)
	}
}

func TestParseAsOf_AcceptsRFC3339(t *testing.T) {
	cases := []string{
		"2026-05-12T12:00:00Z",
		"2026-05-12T12:00:00.123Z",
		"2026-05-12T12:00:00.123456789Z",
	}
	for _, raw := range cases {
		got, err := ParseAsOf(raw)
		if err != nil {
			t.Errorf("ParseAsOf(%q): unexpected %v", raw, err)
			continue
		}
		if got.IsZero() {
			t.Errorf("ParseAsOf(%q): unexpected zero time", raw)
		}
	}
}

func TestParseAsOf_RejectsMalformed(t *testing.T) {
	for _, raw := range []string{"abc", "2026-05-12", "12:00:00"} {
		_, err := ParseAsOf(raw)
		var got *rufioerr.InvalidTimestampError
		if !errors.As(err, &got) {
			t.Errorf("ParseAsOf(%q): want *InvalidTimestampError, got %T %v", raw, err, err)
		}
	}
}

// helper
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
