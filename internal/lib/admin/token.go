// Package admin holds the bearer-token data model: minting, hashing,
// resolving, and revoking the tokens that `rufio serve` accepts as proof
// of identity. Tokens are stored hashed (SHA-256) on disk so a compromised
// .rufio/.admin/tokens.gdl never leaks active credentials; the plaintext
// is shown to the operator EXACTLY ONCE at mint time.
//
// Token format: rufio_<base64url(32 random bytes)>. The "rufio_" prefix is
// a soft fingerprint — it lets a leaked token be grepped out of logs by
// secret-scanners, the same convention GitHub uses for ghp_/gho_ tokens.
//
// On-disk record shape (one per token, in tokens.gdl):
//
//	@token|id:tok-xxxxxxxxxx|agent:alice|hash:<sha256-hex>|created:<RFC3339Nano>|revoked:false
//
// The hash is the SHA-256 of the plaintext; comparison happens via
// crypto/subtle.ConstantTimeCompare to defeat timing oracles.
package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/fslock"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
)

// ErrTokenInvalid is returned when a plaintext token cannot be resolved
// to a non-revoked identity (unknown, malformed, or revoked). Callers
// MUST treat all three reasons identically to avoid leaking which
// invariant failed.
var ErrTokenInvalid = errors.New("token invalid")

// TokenPrefix is the public soft-fingerprint prefix every plaintext token
// carries. Useful for secret-scanners and log scrubbers.
const TokenPrefix = "rufio_"

// IDPrefix is the prefix on the public token id (the user-visible handle
// printed at mint time and used by `rufio admin token revoke <id>`).
const IDPrefix = "tok-"

// tokensFile is the relative path under root where the hashed token store
// lives. Under .rufio/.admin/ to make ignoring + permission-scoping easy
// (the .admin/ subdirectory is grep-friendly for "this is operator-only
// material").
const tokensFile = ".rufio/.admin/tokens.gdl"

// Token is the public projection of an on-disk @token record. The hash is
// NOT exposed — callers can't reconstruct or verify the plaintext from
// outside the package.
type Token struct {
	ID      string
	Agent   string
	Created time.Time
	Revoked bool
}

// MintToken generates a fresh bearer token for agent and persists its
// SHA-256 hash to disk. Returns the plaintext (shown ONCE to the operator)
// and the public projection.
//
// The plaintext NEVER touches disk. Storing the hash is sufficient for
// resolution because comparison uses crypto/subtle.ConstantTimeCompare.
func MintToken(root, agent string) (plaintext string, t Token, err error) {
	if err := identity.Validate(agent); err != nil {
		return "", Token{}, err
	}
	// 32 random bytes ≈ 256 bits of entropy — same level as common
	// cryptographic API keys (GitHub, Stripe). base64url makes the
	// plaintext URL-safe and shell-paste-safe.
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Token{}, err
	}
	plaintext = TokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	id := TokenIDFromPlaintext(plaintext)
	created := time.Now().UTC()
	tok := Token{
		ID:      id,
		Agent:   agent,
		Created: created,
		Revoked: false,
	}
	if err := appendTokenRecord(root, tok, hashHex(plaintext)); err != nil {
		return "", Token{}, err
	}
	return plaintext, tok, nil
}

// ResolveToken verifies plaintext against the on-disk store and returns
// the associated agent identity. Returns ErrTokenInvalid for unknown /
// malformed / revoked tokens (callers must NOT differentiate — the 401
// reason is "token invalid or revoked" regardless).
func ResolveToken(root, plaintext string) (string, error) {
	if !strings.HasPrefix(plaintext, TokenPrefix) {
		return "", ErrTokenInvalid
	}
	want := hashHex(plaintext)
	records, err := readTokensFile(root)
	if err != nil {
		return "", err
	}
	for _, t := range records {
		if t.revoked {
			continue
		}
		// Constant-time compare — same length on both sides (hex of a
		// 32-byte SHA-256 is always 64 chars), so the early-exit
		// length check is safe.
		if subtle.ConstantTimeCompare([]byte(t.hash), []byte(want)) == 1 {
			// Defense in depth: a corrupt tokens.gdl row with an empty
			// `agent:` field would otherwise resolve to currentAgent=""
			// downstream, which the stream/channel_privacy.go predicate
			// treats as anonymous firehose — leaking every channel-
			// message to that token's holder. MintToken validates the
			// agent at write time so this only fires under operator-
			// induced corruption (hand-edited tokens.gdl), but reject
			// it explicitly rather than relying on every reader to
			// re-check.
			if t.agent == "" {
				return "", ErrTokenInvalid
			}
			return t.agent, nil
		}
	}
	return "", ErrTokenInvalid
}

