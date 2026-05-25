package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"

	"github.com/d-mcmillan/rufio/internal/banner"
)

// starterDirs is the canonical project directory tree. Mirrors src/commands/init.ts.
var starterDirs = []string{
	"given",
	"learned",
	"live",
	"live/outbox",
	"live/inbox",
	"live/attention",
	"live/reasoning",
	"live/retracted",
	"live/confirms",
	"live/promoted",
	"live/expired",
	"live/summons/pending",
	"live/summons/accepted",
	"live/summons/declined",
	"live/summons/expired",
	"live/channels/active",
	"live/channels/closed",
	"live/goals/active",
	"live/goals/completed",
	"live/goals/abandoned",
}

// NewInitCmd returns the `rufio init [name]` Cobra command. The version
// is threaded through so the banner shows the same value as `--version`
// (the alternative — hardcoded "0.1.0" — drifts the moment a release
// build sets `-ldflags "-X main.version=..."`).
func NewInitCmd(version string) *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Scaffold a new Rufio project",
		Long: "Scaffold a new Rufio project at the current directory.\n\n" +
			"Writes rufio.gdl (project config) + given/ learned/ live/ " +
			"directories, and emits RUFIO.md (the agent-onboarding " +
			"primer). If CLAUDE.md, .cursorrules or AGENTS.md already " +
			"exist, the primer is also folded into them inside " +
			"<!-- rufio:begin --> ... <!-- rufio:end --> markers " +
			"(idempotent — re-init replaces just the block).\n\n" +
			"Arguments:\n" +
			"  [name]   optional project name; recorded in rufio.gdl's " +
			"@config and surfaced in TUI / fleet displays. When omitted, " +
			"the current directory's basename is used.\n\n" +
			"Re-running `rufio init` on an already-initialised project " +
			"is a SAFE PRIMER REFRESH: rewrites RUFIO.md, re-folds the " +
			"marked block into any present harness file, leaves " +
			"rufio.gdl and given/learned/live data untouched.",
		Args: cobra.MaximumNArgs(1),
		// Canonical RunE shape (template for every later command):
		//   1. Build opts from flags.
		//   2. Resolve cwd (or fail).
		//   3. Call run<Cmd>; if it returns an error, HandleError exits.
		//   4. return nil — unreachable on error path; required by RunE
		//      signature on success path.
		// HandleError is the single sink for "rufio <cmd>: ..." prefix +
		// typed exit code. Don't print to stderr from inside run<Cmd>.
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			cwd, err := os.Getwd()
			if err == nil {
				err = runInit(name, cwd, opts, version)
			}
			if err != nil {
				HandleError("init", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runInit(name, cwd string, opts output.RenderOpts, version string) error {
	root, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}
	projectName := name
	if projectName == "" {
		projectName = filepath.Base(root)
	}
	configPath := filepath.Join(root, "rufio.gdl")

	// #128: re-running `rufio init` on an already-initialised project is a
	// SAFE PRIMER REFRESH, not a hard failure. The previous behaviour
	// (return AlreadyInitialisedError before writePrimerArtifacts) meant a
	// CLAUDE.md/AGENTS.md/.cursorrules added AFTER the first init — or a
	// primer-version bump — could never be folded. Instead: skip ALL
	// re-scaffolding (do NOT rewrite rufio.gdl, do NOT recreate the dir
	// tree, do NOT touch given/learned/live), and ONLY refresh RUFIO.md +
	// idempotently re-fold the marked block. writePrimerArtifacts is
	// already idempotent and edit-safe: RUFIO.md is deterministically
	// overwritten, and the marker machinery replaces only the marked block
	// (never duplicates it, never clobbers user content outside markers).
	// The rufioerr.AlreadyInitialisedError type is intentionally KEPT in
	// internal/lib/errors for any other caller — it is just no longer
	// returned from this refresh path.
	if _, err := os.Stat(configPath); err == nil {
		if err := writePrimerArtifacts(root); err != nil {
			return err
		}
		if !opts.Quiet && !opts.JSON {
			banner.Print(banner.Options{Version: version, ShowVersion: true})
			output.WriteOut("  refreshed primer at "+root, opts)
			output.WriteOut("  config: "+configPath+" (left unchanged)", opts)
			output.WriteOut("", opts)
		}
		if opts.JSON {
			summary := map[string]string{
				"name":      projectName,
				"root":      root,
				"config":    configPath,
				"operation": "refresh",
			}
			if err := output.WriteJSONL(summary, opts); err != nil {
				return err
			}
		}
		return nil
	}

	ts := versioning.NowISO()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, sub := range starterDirs {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return err
		}
	}
	if err := versioning.EnsureRufioDir(root); err != nil {
		return err
	}
	if err := os.WriteFile(configPath, []byte(defaultRufioGDL(projectName, ts)), 0o644); err != nil {
		return err
	}

	// Emit the agent-onboarding primer. This is what makes docs/v1-spec.md
	// §"How agents discover Rufio" (line ~281) TRUE: a RUFIO.md at the
	// project root + an idempotent marked block folded into any detected
	// harness context file (CLAUDE.md/.cursorrules/AGENTS.md). This is the
	// FRESH-init path (no rufio.gdl existed). The re-init refresh path
	// above also calls writePrimerArtifacts; it is idempotent, so a primer
	// block is never duplicated by a re-init (verified by the suite).
	if err := writePrimerArtifacts(root); err != nil {
		return err
	}

	if !opts.Quiet && !opts.JSON {
		banner.Print(banner.Options{Version: version, ShowVersion: true})
		output.WriteOut("  initialised rufio project at "+root, opts)
		output.WriteOut("  config: "+configPath, opts)
		output.WriteOut("", opts)
	}
	if opts.JSON {
		summary := map[string]string{
			"name":      projectName,
			"root":      root,
			"config":    configPath,
			"created":   ts,
			"operation": "fresh",
		}
		if err := output.WriteJSONL(summary, opts); err != nil {
			return err
		}
	}
	return nil
}

