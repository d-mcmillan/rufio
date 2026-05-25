package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/format"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewImportCmd returns the `rufio import` Cobra command. JSONL interop —
// reads JSON objects from stdin, one per line, and writes them as canonical
// substrate records.
//
//	rufio import --format=jsonl [--scope=fleet|agent] [--validate-only]
//
// Each imported record:
//   - Gets a fresh ID (the on-disk id is regenerated; original is dropped)
//   - Preserves author, content, topics, type, subject from the JSON
//   - Lands under live/outbox/<author>/<new-id>.gdl
//
// --validate-only reports parse errors WITHOUT writing. Returns exit 2 on
// any parse error encountered (matches the standard UsageError contract).
func NewImportCmd() *cobra.Command {
	var (
		formatFlag, scopeFlag            string
		validateOnly                     bool
		jsonFlag, quietFlag, noColorFlag bool
	)
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import substrate records from JSONL on stdin",
		Long: "Reads one JSON object per stdin line and writes it as a " +
			"canonical substrate record (live/outbox/<author>/<new-id>." +
			"gdl). Fresh ids are assigned — the original `id` field is " +
			"dropped. --validate-only parses without writing.\n\n" +
			"Pairs with `rufio export --format=jsonl` for round-trip backup.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runImport(cwd, importArgs{
					Format: formatFlag, Scope: scopeFlag, ValidateOnly: validateOnly,
				}, opts)
			}
			if err != nil {
				HandleError("import", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&formatFlag, "format", "jsonl", "input format: jsonl (only supported value)")
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "override scope for imported records (agent|deployment|fleet)")
	cmd.Flags().BoolVar(&validateOnly, "validate-only", false, "parse + report errors without writing records to disk")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSON summary on completion")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

type importArgs struct {
	Format       string
	Scope        string
	ValidateOnly bool
}