// RevokeToken marks the token with the given public id as revoked. The
// record stays on disk so a later list still surfaces it (with revoked:
// true); ResolveToken refuses revoked tokens regardless of plaintext
// match.
//
// Idempotent: revoking an already-revoked token is a no-op success.
// Returns ErrTokenInvalid when the id is unknown.
//
// Security audit F2: the read→modify→write body MUST be serialised
// through the same .admin/tokens.lock that appendTokenRecord uses.
// Pre-fix, a Revoke racing a Mint could read the pre-mint state,
// then write back AFTER Mint had added the new record, silently
// dropping the just-minted token. Operator believes the new agent
// has a valid token (Mint returned it); next ResolveToken sees no
// record. That is data loss, not just a race.
func RevokeToken(root, tokenID string) error {
	if err := ensureAdminDir(root); err != nil {
		return err
	}
	lockDir := filepath.Join(root, ".rufio", ".admin", "tokens.lock")
	_, err := fslock.WithLock(lockDir, 0, func() (struct{}, error) {
		records, err := readTokensFile(root)
		if err != nil {
			return struct{}{}, err
		}
		hit := false
		for i := range records {
			if records[i].id == tokenID {
				records[i].revoked = true
				hit = true
			}
		}
		if !hit {
			return struct{}{}, ErrTokenInvalid
		}
		return struct{}{}, writeTokensFile(root, records)
	})
	return err
}

// ListTokens returns the public projection of every token on disk
// (revoked + active), sorted by created time ascending. Hashes are
// never exposed.
func ListTokens(root string) ([]Token, error) {
	records, err := readTokensFile(root)
	if err != nil {
		return nil, err
	}
	out := make([]Token, 0, len(records))
	for _, r := range records {
		ts, _ := time.Parse(time.RFC3339Nano, r.created)
		out = append(out, Token{
			ID:      r.id,
			Agent:   r.agent,
			Created: ts,
			Revoked: r.revoked,
		})
	}
	return out, nil
}

// TokenIDFromPlaintext returns the deterministic public id we surface for
// a given plaintext. Computed from the FIRST 6 bytes of the hash hex so
// the id is stable across mint/resolve/revoke without exposing the
// plaintext or the full hash. 6 hex chars = ~24 bits — collisions are
// statistically negligible at the operator scale this is sized for
// (<10k tokens per host).
func TokenIDFromPlaintext(plaintext string) string {
	h := hashHex(plaintext)
	if len(h) < 10 {
		return IDPrefix + h
	}
	return IDPrefix + h[:10]
}

// hashHex returns hex(sha256(plaintext)).
func hashHex(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// tokenRecord is the internal mutable shape we round-trip to disk.
// Exposed only through Token (which omits the hash).
type tokenRecord struct {
	id      string
	agent   string
	hash    string // hex-encoded sha256
	created string // RFC3339Nano string, kept verbatim for round-trip
	revoked bool
}

func tokensPath(root string) string {
	return filepath.Join(root, tokensFile)
}

func ensureAdminDir(root string) error {
	return os.MkdirAll(filepath.Join(root, ".rufio", ".admin"), 0o700)
}

// readTokensFile reads + parses the on-disk store. Returns (nil, nil) for
// a missing file (a fresh substrate has no minted tokens, which is a legal
// state — `serve` will refuse every request, but that's a runtime config
// signal, not a parse error).
func readTokensFile(root string) ([]tokenRecord, error) {
	bs, err := os.ReadFile(tokensPath(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return nil, err
	}
	out := make([]tokenRecord, 0, len(records))
	for _, r := range records {
		if r.Type != "token" {
			continue
		}
		out = append(out, tokenRecord{
			id:      r.Get("id"),
			agent:   r.Get("agent"),
			hash:    r.Get("hash"),
			created: r.Get("created"),
			revoked: r.Get("revoked") == "true",
		})
	}
	return out, nil
}

// writeTokensFile re-renders the store from records. Atomic via .tmp +
// rename so a concurrent crash never strands a half-written file.
// Serialised by the .admin lock so concurrent mints/revokes don't race.
func writeTokensFile(root string, records []tokenRecord) error {
	if err := ensureAdminDir(root); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# rufio admin tokens — DO NOT EDIT BY HAND\n")
	b.WriteString("# Format: @token|id:<id>|agent:<agent>|hash:<sha256-hex>|created:<ts>|revoked:<bool>\n")
	for _, t := range records {
		b.WriteString(gdl.RenderLine(gdl.Record{Type: "token", Fields: []gdl.RecordField{
			{Key: "id", Value: t.id},
			{Key: "agent", Value: t.agent},
			{Key: "hash", Value: t.hash},
			{Key: "created", Value: t.created},
			{Key: "revoked", Value: boolStr(t.revoked)},
		}}))
		b.WriteString("\n")
	}
	tmp := tokensPath(root) + ".tmp"
	defer func() { _ = os.Remove(tmp) }()
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, tokensPath(root))
}

// appendTokenRecord adds a single record by reading + appending + writing
// under the .admin lock so a concurrent mint never loses the loser's
// record.
func appendTokenRecord(root string, t Token, hash string) error {
	if err := ensureAdminDir(root); err != nil {
		return err
	}
	lockDir := filepath.Join(root, ".rufio", ".admin", "tokens.lock")
	_, err := fslock.WithLock(lockDir, 0, func() (struct{}, error) {
		records, err := readTokensFile(root)
		if err != nil {
			return struct{}{}, err
		}
		records = append(records, tokenRecord{
			id:      t.ID,
			agent:   t.Agent,
			hash:    hash,
			created: t.Created.UTC().Format(time.RFC3339Nano),
			revoked: t.Revoked,
		})
		return struct{}{}, writeTokensFile(root, records)
	})
	return err
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
