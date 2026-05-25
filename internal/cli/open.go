// Package cli — `rufio open <subject>`.
//
// The cold-agent first-contact verb (v1.2.0). Bundles the 4-5 reads
// every agent does on first contact with a topic — identity, daemon
// health, fleet, attention, recall, thoughts — into a single
// substrate-state snapshot. Read-dual of `attend`.
//
// Pure read, no writes. Exit 0 on success (including empty sections);
// exit 2 on subject validation error or unknown flag. The `open`
// orchestration lives in internal/lib/open; this file owns flag
// parsing, identity resolution, validation, and rendering.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/open"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/recall"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// thoughtIDPattern matches the canonical thought-id shape
// <unix-millis>-<rand6>. Used in validateOpenSubject to recognize
// arguments that should be redirected to `rufio lineage` rather than
// generic-rejected. The same shape is enforced on the write side by
// thought.GenerateID.
var thoughtIDPattern = regexp.MustCompile(`^[0-9]+-[a-z0-9]{6}$`)

// openDefaultSince is the recency floor applied when --since is omitted.
// 24h matches the agent-original spec — recent-enough to surface today's
// activity, long-enough to span an overnight handoff.
const openDefaultSince = 24 * time.Hour

// openDefaultLimit is the per-section cap applied when --limit is omitted.
// 50 matches `rufio recall`'s practical row count for a focused subject
// — large enough to capture a real workstream, small enough to stay
// readable in a 24-row terminal.
const openDefaultLimit = 50

// openDefaultScope is the scope filter applied when --scope is omitted.
// "fleet" matches `attend`'s default — open is the read-dual, so it
// shares the broadest-permitted-default principle. privacy.IsVisible
// (#147) remains the effective floor: open.Bundle maps scope=fleet to
// an empty FilterParams.Scope via effectiveScope() so recall.Filter
// routes through privacy.IsVisible instead of scopePass — see
// internal/lib/open/open.go for the rationale.
const openDefaultScope = "fleet"