func runImport(cwd string, a importArgs, opts output.RenderOpts) error {
	fmtKind := strings.ToLower(strings.TrimSpace(a.Format))
	if fmtKind != "jsonl" {
		return &rufioerr.UsageError{Message: "only --format=jsonl is supported"}
	}
	if a.Scope != "" {
		if err := thought.ValidateScope(a.Scope); err != nil {
			return err
		}
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	// Identity is best-effort; we use it to assign a default author when
	// the JSON record doesn't include one. Authors carried in the JSON
	// are preserved (this lets a cross-org backup restore each record
	// under its original author).
	currentAgent, _, _ := identity.Resolve(root)

	records, parseErrs, err := format.DecodeJSONL(os.Stdin)
	if err != nil {
		return err
	}
	if len(parseErrs) > 0 {
		for _, le := range parseErrs {
			fmt.Fprintf(os.Stderr, "import: line %d: %s (%q)\n", le.Line, le.Message, le.Preview)
		}
		if a.ValidateOnly {
			return &rufioerr.UsageError{Message: fmt.Sprintf("%d malformed line(s) — no records written", len(parseErrs))}
		}
		// In write mode, parse errors are still fatal — we don't want
		// half-imported substrate state.
		return &rufioerr.UsageError{Message: fmt.Sprintf("%d malformed line(s) — refusing to write a partial import", len(parseErrs))}
	}

	if a.ValidateOnly {
		if opts.JSON {
			return output.WriteJSONL(map[string]interface{}{
				"_type":    "import-validate",
				"_version": "1",
				"records":  len(records),
				"errors":   len(parseErrs),
			}, opts)
		}
		if !opts.Quiet {
			fmt.Fprintf(os.Stderr, "import: validated %d records (0 errors)\n", len(records))
		}
		return nil
	}

	// v1.0.4 bug #2: partial-failure used to silently exit 0. Now we
	// count skips alongside writes and exit non-zero when ANY record
	// could not be written. The "write what you can" semantic is
	// preserved (good records still land on disk), but the exit code
	// signals failure so pipelines can `|| handle-failure`.
	wrote := 0
	skipped := 0
	for i, rec := range records {
		author, _ := rec["author"].(string)
		if author == "" {
			author = currentAgent
		}
		if author == "" {
			fmt.Fprintf(os.Stderr, "import: line %d: skipping record without author (and no RUFIO_AGENT_ID to substitute)\n", i+1)
			skipped++
			continue
		}
		typ, _ := rec["_type"].(string)
		if typ == "" {
			typ, _ = rec["type"].(string)
		}
		if typ == "" {
			fmt.Fprintf(os.Stderr, "import: line %d: skipping record without _type\n", i+1)
			skipped++
			continue
		}
		if !supportedImportType(typ) {
			fmt.Fprintf(os.Stderr, "import: line %d: skipping record of unsupported type %q (only thought/observation/reason are imported)\n", i+1, typ)
			skipped++
			continue
		}
		newID, err := thought.GenerateID()
		if err != nil {
			return err
		}
		if err := writeImported(root, author, newID, typ, rec, a.Scope); err != nil {
			return err
		}
		wrote++
	}

	if opts.JSON {
		// JSON consumers get the same numeric breakdown they previously
		// missed — wrote + skipped — so a downstream script can decide
		// without parsing stderr.
		jsonPayload := map[string]interface{}{
			"_type":    "import",
			"_version": "1",
			"wrote":    wrote,
			"skipped":  skipped,
			"errors":   len(parseErrs),
		}
		if err := output.WriteJSONL(jsonPayload, opts); err != nil {
			return err
		}
		if skipped > 0 {
			return &rufioerr.UsageError{Message: fmt.Sprintf("%d record(s) skipped — see stderr for details", skipped)}
		}
		return nil
	}
	if !opts.Quiet {
		fmt.Fprintf(os.Stderr, "import: wrote %d, skipped %d\n", wrote, skipped)
	}
	if skipped > 0 {
		// UsageError → exit code 2 via the dispatcher. Pipelines that
		// want to distinguish "all good" from "some lost data" can
		// branch on the exit code.
		return &rufioerr.UsageError{Message: fmt.Sprintf("%d record(s) skipped — see stderr for details", skipped)}
	}
	return nil
}

// supportedImportType is the allow-list of @<type> values writeImported
// knows how to persist. Other on-disk record kinds (channel-message,
// goal, summon, confirm, refute, retract, attention, …) have dedicated
// cognition verbs as their only write path and cannot be safely
// reconstructed by a generic JSONL importer. They count as skips so
// the exit code signals the data loss.
func supportedImportType(typ string) bool {
	switch typ {
	case "thought", "observation", "reason":
		return true
	}
	return false
}

// writeImported takes a parsed JSON object and persists it as a GDL
// record. Only @thought / @observation / @reason are supported in v1.0.4
// — the high-volume types; channel-messages and goals are write-only
// via their cognition verbs and not part of the import round-trip
// guarantee.
func writeImported(root, author, newID, typ string, rec map[string]interface{}, scopeOverride string) error {
	switch typ {
	case "thought":
		subject, _ := rec["subject"].(string)
		content, _ := rec["content"].(string)
		thoughtType, _ := rec["type"].(string)
		if thoughtType == "" {
			thoughtType = "hypothesis"
		}
		scope, _ := rec["scope"].(string)
		if scopeOverride != "" {
			scope = scopeOverride
		}
		if scope == "" {
			scope = "fleet"
		}
		var topics []string
		if t, ok := rec["topics"].([]interface{}); ok {
			for _, v := range t {
				if s, ok := v.(string); ok {
					topics = append(topics, s)
				}
			}
		}
		ts := versioning.NowISO()
		gdlRec := thought.BuildThoughtRecord(thought.ThoughtInput{
			ID:      newID,
			Author:  author,
			Type:    thoughtType,
			Subject: subject,
			Content: content,
			Scope:   scope,
			Topics:  topics,
			TS:      ts,
		})
		return thought.Write(root, author, newID, []gdl.Record{gdlRec})
	case "observation", "reason":
		// For v1.0.4, encode as a generic thought-shaped record so the
		// round-trip lands the record on disk. Cognition verbs will
		// continue to do the right validation on their own write paths.
		subject, _ := rec["subject"].(string)
		content, _ := rec["content"].(string)
		if content == "" {
			// Build a synthetic content from predicate/object for
			// observations.
			if p, ok := rec["predicate"].(string); ok {
				if o, ok := rec["object"].(string); ok {
					content = p + " " + o
				}
			}
		}
		scope, _ := rec["scope"].(string)
		if scopeOverride != "" {
			scope = scopeOverride
		}
		if scope == "" {
			scope = "fleet"
		}
		ts := versioning.NowISO()
		gdlRec := thought.BuildThoughtRecord(thought.ThoughtInput{
			ID: newID, Author: author, Type: typ, Subject: subject,
			Content: content, Scope: scope, TS: ts,
		})
		return thought.Write(root, author, newID, []gdl.Record{gdlRec})
	}
	return nil
}
