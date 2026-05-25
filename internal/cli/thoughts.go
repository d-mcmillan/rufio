// Package cli — `rufio thoughts list`.
//
// Inspection command per D20.3 + D20.4. Walks live/outbox/*/*.gdl AND
// live/inbox/*/*.gdl, parses every @thought record, deduplicates by id
// (outbox copy preferred — inbox files are routing duplicates), filters
// by an optional --since duration, and prints rows sorted descending
// by ts.
//
// Parent `thoughts` is help-only; the actionable verb is `thoughts list`
// (matches the `summons list` shape). v1 ships only the `list` subcommand
// per D20.6.
//
// Visibility: `thoughts list` ALWAYS surfaces retracted thoughts
// inline, prefixed with [RETRACTED] in text mode and carrying
// retracted_at/by/reason fields in JSON mode (#141). This is the
// focused author-audit view — retract is signal here, not noise.
// TTL-expired thoughts remain hidden by default and are surfaced
// under --include-expired (#151).
package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
	"github.com/d-mcmillan/rufio/internal/lib/recall"
	"github.com/d-mcmillan/rufio/internal/lib/retract"
)

// thoughtContentTruncate caps the content column in columnar output.
// Longer payloads render as `"<first N chars>..."` so a single row
// stays readable in a 120-col terminal. Matches summons/recall.
const thoughtContentTruncate = 80

// NewThoughtsCmd returns the `rufio thoughts` parent Cobra command.
//
// H3c (#125): bare `rufio thoughts` (no subcommand) now ALIASES to the
// `list` subcommand instead of printing help + exit 1 (the pre-H3c
// Cobra default for a parent without RunE). `rufio fleet` already runs
// without an explicit list subcommand; this aligns thoughts/summons/
// goals/channels with that shape so cold agents can predict the
// noun-verb behaviour uniformly.
//
// --help still works — Cobra short-circuits to its help handler on the
// --help flag before RunE fires; the help-flag regression is guarded by
// TestThoughts_HelpFlagStillWorks.
func NewThoughtsCmd() *cobra.Command {
	listCmd := newThoughtsListCmd()
	cmd := &cobra.Command{
		Use:   "thoughts",
		Short: "Inspect recent thoughts across outbox and inbox",
		// Args: any — pass-through to list so flags on the bare invocation
		// flow to the list subcommand. Subcommands (today: only `list`)
		// still dispatch via cobra's positional arg routing.
		RunE: listCmd.RunE,
	}
	// Re-register the list subcommand's flags on the parent so bare-verb
	// invocations like `rufio thoughts --since=10m --json` parse the same
	// way `rufio thoughts list --since=10m --json` does.
	cmd.Flags().AddFlagSet(listCmd.Flags())
	cmd.AddCommand(listCmd)
	return cmd
}

