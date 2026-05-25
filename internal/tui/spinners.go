// spinners.go — the v8 SPINNERS frame-set table + a pure frame selector.
//
// Verbatim character port of the `SPINNERS` object in
// docs/design/tui-v8/reference/rufio-graphs.jsx (lines 152-159) and the
// plan's `## Architectural decision: new package tree` (§12 lists
// spinners.go as the SPINNERS home). ALL SIX sets are declared even
// though only dots/arc/bouncing are wired this PR — the plan §12
// explicitly lists all six (`dots, arc, bouncing, bar, pulse,
// triangle`), and a complete verbatim table is the single source of
// truth a later PR can wire bar/pulse/triangle from without re-porting.
//
// CADENCE: the jsx drives every spinner from one 80ms interval
// (`setInterval(..., 80)`, rufio-graphs.jsx line 163 / rufio-bubbletea-
// v8.jsx line 166) and selects `frames[t % frames.length]`. The Go port
// keeps that exactly: one 80ms tea.Tick increments a single counter
// (anim.spin in app.go) and spinnerFrame does the modulo select.
//
// FRAME-0 INVARIANT: spinnerFrame(set, 0) == set[0]. The three wired
// sets' frame[0] are the exact static consts the screen rendered before
// PR-F — dots[0]=⠋ (was syncSpinnerFrame), arc[0]=◜ (was liveArcFrame),
// bouncing[0]=⠁ (was bouncingSpinnerFrame). So at every counter 0 (a
// fresh App before any tea.Tick fires — exactly the state every golden
// test renders) the spinners are byte-identical to the committed
// goldens. spinners_test.go pins this.
//
// ADD-ONLY: consumed only by the v8 app.go (preview-gated); the old
// internal/tui path is the default `rufio tui` until the PR-G cutover.
package tui

// The six SPINNERS frame sets, each a verbatim split of the jsx string
// literal (rufio-graphs.jsx 152-159). Slices (not strings) so the
// selector indexes whole glyphs without rune-boundary arithmetic at
// every call site. Order within each set is exactly the jsx order.
var (
	// spinnerDots — `'⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'.split('')` (jsx 153). Header
	// "syncing" spinner (jsx rufio-bubbletea-v8.jsx line 211). [0]=⠋.
	spinnerDots = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	// spinnerArc — `'◜◠◝◞◡◟'.split('')` (jsx 154). Chat-header "live"
	// spinner (jsx line 246). [0]=◜.
	spinnerArc = []string{"◜", "◠", "◝", "◞", "◡", "◟"}
	// spinnerBouncing — `'⠁⠂⠄⠂'.split('')` (jsx 155). Mesh-header
	// "live" spinner (jsx line 319). [0]=⠁.
	spinnerBouncing = []string{"⠁", "⠂", "⠄", "⠂"}
	// spinnerBar — `'▎▍▌▋▊▉█▉▊▋▌▍'.split('')` (jsx 156). Declared per
	// plan §12; not wired this PR.
	spinnerBar = []string{"▎", "▍", "▌", "▋", "▊", "▉", "█", "▉", "▊", "▋", "▌", "▍"}
	// spinnerPulse — `'·∙•●•∙'.split('')` (jsx 157). Declared per plan
	// §12; not wired this PR.
	spinnerPulse = []string{"·", "∙", "•", "●", "•", "∙"}
	// spinnerTriangle — `'◢◣◤◥'.split('')` (jsx 158). Declared per plan
	// §12; not wired this PR.
	spinnerTriangle = []string{"◢", "◣", "◤", "◥"}
)

// spinnerFrame returns set[counter % len(set)] — the jsx
// `frames[t % frames.length]` select (rufio-graphs.jsx line 170),
// hardened for Go: an empty/nil set returns "" (the call sites render
// nothing rather than panic) and a negative counter is normalised into
// range (Go's % keeps the sign of the dividend, so a bare counter%len
// could be negative; the tests never go negative but the live tea.Tick
// counter is monotonic-from-0 so this is defensive only).
func spinnerFrame(set []string, counter int) string {
	n := len(set)
	if n == 0 {
		return ""
	}
	i := counter % n
	if i < 0 {
		i += n
	}
	return set[i]
}
