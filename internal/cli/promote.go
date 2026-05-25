package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/retract"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewPromoteCmd returns the `rufio promote <path>@<ver> [--to=<stage>]`
// Cobra command. Advances the target @ref to stage=live and records
// promoted-from. v1 only allows --to=live (use approve to reach staged).
func NewPromoteCmd() *cobra.Command {
	var toFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "promote <path>@<ver>",
		Short: "Advance an @ref to stage=live and record promoted-from",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runPromote(cwd, args[0], toFlag, opts)
			}
			if err != nil {
				HandleError("promote", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&toFlag, "to", "live", "target stage (v1 locks this to 'live'; use approve for staged)")
	// Hide --to in v1 — the only legal value is "live" and the runtime
	// rejects everything else. Surfacing it in --help baited cold
	// agents into trying `--to=staged` (issue #123). Keep the flag
	// definition so v2 can flip Hidden:false when staged promotion
	// lands without a back-compat break.
	_ = cmd.Flags().MarkHidden("to")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runPromote(cwd, pathArg, toFlag string, opts output.RenderOpts) error {
	// v1: --to must be "live" (any other value, including "staged", is rejected).
	target := versioning.StageLive
	if toFlag != "" && toFlag != string(target) {
		return &rufioerr.UsageError{
			Message: fmt.Sprintf("invalid --to %q: only 'live' is allowed in v1 (use approve to reach staged)", toFlag),
		}
	}

	contentPath, sel := versioning.ParsePathSelector(pathArg)
	if sel == nil || sel.Kind != versioning.SelectorVersion {
		// R29b: before emitting the artifact-path-shaped UsageError, check
		// whether the caller passed a value that LOOKS like a thought-id.
		// Two flavours surface the nudge: the full canonical
		// <unix-millis>-<rand6> shape, or a 6-char [a-z0-9]{6} suffix
		// that resolves to a thought via the R29a resolver. In either
		// case the agent has reached for `promote` when they meant
		// `confirm` — manual promotion of a thought isn't a thing
		// (auto-promotion to learned/ fires via quorum confirms).
		if msg, ok := maybeThoughtIDNudge(cwd, pathArg); ok {
			return &rufioerr.UsageError{Message: msg}
		}
		return &rufioerr.UsageError{Message: "promote requires <path>@<version>: e.g. given/policy.md@v1"}
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	// Resolve identity (proof of authorship; not stamped onto the ref but
	// validates we're inside a project with a known agent).
	if _, _, err := identity.Resolve(root); err != nil {
		return err
	}

	// Load and validate the source ref.
	refs, err := versioning.ReadRefs(root, contentPath)
	if err != nil {
		return err
	}
	source := versioning.RefByVersion(refs, sel.Version)
	if source == nil {
		return &rufioerr.NoSuchVersionError{Path: contentPath, Version: "v" + strconv.Itoa(sel.Version)}
	}
	if source.Stage == target {
		return &rufioerr.InvalidStageTransitionError{
			Path: contentPath,
			From: string(source.Stage),
			To:   string(target),
		}
	}

	// Append new ref at live.
	ts := versioning.NowISO()
	newRef, err := versioning.AppendRef(root, versioning.RefIntent{
		Path:         contentPath,
		SHA256:       source.SHA256,
		Stage:        target,
		Timestamp:    ts,
		Author:       source.Author,
		PromotedFrom: string(source.Stage),
	})
	if err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":         "promote",
			"_version":      "1",
			"path":          contentPath,
			"version":       newRef.Version,
			"stage":         string(newRef.Stage),
			"sha256":        newRef.SHA256,
			"promoted_from": string(source.Stage),
			"ts":            ts,
		}
		return output.WriteJSONL(payload, opts)
	}
	output.WriteOut(
		fmt.Sprintf("promoted: %s@v%d to=%s from=v%d", contentPath, newRef.Version, target, source.Version),
		opts,
	)
	return nil
}

// maybeThoughtIDNudge inspects pathArg for the two thought-id shapes
// the agent might pass when they meant `confirm` instead of `promote`:
//
//   - Full canonical <unix-millis>-<rand6> form — match by shape alone;
//     no I/O needed.
//   - 6-char [a-z0-9]{6} suffix that resolves to an on-disk thought via
//     retract.Resolve — requires a project-root lookup. Resolution
//     surfaces *AmbiguousIDError as a thought-id hit too (the agent's
//     suffix matched multiple thoughts).
//
// On a positive match, returns the redirect text and ok=true; the
// caller wraps it in a UsageError so the user-visible envelope stays
// `rufio promote: <msg>` (consistent with every other UsageError).
// Empty / artifact-shaped / no-match returns ok=false and the caller
// emits the original `promote requires <path>@<version>` error.
//
// Project-root lookup failures (no rufio.gdl, identity errors) degrade
// to ok=false silently — promote's own runPromote will surface the same
// error a moment later through the normal path. We don't want the
// nudge to MASK a legitimate "not in a project" error.
func maybeThoughtIDNudge(cwd, pathArg string) (string, bool) {
	if retract.LooksLikeThoughtID(pathArg) {
		return thoughtIDNudgeMessage(pathArg), true
	}
	if retract.LooksLikeShortID(pathArg) {
		root, err := paths.FindProjectRoot(cwd)
		if err != nil {
			return "", false
		}
		// Anonymous resolve — the nudge text is the same regardless of
		// caller identity, and the rejection message must not change
		// based on who's asking (no privacy leak vector here because
		// we never echo the resolved id).
		if _, rerr := retract.Resolve(root, pathArg, ""); rerr == nil {
			return thoughtIDNudgeMessage(pathArg), true
		} else {
			// AmbiguousIDError still counts as a thought-shaped hit —
			// the agent's suffix resolved to ≥2 thoughts, definitively
			// not an artifact path.
			var ambig *rufioerr.AmbiguousIDError
			if errors.As(rerr, &ambig) {
				return thoughtIDNudgeMessage(pathArg), true
			}
		}
	}
	return "", false
}

// thoughtIDNudgeMessage is the canonical redirect text emitted when
// `rufio promote <thought-id>` fires. Names `confirm` and the quorum
// auto-promote semantic so the agent can self-correct without
// re-querying. References primer for the threshold details — promote
// nudge isn't the right place to hardcode N (config-driven in v1.1).
func thoughtIDNudgeMessage(id string) string {
	return fmt.Sprintf(
		"%s looks like a thought-id, not an artifact path. "+
			"To promote a thought to observation status, use `rufio confirm %s` — "+
			"when N agents confirm, auto-promote fires automatically "+
			"(see `rufio primer` for the quorum threshold). "+
			"For artifact promotion, pass a path under given/ or a stage-ref (e.g. given/policy.md@v1).",
		id, id,
	)
}