// defaultRufioGDL produces the project config Greppable file. Same content
// as src/commands/init.ts.
func defaultRufioGDL(name, ts string) string {
	lines := []string{
		"# Rufio project config — Greppable format",
		gdl.RenderLine(gdl.Record{Type: "config", Fields: []gdl.RecordField{
			{Key: "name", Value: name},
			{Key: "version", Value: "1"},
			{Key: "created", Value: ts},
		}}),
		gdl.RenderLine(gdl.Record{Type: "scope", Fields: []gdl.RecordField{
			{Key: "name", Value: "agent"}, {Key: "propagation", Value: "none"},
		}}),
		gdl.RenderLine(gdl.Record{Type: "scope", Fields: []gdl.RecordField{
			{Key: "name", Value: "deployment"}, {Key: "propagation", Value: "same-deployment"},
		}}),
		gdl.RenderLine(gdl.Record{Type: "scope", Fields: []gdl.RecordField{
			{Key: "name", Value: "fleet"}, {Key: "propagation", Value: "all"},
		}}),
		gdl.RenderLine(gdl.Record{Type: "retention", Fields: []gdl.RecordField{
			{Key: "type", Value: "thought"}, {Key: "ttl", Value: "300"}, {Key: "unit", Value: "seconds"},
		}}),
		gdl.RenderLine(gdl.Record{Type: "retention", Fields: []gdl.RecordField{
			{Key: "type", Value: "observation"}, {Key: "ttl", Value: "never"},
		}}),
		gdl.RenderLine(gdl.Record{Type: "retention", Fields: []gdl.RecordField{
			{Key: "type", Value: "given"}, {Key: "ttl", Value: "never"}, {Key: "versioned", Value: "true"},
		}}),
	}
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}