// newThoughtsListCmd returns the `rufio thoughts list` Cobra
// subcommand. Reads outbox + inbox, dedups by id, optionally filters by
// --since=<duration>, sorts desc by ts.
//
// #141: retracted thoughts ALWAYS surface inline with [RETRACTED] (text)
// and retracted_at/by/reason (JSON). This is the focused author-audit
// view — retract is signal here, not noise. #151: --include-expired
// surfaces TTL-expired thoughts (still hidden by default).
//
// --all-agents is a no-op today (`thoughts list` already walks all
// outboxes + inboxes); it documents the fleet-visibility intent and
// reserves the flag for a future per-author-default change.
//
//	rufio thoughts list [--since=<duration>] [--include-expired] [--all-agents] [--json]
func newThoughtsListCmd() *cobra.Command {
	var sinceFlag string
	var jsonFlag, quietFlag, noColorFlag, includeExpiredFlag, allAgentsFlag bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent thoughts across outbox + inbox (deduped, desc by ts)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runThoughtsList(cwd, sinceFlag, includeExpiredFlag, allAgentsFlag, opts)
			}
			if err != nil {
				HandleError("thoughts", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sinceFlag, "since", "", "filter to thoughts newer than this Go duration (e.g. 10m, 24h)")
	cmd.Flags().BoolVar(&includeExpiredFlag, "include-expired", false, "also surface TTL-expired thoughts (retracted thoughts are always shown with a [RETRACTED] marker)")
	cmd.Flags().BoolVar(&allAgentsFlag, "all-agents", false, "fleet visibility — show thoughts authored by any agent (current default)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	// #141: retracted thoughts ALWAYS surface here (inline [RETRACTED]
	// marker + retracted_at/by/reason). Scoped divergence from `recall`,
	// which keeps retracted out of its broad-corpus default — retract is
	// noise at the corpus layer, signal at the per-author audit layer.
	// #151: --include-expired covers TTL-expired thoughts only.
	return cmd
}

// thoughtRow is the parsed projection of a single @thought record
// surfaced to the renderer. Fields mirror the on-disk @thought shape
// (D5.1) plus Path for diagnostic JSON output.
//
// #151: TTLSeconds is the parsed integer-seconds expiry used by the
// visibility predicate. TTL (the string field) is kept for the JSON
// renderer's stable shape — externally the wire format is unchanged.
type thoughtRow struct {
	ID         string
	Author     string
	Type       string
	Subject    string
	Content    string
	Scope      string
	Topics     []string
	TS         string
	TTL        string
	TTLSeconds int
	Parent     string
	Path       string
	Retracted  bool
}

// thoughtRecord is the one-line adapter that lets thoughtRow flow
// through the shared privacy.IsVisible predicate. Mirrors the
// goal.Goal / observation / attention adapters added under #147 —
// each lister wraps its own struct so the privacy package stays
// duck-typed via the tiny Record interface.
type thoughtRecord struct{ r thoughtRow }

func (t thoughtRecord) GetScope() string  { return t.r.Scope }
func (t thoughtRecord) GetAuthor() string { return t.r.Author }

// runThoughtsList is the pure logic for `rufio thoughts list`. Walks
// outbox + inbox (outbox first so its copy wins the dedup), filters by
// --since, applies the visibility predicate (retract = always surface,
// TTL = hide unless --include-expired), sorts desc, and dispatches to
// the renderer.
//
// #141: retracted thoughts ALWAYS surface inline with [RETRACTED]
// (text) + retracted_at/by/reason (JSON). #151: TTL-expired rows are
// hidden by default; --include-expired surfaces them.
//
// --all-agents is currently a no-op (the walk has always been fleet-
// wide). The flag exists so the visibility-scope contract is explicit
// in the CLI surface; a future change can flip the default to own-only
// without breaking callers that opted in.
//
// Privacy (#147 floor — v1.0.6 pre-tag audit MAJOR): identity is
// resolved best-effort; an identified caller never sees scope:agent
// thoughts authored by another agent. An unidentified caller falls
// through to the firehose path (privacy.IsVisible returns true on
// empty currentAgent), matching the recall and goals list behaviour.
// scanThoughts walks every agent's outbox/<agent>/ directory, so
// without this gate the substrate leaked subject + content + retract
// reason between agents — see the v1.0.6 audit + the regression
// guards in test/integration/privacy_cross_surface_test.go.
func runThoughtsList(cwd, sinceRaw string, includeExpired, allAgents bool, opts output.RenderOpts) error {
	_ = allAgents // currently the only mode; reserved for future scoping
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	since, err := recall.ParseSince(sinceRaw)
	if err != nil {
		return err
	}

	// Identity is best-effort. An unidentified caller (no env, no local
	// file) gets the firehose path via privacy.IsVisible — the empty
	// currentAgent returns true unconditionally, preserving admin and
	// pre-#147 contract. Mirrors goals.runGoalsList:137.
	currentAgent, _, _ := identity.Resolve(root)

	// Outbox first → its copy wins the dedup keyed on thought id.
	rows := make([]thoughtRow, 0)
	seen := make(map[string]bool)
	for _, sub := range []string{"outbox", "inbox"} {
		got, err := scanThoughts(root, sub, seen)
		if err != nil {
			return err
		}
		rows = append(rows, got...)
	}

	// Privacy gate (#147 floor). Runs FIRST — before retracted-mark /
	// TTL / --since — so the gate is the same regardless of any
	// downstream user-facing filter (and so we don't waste cycles
	// loading retract/confirm/promote joins for rows the caller can't
	// see). scope=agent rows authored by a different agent are dropped
	// for an identified caller; anonymous callers still see everything
	// (firehose), matching recall and goals list.
	if currentAgent != "" {
		kept := rows[:0]
		for _, r := range rows {
			if !privacy.IsVisible(thoughtRecord{r}, currentAgent) {
				continue
			}
			kept = append(kept, r)
		}
		rows = kept
	}

	// #141 — retract visibility. `thoughts list` is the focused author/
	// fleet audit view: retracted rows MUST surface inline with a
	// [RETRACTED] marker so operators see at a glance which thoughts
	// have been withdrawn. This is the deliberate divergence from
	// `recall`, which keeps retracted records out of the default corpus
	// view (retract is noise at the broad-recall layer; retract is
	// signal at the per-thoughts layer).
	//
	// #151 — TTL visibility. TTL-expired rows STAY hidden by default
	// (TTL = "meant to evaporate"; surfacing them is opt-in via
	// --include-expired). When --include-expired is set, BOTH retracted
	// rows (already shown) and TTL-expired rows surface.
	retracted, err := loadRetractedIDs(root)
	if err != nil {
		return err
	}
	// Mark every row's Retracted bit so downstream renderers can branch
	// on it. This runs in both default and --include-expired modes;
	// retracted rows are never dropped from the slice.
	for i := range rows {
		if retracted[rows[i].ID] {
			rows[i].Retracted = true
		}
	}
	if !includeExpired {
		now := time.Now()
		kept := rows[:0]
		for _, r := range rows {
			// Retracted rows are KEPT (with the marker the renderer
			// applies). Only TTL-expired non-retracted rows are
			// filtered out at the default visibility level.
			if r.Retracted {
				kept = append(kept, r)
				continue
			}
			rec := recall.RecallRecord{
				Type: "thought",
				TS:   r.TS,
				TTL:  r.TTLSeconds,
			}
			if recall.IsExpired(rec, now) {
				continue
			}
			kept = append(kept, r)
		}
		rows = kept
	}

	// --since filter (cheap; applied AFTER scan so dedup still works).
	if since > 0 {
		floor := time.Now().Add(-since)
		filtered := rows[:0]
		for _, r := range rows {
			ts, perr := time.Parse(time.RFC3339Nano, r.TS)
			if perr != nil {
				// Malformed ts — skip silently per D20.4 (the renderer
				// can't usefully present an unsortable row anyway).
				continue
			}
			if ts.After(floor) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	// Sort desc by ts (lexicographic on RFC3339Nano IS chronological for
	// any single TZ — the writer pins UTC via versioning.NowISO). For
	// rows whose ts fails to parse we fall back to lexicographic compare
	// so output is still deterministic.
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].TS > rows[j].TS
	})

	// #141: join retract state per-thought for every row. Retracted
	// rows always reach this point in both default and
	// --include-expired modes; the marker decoration is driven from
	// retractByID below regardless of the flag. Read errors degrade
	// to "no retract" rather than aborting the listing.
	retractByID := make(map[string]retract.Record, len(rows))
	for _, r := range rows {
		rec, rerr := retract.ReadByTarget(root, r.ID)
		if rerr != nil {
			rec = retract.Record{}
		}
		retractByID[r.ID] = rec
	}

	// H2 — state-join markers. R24: agents need to see confirms/refutes/
	// promotion inline on every listed row instead of running a 6-command
	// scavenger hunt (lineage → confirms → refutes → retracts) per id.
	// Tallies degrade to zero on read errors; the promote join degrades
	// to "not promoted" — both fail-soft for the same reason as retract:
	// a noisy partial listing is strictly worse than a clean one.
	tallyByID := make(map[string]confirm.Tally, len(rows))
	promoteByID := make(map[string]promoteRecord, len(rows))
	for _, r := range rows {
		t, terr := confirm.ReadAll(root, r.ID)
		if terr != nil {
			t = confirm.Tally{}
		}
		tallyByID[r.ID] = t
		promoteByID[r.ID] = readPromoteMarker(root, r.ID)
	}

	if opts.JSON {
		return renderThoughtsJSON(rows, root, retractByID, tallyByID, promoteByID, opts)
	}
	renderThoughtsColumnar(rows, retractByID, tallyByID, promoteByID, opts)
	return nil
}

