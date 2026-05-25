package privacy_test

import (
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/privacy"
)

// stubRecord is a minimal Record implementation for table-driven tests —
// keeps the test suite decoupled from any concrete on-disk record type
// (Thought/Goal/Observation/Attention/…). Mirrors how the production
// callers will wrap their own structs to satisfy privacy.Record.
type stubRecord struct {
	scope  string
	author string
}

func (s stubRecord) GetScope() string  { return s.scope }
func (s stubRecord) GetAuthor() string { return s.author }

func TestPrivacy_IsVisible_TableDriven(t *testing.T) {
	cases := []struct {
		name         string
		scope        string
		author       string
		currentAgent string
		want         bool
	}{
		// Anonymous firehose: every record visible, scope ignored.
		{"anonymous sees scope:agent of others", "agent", "alice", "", true},
		{"anonymous sees scope:fleet", "fleet", "alice", "", true},
		{"anonymous sees scope:deployment", "deployment", "alice", "", true},
		{"anonymous sees empty scope", "", "alice", "", true},

		// scope:agent — only the author sees it.
		{"alice sees own scope:agent", "agent", "alice", "alice", true},
		{"bob does NOT see alice's scope:agent", "agent", "alice", "bob", false},

		// Broader scopes always visible regardless of author.
		{"bob sees alice's scope:deployment", "deployment", "alice", "bob", true},
		{"bob sees alice's scope:fleet", "fleet", "alice", "bob", true},

		// Empty scope (legacy / given) — visible to any identified caller.
		{"bob sees record with empty scope", "", "alice", "bob", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := stubRecord{scope: tc.scope, author: tc.author}
			got := privacy.IsVisible(r, tc.currentAgent)
			if got != tc.want {
				t.Errorf("IsVisible(scope=%q author=%q currentAgent=%q) = %v, want %v",
					tc.scope, tc.author, tc.currentAgent, got, tc.want)
			}
		})
	}
}

func TestPrivacy_CanWriteAgainst_TableDriven(t *testing.T) {
	cases := []struct {
		name         string
		scope        string
		author       string
		currentAgent string
		want         bool
	}{
		// scope:agent — only the author can confirm/refute.
		{"alice can write against own scope:agent", "agent", "alice", "alice", true},
		{"bob CANNOT write against alice's scope:agent", "agent", "alice", "bob", false},

		// Broader scopes — anyone can confirm/refute (crowd validation).
		{"bob can write against alice's scope:deployment", "deployment", "alice", "bob", true},
		{"bob can write against alice's scope:fleet", "fleet", "alice", "bob", true},

		// Empty scope (no explicit scope on the target) — non-restrictive.
		{"bob can write against record with empty scope", "", "alice", "bob", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := stubRecord{scope: tc.scope, author: tc.author}
			got := privacy.CanWriteAgainst(r, tc.currentAgent)
			if got != tc.want {
				t.Errorf("CanWriteAgainst(scope=%q author=%q currentAgent=%q) = %v, want %v",
					tc.scope, tc.author, tc.currentAgent, got, tc.want)
			}
		})
	}
}
