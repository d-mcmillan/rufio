package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/client"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/format"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/recall"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// NewExportCmd returns the `rufio export` Cobra command. JSONL interop
// utility — emits one JSON object per stdout line, one record each.
//
//	rufio export --format=jsonl [--from=live|learned|all] [--since=24h] [--types=csv]
//
// Format choices:
//
//   - jsonl  one JSON object per line (default).
//   - gdl    re-emits canonical GDL lines (symmetry; useful for diff piping).
//
// JSONL is INTEROP only. The substrate stores GDL on disk; this verb is
// for pipelines that don't speak GDL (jq, pandas, etc.).
//
// Respects --scope/--since/--types filters identical to `recall`.
// Honors the privacy floor — caller's identity scopes visibility.
func NewExportCmd() *cobra.Command {
	var (
		formatFlag, scopeFlag, typesFlag, sinceFlag, asOfFlag string
		jsonFlag, quietFlag, noColorFlag                      bool
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export substrate records as JSONL (or canonical GDL)",
		Long: "Streams every visible record to stdout, one object per " +
			"line.\n\nUse to feed substrate data into non-GDL pipelines " +
			"(jq, pandas, BigQuery import). JSONL is import/export only — " +
			"records on disk remain canonical GDL.\n\n" +
			"Pairs with `rufio import --format=jsonl` for round-trip " +
			"backup/restore. Respects the privacy floor: scope=agent " +
			"records authored by other agents are NEVER exported.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runExport(cwd, exportArgs{
					Format: formatFlag, Scope: scopeFlag, Types: typesFlag,
					Since: sinceFlag, AsOf: asOfFlag,
				}, opts)
			}
			if err != nil {
				HandleError("export", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&formatFlag, "format", "jsonl", "output format: jsonl or gdl")
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "scope filter (agent|deployment|fleet)")
	cmd.Flags().StringVar(&typesFlag, "types", "", "CSV of record types to include (default: all)")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "include only records younger than the Go duration")
	cmd.Flags().StringVar(&asOfFlag, "as-of", "", "RFC3339 timestamp; exclude records newer than this")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "(no-op; --format=jsonl is implicit)")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

type exportArgs struct {
	Format, Scope, Types, Since, AsOf string
}

func runExport(cwd string, a exportArgs, opts output.RenderOpts) error {
	fmtKind := strings.ToLower(strings.TrimSpace(a.Format))
	if fmtKind == "" {
		fmtKind = "jsonl"
	}
	switch fmtKind {
	case "jsonl", "gdl":
		// supported
	default:
		return &rufioerr.UsageError{Message: "invalid --format=" + a.Format + " (want jsonl|gdl)"}
	}

	if remoteEnabled() {
		return runExportRemote(cwd, fmtKind, a, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	currentAgent, _, _ := identity.Resolve(root)

	types, err := recall.ValidateTypes(a.Types)
	if err != nil {
		return err
	}
	if a.Scope != "" {
		if err := thought.ValidateScope(a.Scope); err != nil {
			return err
		}
	}
	since, err := recall.ParseSince(a.Since)
	if err != nil {
		return err
	}
	asof, err := recall.ParseAsOf(a.AsOf)
	if err != nil {
		return err
	}

	records, err := recall.Scan(root, true)
	if err != nil {
		return err
	}
	filtered := recall.Filter(records, recall.FilterParams{
		Types: types, Scope: a.Scope, Since: since, AsOf: asof,
		IncludeExpired: false, CurrentAgent: currentAgent,
	})

	// Reuse recall.RenderJSON — same per-record shape the recall verb
	// emits, byte-identical. Root is threaded through so the path
	// field is rendered root-relative (security audit H2 + Bonus).
	var buf bytes.Buffer
	if err := recall.RenderJSON(&buf, root, filtered); err != nil {
		return err
	}

	if fmtKind == "gdl" {
		// gdl format: re-render each record as a canonical GDL line.
		// We read the on-disk file for each path so the bytes are
		// byte-identical to what's on the substrate (no reconstruction
		// drift).
		seenPath := map[string]bool{}
		for _, r := range filtered {
			if r.Path == "" || seenPath[r.Path] {
				continue
			}
			seenPath[r.Path] = true
			bs, err := os.ReadFile(r.Path)
			if err != nil {
				continue
			}
			os.Stdout.Write(bs)
			if len(bs) > 0 && bs[len(bs)-1] != '\n' {
				os.Stdout.Write([]byte("\n"))
			}
		}
		return nil
	}

	// jsonl: stream the per-record JSONL lines that RenderJSON emitted.
	// They're already one-per-line, byte-identical to recall --json.
	os.Stdout.WriteString(buf.String())
	return nil
}

func runExportRemote(_ string, fmtKind string, a exportArgs, opts output.RenderOpts) error {
	timeout := 60 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	c, err := client.Dial(ctx, client.Config{
		Endpoint:    remoteServerURL,
		Token:       resolvedToken(),
		InsecureTLS: remoteInsecure,
		HTTPTimeout: timeout,
	})
	if err != nil {
		return err
	}
	defer c.Close()

	args := dropEmpty(map[string]interface{}{
		"types": a.Types,
		"scope": a.Scope,
		"since": a.Since,
		"as_of": a.AsOf,
	})
	res, err := c.CallTool(ctx, "recall", args)
	if err != nil {
		return err
	}
	rawRecords, _ := res["records"].([]interface{})
	out := make([]map[string]interface{}, 0, len(rawRecords))
	for _, r := range rawRecords {
		if m, ok := r.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	if fmtKind == "gdl" {
		// We don't have raw GDL bytes from the remote tool — render the
		// JSON for each line. The CLI surface treats --format=gdl as
		// "match the local --format=gdl shape", which on the remote
		// path is JSON-only. Print a one-line note on stderr so a user
		// piping --format=gdl through --server gets a clear signal.
		_, _ = fmt.Fprintln(os.Stderr, "export: --format=gdl is not available over --server (use --format=jsonl or run locally)")
	}
	// Stream as JSONL.
	for _, r := range out {
		bs, err := json.Marshal(r)
		if err != nil {
			return err
		}
		os.Stdout.Write(bs)
		os.Stdout.Write([]byte("\n"))
	}
	_ = opts
	_ = format.LineError{}
	return nil
}