// promoteRecord is the projection of the @auto-promote audit record at
// live/promoted/<id>.gdl. Present=false when no marker exists OR when the
// marker is a @promote-skipped record (skipped promotions are NOT a
// "[PROMOTED]" state from the listing's perspective; the thought never
// crossed the threshold into the durable learned/ corpus, so we don't
// mark it as promoted).
type promoteRecord struct {
	Present       bool
	TS            string
	By            string
	ObservationID string
}

// readPromoteMarker parses live/promoted/<id>.gdl and returns the
// @auto-promote audit shape. Missing file → zero value, no error.
// Malformed file → zero value, no error (matches the fail-soft posture
// of retract.ReadByTarget — a noisy half-listing helps no one).
//
// @auto-promote records carry: thought:<id>|observation:<obs-id>|by:auto-promote|ts:<iso>
// @promote-skipped records carry: target:<id>|reason:<reason>|by:auto-promote|ts:<iso>
// We surface ONLY @auto-promote — a @promote-skipped marker means the
// thought never reached learned/, so [PROMOTED] would mislead.
func readPromoteMarker(root, targetID string) promoteRecord {
	path := filepath.Join(root, "live", "promoted", targetID+".gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		return promoteRecord{}
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return promoteRecord{}
	}
	for _, r := range records {
		if r.Type != "auto-promote" {
			continue
		}
		return promoteRecord{
			Present:       true,
			TS:            r.Get("ts"),
			By:            r.Get("by"),
			ObservationID: r.Get("observation"),
		}
	}
	return promoteRecord{}
}