// NewOpenCmd returns the `rufio open <subject>` Cobra command.
//
//	rufio open <subject> [--topics=csv] [--since=24h]
//	                     [--scope=agent|deployment|fleet] [--limit=50]
//	                     [--json] [--no-color] [-q]
//
// <subject> is required (namespace:local — same regex as --subject on
// the write verbs). A thought-id-shaped argument is rejected with a
// hint at `rufio lineage <id>` — a deliberate cross-verb breadcrumb so
// agents who reach for `open` with a thought id learn the correct verb.
func NewOpenCmd() *cobra.Command {
	var topicsFlag, sinceFlag, scopeFlag string
	var limitFlag int
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "open <subject>",
		Short: "Read-bundle: identity + daemon + fleet + attention + recall + thoughts on subject",
		Long: `Open a subject — the read-dual of attend.

Bundles the 4-5 reads every agent does on first contact with a topic
(identity, daemon health, fleet, attention, recall, thoughts) into one
substrate-state snapshot. Pure read; no writes.

<subject> is namespace:local (e.g. customer:5821). A thought-id-shaped
argument is redirected to ` + "`rufio lineage <id>`" + ` — that's the right verb
for thought-history queries.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			subject := args[0]
			if err := validateOpenSubject(subject); err != nil {
				HandleError("open", err)
				return nil
			}
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runOpen(cmd, cwd, openArgs{
					Subject: subject,
					Topics:  topicsFlag,
					Since:   sinceFlag,
					Scope:   scopeFlag,
					Limit:   limitFlag,
				}, opts)
			}
			if err != nil {
				HandleError("open", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&topicsFlag, "topics", "",
		"CSV of topic tokens; server-side ANY-match against the recall+thoughts sections (mirrors recall --topics)")
	cmd.Flags().StringVar(&sinceFlag, "since", "",
		"recency floor as Go duration (e.g. 10m, 24h); default 24h")
	cmd.Flags().StringVar(&scopeFlag, "scope", "",
		"visibility scope (agent|deployment|fleet); default fleet — privacy.IsVisible is the floor regardless")
	cmd.Flags().IntVar(&limitFlag, "limit", 0,
		"max rows per section (default 50)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit a single JSON object")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// openArgs collects the flag values for runOpen. Mirrors the recallArgs
// shape — string flag values pre-parse, durations/limits resolved
// inside runOpen.
type openArgs struct {
	Subject string
	Topics  string
	Since   string
	Scope   string
	Limit   int
}

// validateOpenSubject is the front-door validator. It applies two
// rejections in order so the failure messages stay actionable:
//
//  1. Thought-id shape (`<unix-millis>-<rand6>`) → redirect to
//     `rufio lineage <id>`. Cold agents who reach for `open` with a
//     thought id learn the right verb instead of getting a generic
//     regex error.
//  2. Otherwise → standard thought.ValidateSubject (the same
//     namespace:local regex the write verbs use).
//
// Both rejections return UsageError so the dispatcher exits 2.
func validateOpenSubject(subject string) error {
	if thoughtIDPattern.MatchString(subject) {
		return &rufioerr.UsageError{Message: fmt.Sprintf(
			"%q looks like a thought id — try `rufio lineage %s` for the audit trail of that thought",
			subject, subject,
		)}
	}
	if err := thought.ValidateSubject(subject); err != nil {
		return err
	}
	return nil
}

// runOpen is the pure logic for `rufio open`. Resolves identity,
// applies defaults, calls open.Bundle, and dispatches to the renderer
// matching opts.JSON.
//
// Identity resolution is best-effort: when unset, CurrentAgent="" puts
// the bundle in the firehose path (privacy.IsVisible returns true for
// every record). This matches `recall`'s behavior — anonymous callers
// see the full corpus.
func runOpen(cmd *cobra.Command, cwd string, a openArgs, opts output.RenderOpts) error {
	// v1.0.4: --server routes open through the remote MCP tool. Server
	// resolves identity from the bearer token and applies privacy
	// floor before returning the bundle.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"subject": a.Subject,
			"topics":  a.Topics,
			"since":   a.Since,
			"scope":   a.Scope,
			"limit":   a.Limit,
		})
		return remoteCallAndRender("open", "open", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	// Identity best-effort: unresolved → CurrentAgent="" → firehose.
	currentAgent, _, _ := identity.Resolve(root)

	since := openDefaultSince
	if a.Since != "" {
		parsed, perr := time.ParseDuration(a.Since)
		if perr != nil || parsed <= 0 {
			return &rufioerr.UsageError{Message: fmt.Sprintf("invalid --since %q: must be a positive Go duration (e.g. 24h, 10m)", a.Since)}
		}
		since = parsed
	}
	scope := openDefaultScope
	if a.Scope != "" {
		if err := thought.ValidateScope(a.Scope); err != nil {
			return err
		}
		scope = a.Scope
	}
	limit := openDefaultLimit
	if a.Limit > 0 {
		limit = a.Limit
	}
	topics := splitCSVTrim(a.Topics)

	bundle, err := open.Bundle(root, open.Params{
		Subject:      a.Subject,
		Topics:       topics,
		Since:        since,
		Scope:        scope,
		Limit:        limit,
		CurrentAgent: currentAgent,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if opts.JSON {
		// Pass root so the path field renders root-relative
		// (security audit H2 + Bonus).
		return renderOpenJSON(out, bundle, root)
	}
	return renderOpenText(out, bundle, scope, opts)
}

// renderOpenText is the labelled-sections renderer. Order is locked at
// OPEN → DAEMON → FLEET → ATTENTION → RECALL → THOUGHTS (state first,
// activity second). Empty sections are OMITTED — the read-tax-reduction
// goal trumps a fixed-shape "(none)" row. If every activity section is
// empty, the renderer prints a single `(no activity for <subject>)`
// fallback line below the OPEN+DAEMON headers (those always render
// because they describe substrate state, not subject activity).
//
// Hidden-count footer ("(N private records hidden by privacy floor)")
// lands at the bottom — it's a render-fact about the output, not a
// substrate-fact about the subject.
func renderOpenText(w interface{ Write([]byte) (int, error) }, b open.OpenBundle, scope string, opts output.RenderOpts) error {
	// Stub-narrow render path lands in Task 9. Task 8 ships the
	// minimal "header + maybe-fallback" so the command runs end-to-end
	// without rendering the activity sections yet.
	fmt.Fprintf(w, "OPEN %s (agent=%s, scope=%s)\n", b.Subject, b.Agent, scope)
	renderDaemonLine(w, b)

	anyActivity := len(b.Fleet) > 0 || len(b.Attention) > 0 ||
		len(b.Recall) > 0 || len(b.Thoughts) > 0
	if !anyActivity {
		fmt.Fprintf(w, "(no activity for %s)\n", b.Subject)
	} else {
		renderFleetSection(w, b, opts)
		renderAttentionSection(w, b, opts)
		renderRecallSection(w, b, opts)
		renderThoughtsSection(w, b, opts)
	}

	if b.HiddenPrivateCount > 0 {
		noun := "records"
		if b.HiddenPrivateCount == 1 {
			noun = "record"
		}
		fmt.Fprintf(w, "(%d private %s hidden by privacy floor)\n", b.HiddenPrivateCount, noun)
	}
	return nil
}

// renderDaemonLine prints the one-line daemon advisory. Locked at:
//
//	DAEMON: running (heartbeat 4s ago)
//	DAEMON: STALE - last heartbeat 47s ago; routing may be delayed
//	DAEMON: not running (no heartbeat)
//
// Mirrors fleet's renderDaemonHealthHeader but is goal-shared output
// (not stderr chatter) — open's whole point is to bundle these reads,
// so the daemon line is part of the deliverable.
func renderDaemonLine(w interface{ Write([]byte) (int, error) }, b open.OpenBundle) {
	switch b.Daemon.State.String() {
	case "running":
		fmt.Fprintf(w, "DAEMON: running (heartbeat %s ago)\n", formatOpenAge(b.Daemon.LastTickAge))
	case "stale":
		fmt.Fprintf(w, "DAEMON: STALE - last heartbeat %s ago; routing may be delayed\n", formatOpenAge(b.Daemon.LastTickAge))
	default:
		fmt.Fprintf(w, "DAEMON: not running (no heartbeat)\n")
	}
}

// formatOpenAge is a small whole-second formatter — matches fleet's
// formatHeaderAge so the two surfaces report daemon age consistently.
func formatOpenAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Truncate(time.Second).String()
}

// renderFleetSection prints the FLEET header + one row per engaged peer.
// Omitted entirely when b.Fleet is empty (read-tax-reduction).
func renderFleetSection(w interface{ Write([]byte) (int, error) }, b open.OpenBundle, opts output.RenderOpts) {
	if len(b.Fleet) == 0 {
		return
	}
	fmt.Fprintln(w, "FLEET")
	now := time.Now()
	for _, r := range b.Fleet {
		intent := r.Intent
		if intent == "" {
			intent = "(no intent)"
		}
		intent = truncateOpen(intent, 80)
		fmt.Fprintf(w, "  %s\t%s\t%s\n",
			r.Agent,
			output.RenderRelTime(r.LastSeen, now),
			intent,
		)
	}
}

// renderAttentionSection prints the ATTENTION header + top-3 fleet
// agents' current @attention payload (intent + entities + topics).
// Omitted when b.Attention is empty.
func renderAttentionSection(w interface{ Write([]byte) (int, error) }, b open.OpenBundle, opts output.RenderOpts) {
	if len(b.Attention) == 0 {
		return
	}
	fmt.Fprintln(w, "ATTENTION")
	for _, a := range b.Attention {
		entities := strings.Join(a.Entities, ",")
		topics := strings.Join(a.Topics, ",")
		fmt.Fprintf(w, "  %s\tintent:%q\tentities:%s\ttopics:%s\n",
			a.Agent, truncateOpen(a.Intent, 80), entities, topics,
		)
	}
}

// renderRecallSection prints the RECALL header + thought+observation
// rows on subject. Each row carries id, type, author, relative-time,
// and a content snippet (truncated 80 cols). Sort: TS descending so the
// most recent activity surfaces first — matches recall.RenderColumnar.
func renderRecallSection(w interface{ Write([]byte) (int, error) }, b open.OpenBundle, opts output.RenderOpts) {
	renderRecallLike(w, "RECALL", b.Recall, opts)
}

// renderThoughtsSection prints the THOUGHTS header + all @thought
// records on subject. Same row shape as recall.
func renderThoughtsSection(w interface{ Write([]byte) (int, error) }, b open.OpenBundle, opts output.RenderOpts) {
	renderRecallLike(w, "THOUGHTS", b.Thoughts, opts)
}

// renderRecallLike is the shared row-renderer for the RECALL and
// THOUGHTS sections. Skips the header when records is empty so empty
// sections are OMITTED (read-tax-reduction rule).
func renderRecallLike(w interface{ Write([]byte) (int, error) }, header string, records []recall.RecallRecord, opts output.RenderOpts) {
	if len(records) == 0 {
		return
	}
	fmt.Fprintln(w, header)
	now := time.Now()
	sorted := make([]recall.RecallRecord, len(records))
	copy(sorted, records)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].TS > sorted[j].TS })
	for _, r := range sorted {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n",
			output.FormatID(r.ID),
			openTypeLabel(r),
			r.Author,
			output.RenderRelTime(r.TS, now),
			truncateOpen(r.Content, 80),
		)
	}
}

// openTypeLabel renders the type-column the same way recall's
// unifiedTypeLabel does — `thought:<subtype>` when a subtype exists,
// else bare type. Local copy avoids importing recall's private helper.
func openTypeLabel(r recall.RecallRecord) string {
	if r.Type == "thought" && r.ThoughtType != "" {
		return "thought:" + r.ThoughtType
	}
	return r.Type
}

// truncateOpen caps s at n chars, appending "..." when truncated.
// Mirrors recall.truncate (which is package-private to recall — local
// helper avoids the import-cycle risk).
func truncateOpen(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// renderOpenJSON emits the locked Task 10 JSON shape:
//
//	{
//	  "_type": "open",
//	  "_version": 1,
//	  "subject": "...",
//	  "agent": "...",
//	  "daemon": {"running": bool, "heartbeat": "RFC3339"},
//	  "fleet": [...], "attention": [...],
//	  "recall": [...], "thoughts": [...],
//	  "hidden_private_count": 0
//	}
//
// The payload-builder lives in internal/lib/open (open.JSONPayload) so
// the MCP `open` tool can emit a byte-identical shape — the
// fidelity-across-transports contract is enforced by sharing the helper,
// not by duplicating the map construction.
func renderOpenJSON(w interface{ Write([]byte) (int, error) }, b open.OpenBundle, root string) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(open.JSONPayload(b, root))
}
