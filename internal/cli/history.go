package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewHistoryCmd returns the `rufio history <path>` Cobra command.
func NewHistoryCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "history <path>",
		Short: "Print every @ref for a path, latest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runHistory(args[0], cwd, opts)
			}
			if err != nil {
				HandleError("history", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output (one ref per line)")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter (no-op — rows are data)")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runHistory(rawPath, cwd string, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	contentPath, err := paths.ResolveContentPath(root, rawPath)
	if err != nil {
		return err
	}
	refs, err := versioning.ReadRefs(root, contentPath)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return fmt.Errorf("no refs for '%s'", contentPath)
	}
	// Latest first.
	sort.Slice(refs, func(i, j int) bool { return refs[i].Version > refs[j].Version })

	if opts.JSON {
		for _, r := range refs {
			obj := map[string]interface{}{
				"path": r.Path, "version": r.Version, "sha256": r.SHA256,
				"stage": string(r.Stage), "ts": r.Timestamp, "author": r.Author,
			}
			if r.RolledBackFrom != nil {
				obj["rolledBackFrom"] = *r.RolledBackFrom
			}
			if err := output.WriteJSONL(obj, opts); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range refs {
		trailer := ""
		if r.RolledBackFrom != nil {
			trailer = fmt.Sprintf("  rolled-back-from:v%d", *r.RolledBackFrom)
		}
		// Rows ARE data — use WriteData so --quiet doesn't suppress them.
		output.WriteData(
			fmt.Sprintf("%s  v%d  stage:%s  sha256:%s…  author:%s%s",
				r.Timestamp, r.Version, r.Stage, r.SHA256[:12], r.Author, trailer),
			opts,
		)
	}
	return nil
}
