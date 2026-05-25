package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewPullCmd returns the `rufio pull <path>[@vN|@stage]` Cobra command.
func NewPullCmd() *cobra.Command {
	var stageFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "pull <path>",
		Short: "Fetch a versioned blob and write to stdout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runPull(args[0], stageFlag, cwd, opts)
			}
			if err != nil {
				HandleError("pull", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&stageFlag, "stage", "", "stage selector override (draft|staged|live)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output (with base64 content)")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter (no-op for pull — blob is data)")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runPull(rawPath, stageOverride, cwd string, opts output.RenderOpts) error {
	// Selector resolution priority:
	//   1. Explicit @vN or @stage suffix on the path argument
	//   2. --stage flag
	//   3. Default: latest live
	pathPart, sel := versioning.ParsePathSelector(rawPath)

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

	var effective versioning.VersionSelector
	switch {
	case sel != nil:
		effective = *sel
	case stageOverride != "":
		if !versioning.IsValidStage(stageOverride) {
			return &rufioerr.UsageError{Message: fmt.Sprintf("unknown stage '%s' (valid: draft|staged|live)", stageOverride)}
		}
		effective = versioning.VersionSelector{Kind: versioning.SelectorStage, Stage: versioning.Stage(stageOverride)}
	default:
		effective = versioning.VersionSelector{Kind: versioning.SelectorStage, Stage: versioning.StageLive}
	}

	if len(refs) == 0 {
		return &rufioerr.NoSuchVersionError{Path: contentPath, Version: selectorLabel(effective)}
	}
	ref, err := versioning.LookupRefOrThrow(refs, effective, contentPath)
	if err != nil {
		return err
	}
	blob, err := versioning.ReadBlob(root, ref.SHA256)
	if err != nil {
		return err
	}

	if opts.JSON {
		summary := map[string]interface{}{
			"path":          ref.Path,
			"version":       ref.Version,
			"stage":         string(ref.Stage),
			"sha256":        ref.SHA256,
			"ts":            ref.Timestamp,
			"contentBase64": base64.StdEncoding.EncodeToString(blob),
		}
		bs, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		os.Stdout.Write(bs)
		os.Stdout.Write([]byte("\n"))
		return nil
	}
	// Default: write blob bytes directly to stdout (binary-safe).
	_, err = os.Stdout.Write(blob)
	return err
}

func selectorLabel(s versioning.VersionSelector) string {
	if s.Kind == versioning.SelectorVersion {
		return "v" + strconv.Itoa(s.Version)
	}
	return "stage=" + string(s.Stage)
}
