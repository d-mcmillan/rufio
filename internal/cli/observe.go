package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/observation"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewObserveCmd returns the `rufio observe` Cobra command. Writes a
// single @observation record to learned/<subject-path>/<id>.gdlm.
//
//	rufio observe --subject=<entity> --predicate=<rel> --object=<value>
//	              --scope=<...> [--confidence=<0..1>] [--topics=<csv>]
func NewObserveCmd() *cobra.Command {
	var subjectFlag, predicateFlag, objectFlag, scopeFlag string
	var confidenceFlag, topicsFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "observe",
		Short: "Record a durable observation (subject-predicate-object triple) under learned/",
		Long:  withIdentityEnvHelp("Record a durable observation (subject-predicate-object triple) under learned/."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runObserve(cwd, observeArgs{
					Subject: subjectFlag, Predicate: predicateFlag, Object: objectFlag,
					Scope: scopeFlag, Confidence: confidenceFlag, Topics: topicsFlag,
				}, opts)
			}
			if err != nil {
				HandleError("observe", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&subjectFlag, "subject", "", "entity id this observation is about, namespace:local e.g. customer:5821 (required)")
	cmd.Flags().StringVar(&predicateFlag, "predicate", "", "relationship (required, kebab/snake-case)")
	cmd.Flags().StringVar(&objectFlag, "object", "", "value/object of the observation (required, free-text)")
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "visibility scope (agent|deployment|fleet); default fleet")
	// --confidence is semantically a float in [0,1]; render it as
	// `--confidence float` in --help so cold agents don't pass a
	// word like "high" (issue #123). Parsing + range-check stay
	// in observation.ParseConfidence so the error contract and
	// "empty → 1.0" default are preserved.
	cmd.Flags().Var(&stringValueWithType{raw: &confidenceFlag, typeName: "float"}, "confidence", "confidence [0,1] (optional; default 1.0)")
	cmd.Flags().StringVar(&topicsFlag, "topics", "", "comma-separated topic tokens labelling this observation (plural; record-side labels — distinct from `summon --topic` which names a channel)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

type observeArgs struct {
	Subject, Predicate, Object, Scope string
	Confidence, Topics                string
}

func runObserve(cwd string, a observeArgs, opts output.RenderOpts) error {
	// Validate BEFORE touching the filesystem (design §4.D).
	if err := thought.ValidateSubject(a.Subject); err != nil {
		return err
	}
	if err := observation.ValidatePredicate(a.Predicate); err != nil {
		return err
	}
	object := strings.TrimSpace(a.Object)
	if err := observation.ValidateObject(object); err != nil {
		return err
	}
	// H3a (#125): default empty --scope to fleet. Same rule as think/attend/
	// reason — unified write-verb contract.
	scope := strings.TrimSpace(a.Scope)
	if scope == "" {
		scope = "fleet"
	}
	if err := thought.ValidateScope(scope); err != nil {
		return err
	}
	a.Scope = scope
	confidence, err := observation.ParseConfidence(a.Confidence)
	if err != nil {
		return err
	}
	topics := splitCSVTrim(a.Topics)
	if err := attention.ValidateTopics(topics); err != nil {
		return err
	}

	// v1.0.4: --server routes observe through the remote MCP tool.
	if remoteEnabled() {
		args := dropEmpty(map[string]interface{}{
			"subject":    a.Subject,
			"predicate":  a.Predicate,
			"object":     object,
			"scope":      scope,
			"confidence": a.Confidence,
			"topics":     topics,
		})
		return remoteCallAndRender("observe", "observe", args, opts)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	agent, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	id, err := thought.GenerateID()
	if err != nil {
		return err
	}
	ts := versioning.NowISO()

	rec := observation.BuildObservationRecord(observation.ObservationInput{
		ID: id, Author: agent, Subject: a.Subject, Predicate: a.Predicate,
		Object: object, Scope: a.Scope, Topics: topics,
		Confidence: confidence, TS: ts,
	})

	if err := observation.Write(root, a.Subject, id, rec); err != nil {
		return err
	}

	if opts.JSON {
		payload := map[string]interface{}{
			"_type":      "observe",
			"_version":   "1",
			"id":         id,
			"author":     agent,
			"subject":    a.Subject,
			"predicate":  a.Predicate,
			"object":     object,
			"scope":      a.Scope,
			"confidence": confidence,
			"ts":         ts,
		}
		if topics == nil {
			payload["topics"] = []string{}
		} else {
			payload["topics"] = topics
		}
		return output.WriteJSONL(payload, opts)
	}
	// H3d (#125): house-style echo `<verb>: <key>=<val>...`.
	output.WriteOut("observe: id="+id+" subject="+a.Subject+" predicate="+a.Predicate+" scope="+a.Scope, opts)
	return nil
}