// loadRetractedIDs walks live/retracted/*.gdl and returns the set of
// target ids. Missing dir → empty set, nil error. Used by
// runThoughtsList (#151) to apply the shared retract-visibility
// predicate — recall.Scan does the same thing via scanRetracted, and
// we deliberately re-walk here rather than importing the unexported
// helper so the cli package stays at arm's length from internal recall
// state.
func loadRetractedIDs(root string) (map[string]bool, error) {
	dir := filepath.Join(root, "live", "retracted")
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	out := make(map[string]bool)
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".gdl" {
			return nil
		}
		bs, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		records, err := gdl.ParseDocument(string(bs))
		if err != nil {
			return err
		}
		for _, r := range records {
			if r.Type != "retract" {
				continue
			}
			if target := r.Get("target"); target != "" {
				out[target] = true
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// scanThoughts walks live/<sub>/*/*.gdl (where sub is "outbox" or
// "inbox"), parses each file, and projects every @thought record into
// a thoughtRow. Records whose id is already in seen are skipped — this
// is the dedup mechanism that lets outbox copies win.
//
// Missing live/<sub>/ directory → empty slice, nil error. Read/parse
// errors propagate; individual files with no @thought records are
// silently skipped (matches recall.scanOutbox).
func scanThoughts(root, sub string, seen map[string]bool) ([]thoughtRow, error) {
	dir := filepath.Join(root, "live", sub)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	out := make([]thoughtRow, 0)
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".gdl" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		records, err := gdl.ParseDocument(string(data))
		if err != nil {
			return err
		}
		for _, r := range records {
			if r.Type != "thought" {
				continue
			}
			id := r.Get("id")
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			row := thoughtRow{
				ID:      id,
				Author:  r.Get("author"),
				Type:    r.Get("type"),
				Subject: r.Get("subject"),
				Content: r.Get("content"),
				Scope:   r.Get("scope"),
				TS:      r.Get("ts"),
				TTL:     r.Get("ttl"),
				Parent:  r.Get("parent"),
				Path:    path,
			}
			// #151 — parse ttl into TTLSeconds for the shared visibility
			// predicate. Mirror recall.scanOutbox semantics: 0 / unparseable
			// / negative all map to 0 (never expires).
			if v := r.Get("ttl"); v != "" {
				if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
					row.TTLSeconds = n
				}
			}
			if v := r.Get("topics"); v != "" {
				row.Topics = strings.Split(v, ",")
			}
			out = append(out, row)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// renderThoughtsColumnar prints one tab-separated line per thought.
// Empty input produces zero output. H1b reshapes the leading two
// columns: text mode emits a relative-time phrase (output.RenderRelTime,
// "12m ago" / "2d ago" / "2026-05-12") and a short id (output.FormatID,
// the 6-char suffix). The wire-format JSON renderer keeps the
// RFC3339Nano + full id intact.
//
//	<reltime>\t<short-id>\tauthor:<author>\ttype:<type>\tsubject:<subject>\tcontent:"<truncated>"
//
// Content is double-quoted and truncated at 80 chars per D20.4.
//
// #141: retracted rows are prefixed with `[RETRACTED] ` and gain a
// trailing `\tretract_reason:"<reason>"` column. The retract reason is
// load-bearing for cold agents reading the listing — surfaces the
// author's stated rationale at a glance.
//
// H1a: the [RETRACTED] tag is rendered in red on a colour-capable tty
// (never on piped output). The short id is rendered cyan so it pops
// against the dim relative-time + author columns.
//
// H2 / R24: each row gains a compact `+N/-M` social-validation column
// (omitted segments suppressed when both counts are zero) and a
// `[PROMOTED]` token when live/promoted/<id>.gdl carries an
// @auto-promote audit. The badge lands AFTER subject/content so a row
// stays parseable — closes the 6-command scavenger-hunt gap from R24.
func renderThoughtsColumnar(rows []thoughtRow, retracts map[string]retract.Record, tallies map[string]confirm.Tally, promotes map[string]promoteRecord, opts output.RenderOpts) {
	now := time.Now()
	for _, r := range rows {
		reltime := output.Dim(output.RenderRelTime(r.TS, now), opts)
		id := output.Cyan(output.FormatID(r.ID), opts)
		line := fmt.Sprintf(
			"%s\t%s\tauthor:%s\ttype:%s\tsubject:%s\tcontent:%s",
			reltime, id, r.Author, r.Type, r.Subject,
			truncateQuoted(r.Content, thoughtContentTruncate),
		)
		// H2: state-join badge — confirms/refutes counts plus a
		// [PROMOTED] tag when applicable. The badge is appended as a
		// new tab-delimited field so existing column parsers (the
		// labelled author:/type:/subject:/content: format already keeps
		// renames safe) keep working — they just see one more field.
		if badge := stateBadge(tallies[r.ID], promotes[r.ID]); badge != "" {
			line = line + "\t" + badge
		}
		if rec, ok := retracts[r.ID]; ok && rec.Present {
			line = output.BoldState("[RETRACTED]", opts) + " " + line + fmt.Sprintf("\tretract_reason:%q", rec.Reason)
		}
		output.WriteData(line, opts)
	}
}

// stateBadge renders the inline H2 social-state token: `+N` (confirms),
// `-M` (refutes), `[PROMOTED]` (auto-promote marker present). Empty
// segments are suppressed so a virgin thought row stays clean — only
// the parts with signal land. Returns "" when there's nothing to say,
// which lets the caller omit the trailing tab entirely.
//
// Notably we DO NOT inject [RETRACTED] here — H1/H2 boundary discipline:
// retract has its own dedicated column rendering with reason: text, set
// upstream by callers. This helper owns only confirm/refute/promoted.
func stateBadge(t confirm.Tally, p promoteRecord) string {
	c := len(t.Confirms)
	r := len(t.Refutes)
	var segs []string
	if c > 0 {
		segs = append(segs, fmt.Sprintf("+%d", c))
	}
	if r > 0 {
		segs = append(segs, fmt.Sprintf("-%d", r))
	}
	if p.Present {
		segs = append(segs, "[PROMOTED]")
	}
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, " ")
}

// renderThoughtsJSON emits one JSONL object per thought. Fields
// locked at: _type, _version, id, author, type, subject, content,
// scope, topics, ts, ttl, parent, path, confirmed_by, refuted_by.
// Optional parent is always rendered (nil when absent) so the JSON
// shape is stable.
//
// #132: every row carries confirmed_by + refuted_by arrays (always
// present, possibly []) so consumers can iterate without nil-checks.
// Read errors per-thought are best-effort — a parse failure on the
// confirms file degrades to empty arrays rather than aborting the
// whole listing.
//
// H2: every row also carries promoted_at, promoted_by,
// promoted_observation — present-but-null when no @auto-promote
// marker exists, populated when one does. Mirrors the #141
// retracted_*/at/by/reason contract: stable keys, no nil-vs-missing
// ambiguity for downstream consumers.
func renderThoughtsJSON(rows []thoughtRow, root string, retracts map[string]retract.Record, tallies map[string]confirm.Tally, promotes map[string]promoteRecord, opts output.RenderOpts) error {
	_ = tallies // tally counts surface in confirmed_by/refuted_by arrays already
	for _, r := range rows {
		topics := r.Topics
		if topics == nil {
			topics = []string{}
		}
		socials, _ := confirm.ReadRecords(root, r.ID)
		confirms, refutes := splitSocials(socials)
		payload := map[string]interface{}{
			"_type":    "thought",
			"_version": "1",
			"id":       r.ID,
			"author":   r.Author,
			"type":     r.Type,
			"subject":  r.Subject,
			"content":  r.Content,
			"scope":    r.Scope,
			"topics":   topics,
			"ts":       r.TS,
			"ttl":      r.TTL,
			// Security audit H2 + Bonus: emit root-relative POSIX
			// path instead of the absolute server filesystem path.
			// Root is threaded through so the relativiser uses
			// filepath.Rel (no substring-match drift on roots
			// containing "live"/"given"/"learned" as components).
			"path":         recall.RelativisePath(r.Path, root),
			"confirmed_by": socialsToJSON(confirms),
			"refuted_by":   socialsToJSON(refutes),
		}
		if r.Parent != "" {
			payload["parent"] = r.Parent
		} else {
			payload["parent"] = nil
		}
		// #141: retracted_at/by/reason are ALWAYS present (possibly
		// null) so JSON consumers don't have to handle both missing
		// and null. Mirrors the confirmed_by/refuted_by stability
		// contract from #132 — read-side keys never disappear.
		rec, ok := retracts[r.ID]
		if ok && rec.Present {
			payload["retracted_at"] = rec.TS
			payload["retracted_by"] = rec.By
			payload["retract_reason"] = rec.Reason
		} else {
			payload["retracted_at"] = nil
			payload["retracted_by"] = nil
			payload["retract_reason"] = nil
		}
		// H2: promotion state — present-but-null contract per #132.
		// Drives the [PROMOTED] text marker; here we surface the full
		// audit shape (ts/by/observation-id) so JSON consumers can
		// drill in without re-reading live/promoted/.
		if pr, ok := promotes[r.ID]; ok && pr.Present {
			payload["promoted_at"] = pr.TS
			payload["promoted_by"] = pr.By
			payload["promoted_observation"] = pr.ObservationID
		} else {
			payload["promoted_at"] = nil
			payload["promoted_by"] = nil
			payload["promoted_observation"] = nil
		}
		if err := output.WriteJSONL(payload, opts); err != nil {
			return err
		}
	}
	return nil
}

// truncateQuoted wraps s in double quotes and truncates the inner
// content (not the quotes) to n. Longer values surface as
// `"<first n chars>..."`. Matches the summons quoteAndTruncate shape.
func truncateQuoted(s string, n int) string {
	if len(s) <= n {
		return `"` + s + `"`
	}
	return `"` + s[:n] + `..."`
}
