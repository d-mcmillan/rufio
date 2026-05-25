package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewRollbackCmd returns the `rufio rollback <path>@<version>` Cobra command.
// Pure composition over AppendRef: read refs, find target version, append
// a new ref carrying target's sha256 + RolledBackFrom.
func NewRollbackCmd() *cobra.Command {
	var stageFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "rollback <path>@<version>",
		Short: "Append a new version pointing at an older blob (with explicit lineage)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runRollback(args[0], stageFlag, cwd, opts)
			}
			if err != nil {
				HandleError("rollback", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&stageFlag, "stage", "live", "stage for the rollback ref (draft|staged|live)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit structured JSONL")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runRollback(rawPath, stageStr, cwd string, opts output.RenderOpts) error {
	if !versioning.IsValidStage(stageStr) {
		return &rufioerr.UsageError{Message: fmt.Sprintf("unknown stage '%s' (valid: draft|staged|live)", stageStr)}
	}
	stage := versioning.Stage(stageStr)

	pathPart, sel := versioning.ParsePathSelector(rawPath)
	if sel == nil {
		return &rufioerr.UsageError{Message: fmt.Sprintf("rollback requires an explicit version selector (e.g. %s@v1)", pathPart)}
	}
	if sel.Kind != versioning.SelectorVersion {
		return &rufioerr.UsageError{Message: "rollback requires an explicit @vN version selector; stage selectors are not allowed"}
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	contentPath, err := paths.ResolveContentPath(root, pathPart)
	if err != nil {
		return err
	}
	refs, err := versioning.ReadRefs(root, contentPath)
	if err != nil {
		return err
	}
	target := versioning.RefByVersion(refs, sel.Version)
	if target == nil {
		return &rufioerr.NoSuchVersionError{Path: contentPath, Version: fmt.Sprintf("v%d", sel.Version)}
	}

	ts := versioning.NowISO()
	rolledBackFrom := target.Version
	newRef, err := versioning.AppendRef(root, versioning.RefIntent{
		Path: contentPath, SHA256: target.SHA256, Stage: stage,
		Timestamp: ts, Author: "unknown", RolledBackFrom: &rolledBackFrom,
	})
	if err != nil {
		return err
	}

	if opts.JSON {
		obj := map[string]interface{}{
			"path":           newRef.Path,
			"version":        newRef.Version,
			"sha256":         newRef.SHA256,
			"stage":          string(newRef.Stage),
			"ts":             newRef.Timestamp,
			"author":         newRef.Author,
			"rolledBackFrom": *newRef.RolledBackFrom,
		}
		return output.WriteJSONL(obj, opts)
	}
	output.WriteOut(
		fmt.Sprintf("%s  rollback  %s@v%d  stage:%s  sha256:%s…  rolled-back-from:v%d",
			newRef.Timestamp, newRef.Path, newRef.Version, newRef.Stage,
			newRef.SHA256[:12], target.Version),
		opts,
	)
	return nil
}
