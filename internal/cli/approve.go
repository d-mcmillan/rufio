package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewApproveCmd returns the `rufio approve <path>@<ver> [--as=<actor>]`
// Cobra command. Advances the target @ref to stage=staged and records
// approved-by. Default actor is the current identity; --as overrides.
func NewApproveCmd() *cobra.Command {
	var asFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "approve <path>@<ver>",
		Short: "Advance an @ref to stage=staged and record approved-by",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runApprove(cwd, args[0], asFlag, opts)
			}
			if err != nil {
				HandleError("approve", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&asFlag, "as", "", "actor performing the approval (default: current identity)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runApprove(cwd, pathArg, asFlag string, opts output.RenderOpts) error {
	contentPath, sel := versioning.ParsePathSelector(pathArg)
	if sel == nil || sel.Kind != versioning.SelectorVersion {
		return &rufioerr.UsageError{Message: "approve requires <path>@<version>: e.g. given/policy.md@v1"}
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	// Resolve actor: --as wins; else current identity.
	actor := asFlag
	if actor == "" {
		actor, _, err = identity.Resolve(root)
		if err != nil {
			return err
		}
	} else {
		if err := identity.Validate(actor); err != nil {
			return err
		}
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
	if source.Stage != versioning.StageDraft {
		return &rufioerr.InvalidStageTransitionError{
			Path: contentPath,
			From: string(source.Stage),
			To:   string(versioning.StageStaged),
		}
	}

	// Append new ref at staged.
	ts := versioning.NowISO()
	newRef, err := versioning.AppendRef(root, versioning.RefIntent{
		Path:       contentPath,
		SHA256:     source.SHA256,
		Stage:      versioning.StageStaged,
		Timestamp:  ts,
		Author:     source.Author,
		ApprovedBy: actor,
	})
	if err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":       "approve",
			"_version":    "1",
			"path":        contentPath,
			"version":     newRef.Version,
			"stage":       string(newRef.Stage),
			"sha256":      newRef.SHA256,
			"approved_by": actor,
			"ts":          ts,
		}
		return output.WriteJSONL(payload, opts)
	}
	output.WriteOut(
		fmt.Sprintf("approved: %s@v%d by=%s", contentPath, newRef.Version, actor),
		opts,
	)
	return nil
}
