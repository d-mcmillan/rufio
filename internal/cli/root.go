package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCmd builds the root rufio command and registers every subcommand
// (implemented + stubs). Called by cmd/rufio/main.go.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "rufio",
		Short: "the substrate for distributed cognition",
		// Long is rendered by Cobra under "Description" in --help. We use
		// it to surface the cold-start anchor (#114) and document
		// RUFIO_AGENT_ID in a footer "Environment variables:" section
		// (#112) — both keyed off the cold-start dogfood findings where
		// agents could not discover either via --help alone.
		Long: "the substrate for distributed cognition\n\n" +
			"First time? Run `rufio primer` for the agent-onboarding " +
			"substrate primer (no init required).\n\n" +
			"Environment variables:\n" +
			"  RUFIO_AGENT_ID   override the persisted agent identity for " +
			"this invocation\n" +
			"                   (wins over .rufio/identity.local.gdl; " +
			"format: [a-z0-9][a-z0-9-]{0,63})",
		Version:       version,
		SilenceErrors: true, // we print our own
		SilenceUsage:  true, // don't dump usage on every error
	}

	// Persistent --server / --token flags route any read or write verb
	// through a remote `rufio serve` daemon. When --server is set:
	//   - Identity comes from the token, NOT RUFIO_AGENT_ID
	//   - All FS operations are HTTP calls to the server's /mcp endpoint
	//   - The local .rufio/ is untouched (use `rufio mirror sync` to
	//     keep a local file-native shadow)
	// When --server is NOT set: existing local-substrate behaviour.
	root.PersistentFlags().StringVar(&remoteServerURL, "server", "", "remote rufio server URL (e.g. https://rufio.example.com:8443); falls back to RUFIO_SERVER env")
	root.PersistentFlags().StringVar(&remoteToken, "token", "", "bearer token for --server (or set RUFIO_TOKEN env)")
	root.PersistentFlags().BoolVar(&remoteInsecure, "insecure-tls", false, "skip TLS certificate verification (self-signed dev only)")
	root.PersistentFlags().StringVar(&remoteTimeoutStr, "timeout", "", "per-call timeout when using --server (Go duration, default 30s)")

	// Implemented subcommands (move from allStubs() to here as each phase ships).
	root.AddCommand(NewInitCmd(version))
	root.AddCommand(NewPushCmd())
	root.AddCommand(NewPullCmd())
	root.AddCommand(NewHistoryCmd())
	root.AddCommand(NewDiffCmd())
	root.AddCommand(NewRollbackCmd())
	root.AddCommand(NewDevCmd(version))
	root.AddCommand(NewWhoamiCmd())
	root.AddCommand(NewIdentityCmd())
	root.AddCommand(NewAttendCmd())
	root.AddCommand(NewOpenCmd())
	root.AddCommand(NewThinkCmd())
	root.AddCommand(NewObserveCmd())
	root.AddCommand(NewReasonCmd())
	root.AddCommand(NewRetractCmd())
	root.AddCommand(NewConfirmCmd())
	root.AddCommand(NewConfirmsCmd())
	root.AddCommand(NewRefuteCmd())
	root.AddCommand(NewRecallCmd())
	root.AddCommand(NewApproveCmd())
	root.AddCommand(NewPromoteCmd())
	root.AddCommand(NewListenCmd())
	root.AddCommand(NewStreamCmd())
	root.AddCommand(NewSummonCmd())
	root.AddCommand(NewSummonsCmd())
	root.AddCommand(NewDeclineCmd())
	root.AddCommand(NewAcceptCmd())
	root.AddCommand(NewSayCmd())
	root.AddCommand(NewLeaveCmd())
	root.AddCommand(NewCloseCmd())
	root.AddCommand(NewChannelsCmd())
	root.AddCommand(NewChannelCmd())
	root.AddCommand(NewGoalCmd())
	root.AddCommand(NewGoalsCmd())
	root.AddCommand(NewLineageCmd())
	root.AddCommand(NewFleetCmd())
	root.AddCommand(NewAttentionCmd())
	root.AddCommand(NewThoughtsCmd())
	root.AddCommand(NewSwarmCmd())
	root.AddCommand(NewTuiCmd())
	root.AddCommand(NewDemoCmd())
	root.AddCommand(NewMcpCmd(version))
	root.AddCommand(NewPrimerCmd())
	root.AddCommand(NewQuickstartCmd())
	root.AddCommand(NewServeCmd(version))
	root.AddCommand(NewAdminCmd())
	root.AddCommand(NewMirrorCmd())
	root.AddCommand(NewExportCmd())
	root.AddCommand(NewImportCmd())
	root.AddCommand(NewVersionCmd())

	// Stubs for unimplemented commands (planned for a later milestone).
	for _, s := range allStubs() {
		root.AddCommand(newStubCmd(s.Name, s.Target))
	}

	return root
}
