package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewPushCmd returns the `rufio push <path> [--stage=...]` Cobra command.
// Wires content-addressed blob storage + append-only @ref records.
func NewPushCmd() *cobra.Command {
	var stageFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "push <path>",
		Short: "Commit a new version of a content path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runPush(args[0], stageFlag, cwd, opts)
			}
			if err != nil {
				HandleError("push", err)
			}
			return nil
		},
	}
	// Default to draft so a bare `rufio push <path>` lands a new
	// ref under draft and goes through the approve/promote
	// workflow. The previous live default silently bypassed
	// approve+promote and was the #1 cold-agent foot-gun in the
	// vet. Explicit `--stage=live` is still valid for the (rare)
	// hot-publish case. Issue #123.
	cmd.Flags().StringVar(&stageFlag, "stage", "draft", "stage for the new ref (draft|staged|live)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runPush(rawPath, stageStr, cwd string, opts output.RenderOpts) error {
	if !versioning.IsValidStage(stageStr) {
		return &rufioerr.UsageError{Message: fmt.Sprintf("unknown stage '%s' (valid: draft|staged|live)", stageStr)}
	}
	stage := versioning.Stage(stageStr)

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	contentPath, err := paths.ResolveContentPath(root, rawPath)
	if err != nil {
		return err
	}
	absolute := filepath.Join(root, filepath.FromSlash(contentPath))
	content, err := os.ReadFile(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("file not found at %s", absolute)
		}
		return err
	}

	sha, err := versioning.WriteBlob(root, content)
	if err != nil {
		return err
	}
	ts := versioning.NowISO()
	agent, _, _ := identity.Resolve(root)
	if agent == "" {
		agent = "unknown"
	}
	// Version is assigned INSIDE AppendRef's per-path lock — the TOCTOU-safe
	// path established in week-1 Phase 4 review I1 fix.
	ref, err := versioning.AppendRef(root, versioning.RefIntent{
		Path: contentPath, SHA256: sha, Stage: stage,
		Timestamp: ts, Author: agent,
	})
	if err != nil {
		return err
	}

	if opts.JSON {
		summary := map[string]interface{}{
			"path":    ref.Path,
			"version": ref.Version,
			"sha256":  ref.SHA256,
			"stage":   string(ref.Stage),
			"ts":      ref.Timestamp,
		}
		return output.WriteJSONL(summary, opts)
	}
	output.WriteOut(
		fmt.Sprintf("%s  push  %s@v%d  stage:%s  sha256:%s…",
			ref.Timestamp, ref.Path, ref.Version, ref.Stage, ref.SHA256[:12]),
		opts,
	)
	return nil
}
