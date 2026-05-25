package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/diff"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewDiffCmd returns the `rufio diff <path>@vN <path>@vM` Cobra command.
func NewDiffCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "diff <path>@v1 <path>@v2",
		Short: "Unified diff between two versions of the same path",
		// Cold agents kept inferring a `diff <path>@v1 @v2` shortcut
		// from the Short line and then guessing why their second
		// attempt failed. Be explicit: the two args MUST reference
		// the same path; pass it twice. Cross-path diffs are
		// rejected by runDiff (see UsageError below). Issue #123.
		Long: "Unified diff between two versions of the same path.\n\n" +
			"Both versions must reference the same path; pass it twice " +
			"(rufio diff given/policy.md@v1 given/policy.md@v2).",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runDiff(args[0], args[1], cwd, opts)
			}
			if err != nil {
				HandleError("diff", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit structured JSONL")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter (no-op — diff text is data)")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runDiff(left, right, cwd string, opts output.RenderOpts) error {
	leftPath, leftSel := versioning.ParsePathSelector(left)
	rightPath, rightSel := versioning.ParsePathSelector(right)
	if leftSel == nil {
		return &rufioerr.UsageError{Message: "left side requires an explicit version selector (@vN, @draft, @staged, @live)"}
	}
	if rightSel == nil {
		return &rufioerr.UsageError{Message: "right side requires an explicit version selector (@vN, @draft, @staged, @live)"}
	}
	if leftPath != rightPath {
		return &rufioerr.UsageError{Message: fmt.Sprintf("both arguments must reference the same path (got '%s' vs '%s')", leftPath, rightPath)}
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	contentPath, err := paths.ResolveContentPath(root, leftPath)
	if err != nil {
		return err
	}
	refs, err := versioning.ReadRefs(root, contentPath)
	if err != nil {
		return err
	}
	leftRef, err := versioning.LookupRefOrThrow(refs, *leftSel, contentPath)
	if err != nil {
		return err
	}
	rightRef, err := versioning.LookupRefOrThrow(refs, *rightSel, contentPath)
	if err != nil {
		return err
	}

	emitJSON := func(binary, identical bool, diffText string) error {
		return output.WriteJSONL(map[string]interface{}{
			"left":      refSummary(leftRef),
			"right":     refSummary(rightRef),
			"binary":    binary,
			"identical": identical,
			"diff":      diffText,
		}, opts)
	}

	if leftRef.SHA256 == rightRef.SHA256 {
		if opts.JSON {
			return emitJSON(false, true, "")
		}
		return nil
	}

	leftBlob, err := versioning.ReadBlob(root, leftRef.SHA256)
	if err != nil {
		return err
	}
	rightBlob, err := versioning.ReadBlob(root, rightRef.SHA256)
	if err != nil {
		return err
	}

	if diff.IsBinary(leftBlob) || diff.IsBinary(rightBlob) {
		msg := fmt.Sprintf("Binary files %s and %s differ", left, right)
		if opts.JSON {
			return emitJSON(true, false, msg)
		}
		output.WriteData(msg, opts)
		return nil
	}

	diffText, err := diff.UnifiedDiff(string(leftBlob), string(rightBlob), left, right)
	if err != nil {
		return err
	}
	if opts.JSON {
		return emitJSON(false, false, diffText)
	}
	if diffText != "" {
		output.WriteData(diffText, opts)
	}
	return nil
}

func refSummary(r versioning.RefRecord) map[string]interface{} {
	return map[string]interface{}{
		"path": r.Path, "version": r.Version, "sha256": r.SHA256,
		"stage": string(r.Stage), "ts": r.Timestamp,
	}
}
