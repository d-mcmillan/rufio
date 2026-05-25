package cli

// Custom pflag.Value implementations for verb flags that are
// semantically numeric (a float in [0,1] for `--confidence`; an
// integer seconds count for `--ttl`) but whose VALIDATION pipeline
// already lives in the lib layer (observation.ParseConfidence,
// thought.ParseTTL) and is consumed unchanged by the MCP server,
// the SDK passthrough, and a fleet of existing tests.
//
// We don't want to switch the CLI to pflag's built-in Float64Var /
// IntVar because that would replace the existing "invalid --ttl"
// / "invalid --confidence" error contract with pflag's own
// `invalid argument "abc" for "--ttl" flag` wording — a wider
// blast-radius change than this PR allows (issue #123, NN-non-
// negotiables: "DO NOT change any verb's runtime behaviour beyond
// what these 5 items require").
//
// The fix needed for #123 is ONLY the --help display: the flag
// should render as `--ttl int` and `--confidence float` so cold
// agents stop trying to pass `5m` or `high`. That is what
// pflag.Value.Type() controls. So we keep storing the raw string
// (and lazily passing it through the existing parser) but lie
// to pflag about the type-name for --help-rendering purposes
// only.

// stringValueWithType is a pflag.Value that stores a raw string
// and reports a custom Type() name. Used to render numeric-typed
// help text without altering the parsing/validation pipeline.
type stringValueWithType struct {
	raw      *string
	typeName string
}

func (s *stringValueWithType) String() string { return *s.raw }

func (s *stringValueWithType) Set(v string) error {
	*s.raw = v
	return nil
}

func (s *stringValueWithType) Type() string { return s.typeName }
