package cli

// identityEnvHelp is the shared, copy-paste-once footer text appended to
// the Long help of every identity-consuming verb (#112 fix).
//
// Background: the dogfood that surfaced #112 found agents had to `strings`
// the binary to discover RUFIO_AGENT_ID, because it is the ONLY
// per-invocation identity override for the cognition verbs (the only
// verbs with their own `--as` flag are `listen` and `summons list`).
// Pinning a uniform line on every identity-consuming verb's --help means
// `rufio <verb> --help` is sufficient — no `strings` archaeology needed.
//
// The single source of truth for what verbs consume identity is whichever
// verb's NewXCmd() actually appends this constant to its Long. The root
// --help mention (in root.go's Long) is the discoverable entry point.
const identityEnvHelp = "\n\nEnvironment variables:\n" +
	"  RUFIO_AGENT_ID   override the persisted agent identity for this " +
	"invocation\n" +
	"                   (wins over .rufio/identity.local.gdl)"

// withIdentityEnvHelp returns the canonical Long help for a verb that
// consumes identity. shortAsLong is the short description (typically
// reused verbatim as the Long opener) — the env-var footer is appended.
//
// Using a constructor helper avoids drift: if a verb forgets to call
// withIdentityEnvHelp() the contract is verified by
// test/integration/root_help_env_test.go (TestVerbHelp_DocumentsRUFIO_AGENT_ID)
// which RED-fails until the help text mentions RUFIO_AGENT_ID.
func withIdentityEnvHelp(shortAsLong string) string {
	return shortAsLong + identityEnvHelp
}
