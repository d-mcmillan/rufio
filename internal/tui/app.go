// app.go — the v8 top-level tea.Model and screen compositor.
//
// Faithful character-cell port of the jsx `RufioBubbleTeaV8` shell from
// docs/design/tui-v8/reference/rufio-bubbletea-v8.jsx, per handoff §5
// (screen layout), §7.1 (top tabs), §7.8 (footer) and §9 ("what's
// faked": gradient = per-char sweep; spinners static this PR).
//
// RE-SCOPE (2026-05-15, PR-D): the nav is Rufio's domain
// (`substrate · fleet · channels · goals · memory`), NOT the v8
// prototype labels (was substrate/agents/stream/memory/rules). The v8
// visual *language* (quiet dot-tabs §7.1, gradient header, borderless
// rows, the panel) is kept verbatim; the data + nav model are Rufio's.
// The footer attribution and any GOV/`rules` chrome are DROPPED
// (prototype chrome, not product). Nav interactivity is pulled forward:
// 1-5 / tab / shift+tab switch tabs, ↑↓/jk select substrate rows, enter
// on a decision row opens the lineage drill-down, ? toggles help, esc
// closes overlays, q quits. See docs/plans/2026-05-15-tui-v8-rebuild.md
// "PR-D — Rufio-domain re-map" and docs/design/tui-v8-data-mapping.md §0.
//
// G4 CUTOVER COMPLETE (2026-05-17): App is now the unconditional default
// of `rufio tui` (internal/cli/tui.go); the RUFIO_TUI_PREVIEW gate and
// the legacy internal/tui Model are deleted. (Was: ADD-ONLY, reachable
// only via the hidden RUFIO_TUI_PREVIEW=1 gate until the PR-G cutover.)
package tui

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// operatorFallbackID is the operator identity used when identity.Resolve
// returns NoIdentityError (no RUFIO_AGENT_ID, no .rufio/identity.local.gdl).
// Matches data-mapping §1 :114 ("else `operator`") and the old TUI's
// "anyone can inspect the fleet" anonymous-ok posture (tui.go:146-149).
const operatorFallbackID = "operator"

// AppView is the active top-level view. Named string consts (not iota)
// per the CLAUDE.md stack rule "no iota enums for user-facing string
// values". RE-SCOPE: the tab set is Rufio's real surfaces — substrate
// (broadcast thought-stream), fleet (agents), channels (private 1:1),
// goals (coordination), memory (durable learned/). `lineage` is NOT a
// tab — it is a per-decision drill-down from a substrate decision row.
// `rules`/governance is dropped (no Rufio primitive).
type AppView string

const (
	viewSubstrate AppView = "substrate"
	viewFleet     AppView = "fleet"
	viewChannels  AppView = "channels"
	viewGoals     AppView = "goals"
	viewMemory    AppView = "memory"
)

// appViewOrder is the canonical left-to-right tab order. Index+1 is the
// numeric keybind (1-5); tab/shift+tab cycle through this slice.
var appViewOrder = []AppView{
	viewSubstrate, viewFleet, viewChannels, viewGoals, viewMemory,
}

// appOverlay is the active focused overlay (none / help / lineage
// drill-down). Drill-down is opened by `enter` on a substrate decision
// row; help by `?`. `esc` (or `?` again, for help) closes back to none.
type appOverlay string

const (
	overlayNone    appOverlay = "none"
	overlayHelp    appOverlay = "help"
	overlayLineage appOverlay = "lineage"
)

// wordmark is the gradient header text (jsx line 209). The per-char
// color sweep is applied at render time (handoff §9).
const wordmark = "rufio · substrate"

// substrateEmptyHint is the quiet, centered setup guidance shown in the
// messages region when the substrate has NO thoughts at all (fresh /
// empty). The v8 frame (panels/borders/chrome/composer) renders normally
// AROUND it — this is a single hint line, never a modal block or a blank
// void. The wording reuses the old TUI's attend-guidance copy
// (tui.go:119 `emptyStateHint`) re-toned to the v8 borderless language
// (rendered Dim + centered, no old-TUI chrome). Kept short so it fits
// the NARROWED two-panel chat content width (~66 cells at 120 cols)
// without truncation. LOCKED 2026-05-16: the console is a filesystem
// console and stays fully usable even with nothing on disk yet.
const substrateEmptyHint = "no thoughts on the substrate yet — try `rufio think` from an agent"

// substrateOfflineNote is the quiet daemon-offline indicator surfaced in
// the chat-chrome strip when PollDaemonOnline reports the daemon down.
// LOCKED 2026-05-16: the TUI is NEVER gated on a live daemon — history
// on disk renders normally and the console stays fully usable; this is a
// non-alarming Warm/Dim status whisper, NOT a modal block. Reuses the
// old TUI's `· daemon offline ·` wording (tui.go:661), v8-styled.
const substrateOfflineNote = "· daemon offline ·"

// anim holds the five INDEPENDENT animation cadence counters (PR-F).
// Each is the monotonic tick COUNT for one cadence — View() selects the
// current frame as `frames[counter % len(frames)]` (spinners), the
// blink phase, the typing-dot phase, the mesh tick, and the series
// shift. A struct (not the old single `tick int` seam) because the five
// periods (80/90/220/500/1000ms) advance at different rates and a
// single counter cannot represent five phases.
//
// FRAME-0 INVARIANT: the zero value `anim{}` (every counter 0) is the
// state of a fresh App before ANY tea.Tick fires — exactly the state
// every golden/zzrender/border test renders (drive() never delivers a
// tick Msg). At anim{} every cadence selects frame[0], which is
// byte-identical to the pre-PR-F static render: dots[0]=⠋, arc[0]=◜,
// bouncing[0]=⠁, caret ON (blink starts opacity-1), typing=`···`,
// sparkline window=`▁▂▃▄▅▆▇█▆▅` + rate `3/s`, mesh tick 0 (no
// particles/rings advanced). So the committed goldens stay unchanged.
type anim struct {
	spin   int // 80ms   — all three spinners (dots/arc/bouncing)
	mesh   int // 90ms   — mesh particle flow + node pulse rings
	typing int // 220ms  — typing-dots 3-state cycle
	series int // 500ms  — sparkline series shift count
	caret  int // 500ms toggle (1000ms cycle, 50% duty) — even=ON, odd=OFF
}

// App is the v8 top-level tea.Model.
//
// Fields wired this PR (PR-F): root, width, height, view, overlay,
// selected, channelSel, anim, series. Deferred to PR-G (real data;
// watch.go re-wire): the substrate fsnotify watcher + the live
// composer/paused state (handoff §8.2).
//
// `anim` (the five animation cadence counters) replaces the old single
// `tick int` seam — PR-F is the first reader, and one counter could not
// represent the five independent periods. `series` is the deterministic
// sparkline ring; it advances in lock-step with anim.series so View()
// reads a value, not a clock. View() now READS anim+series to select
// the live frame for every cadence (was: a static first frame).
type App struct {
	// root is the resolved project root. Stored for PR-G (real
	// substrate wiring); unused for rendering this PR.
	root string
	// width / height are the terminal dimensions from the latest
	// tea.WindowSizeMsg. Zero until the first resize.
	width  int
	height int
	// view is the active top-level tab.
	view AppView
	// overlay is the active focused overlay (none / help / lineage).
	overlay appOverlay
	// selected is the highlighted SubstrateThread row index (substrate
	// tab only). ↑/k ↓/j move it; enter on a decision row opens the
	// lineage drill-down.
	selected int
	// scrollOffset is the substrate-feed scrollback position (#134): the
	// number of RENDERED lines scrolled UP from the live bottom. 0 = the
	// live tail (exactly the pre-#134 topTruncate render — byte-identical;
	// every golden is frame-0 at offset 0). PgUp/Home increase it,
	// PgDn/End decrease it; windowLines clamps it to [0, n-maxRows] on
	// every render so it can never point past the content. It is a PURE
	// VIEW concern — the full thread is always resident in a.substrate;
	// only the render window moves. Re-clamped in the substrateLoadedMsg
	// fold (mirroring the `selected` re-clamp) so a shorter re-read or new
	// events while scrolled keep the view in range.
	scrollOffset int
	// channelSel is the selected ChannelThreads index (channels tab).
	// Default-selects the first channel.
	channelSel int
	// anim holds the five independent animation cadence counters
	// (80/90/220/500/1000ms). Zero value = frame-0 (every golden test
	// renders here — drive() never sends a tick Msg). View() reads it.
	anim anim
	// series is the 36-sample sparkline ring. v1.0.6.3 (Bundle F) wires
	// it to REAL substrate event rate: every 500ms seriesTickMsg, the
	// App computes events_per_sec from the eventTickCount field (events
	// observed since the previous tick: ThoughtMsg + ConfirmMsg +
	// AttentionMsg arrivals from the fsnotify watcher), feeds the count
	// into series.advanceWithSample, and resets the counter. The
	// deterministic series.advance(tick) path remains for tests + the
	// frame-0 golden invariant. Nil-safe: lazily created in NewApp.
	series *series
	// eventTickCount accumulates substrate events received since the
	// previous seriesTickMsg. ThoughtMsg, ConfirmMsg, and AttentionMsg
	// increment it; the 500ms series tick reads it, multiplies by 2
	// (one tick = 0.5s), feeds the result into series as events/sec,
	// then resets to 0. Reset-on-tick gives a 500ms sliding rate; the
	// jitter is acceptable for a v1 ambient indicator and matches the
	// resolution callers expect from a 2 Hz sparkline.
	eventTickCount int

	// ── PR-G1: live substrate state ─────────────────────────────────
	//
	// The substrate chat is now LIVE (read-only): the static
	// SubstrateThread fixture render is replaced by the projected live
	// broadcast feed (loadSubstrate → projectThread, with the OPEN-2
	// threshold applied). The mesh / channels / goals / memory tabs +
	// the lineage drill-down still read fixtures.go (G2/G3, out of
	// scope here). These fields are folded by Update on each watcher
	// Msg and read by the substrate render path; they are independent
	// of the anim/series cadence fields above (those stay untouched).

	// me is the resolved operator identity (identity.Resolve; default
	// operatorFallbackID on NoIdentityError). It is the projectThread
	// operatorID — a row authored by `me` renders as a kindOp row.
	me string
	// now is the clock loadSubstrate/projectThread is given. Set once
	// at NewApp (time.Now for the live binary); the chat rows do not
	// render a now-derived value (projectThread's now is reserved;
	// tsToClock uses each record's own ts), so this is render-invariant
	// — but tests still bypass it entirely by injecting a pinned
	// substrateLoadedMsg, never relying on the disk load.
	now time.Time
	// substrate is the live projected chat thread (the replacement for
	// the SubstrateThread fixture in the substrate panel). Hydrated
	// synchronously in NewApp so the FIRST paint is populated from
	// history-on-disk even before the watcher streams and even if the
	// daemon is offline; re-projected on every fold.
	substrate []ThreadMsg
	// mesh is the live projected substrate mesh (PR-G2): the synthesized
	// operator hub (OPEN-4 — always present) + the agents with a
	// live/attention/ record (G0 projectMeshNodes, verbatim) + the
	// outbox∩inbox routing edges (G0 deriveMeshEdgesLive, verbatim). It
	// replaces the MeshNodes/deriveMeshEdges FIXTURE in BOTH the substrate
	// right-rail AND the fleet tab (same renderer/data per the v8
	// design). Hydrated synchronously in NewApp (mesh populated on the
	// FIRST paint — operator-only minimum even with zero attention) and
	// re-projected on every AttentionMsg / routing-affecting watcher
	// event via the meshLoadedMsg fold. Independent of substrate/anim/
	// series. The channels/goals/memory/lineage tabs stay on fixtures.go
	// (G3, out of scope here).
	mesh meshState

	// ── PR-G3: live channels/goals/memory tabs + lineage-id carry ────
	//
	// The channels / goals / memory tabs are now LIVE (read-only): the
	// static ChannelThreads / GoalCards / MemoryEntries fixture renders
	// are replaced by the projected on-disk state. The lineage drill-down
	// is now LIVE too — `enter` on a live decision row resolves the
	// overlay via the G0 projectLineage(root, <that row's thought-id>)
	// VERBATIM. These fields are folded by Update on the relevant watcher
	// events and read by the tab render path / the enter handler; they
	// are independent of the substrate/mesh/anim/series fields above.

	// tabs is the live projected channels/goals/memory state (the
	// replacement for the ChannelThreads/GoalCards/MemoryEntries
	// fixtures in the tab render path). Hydrated synchronously in NewApp
	// so the FIRST paint is populated from on-disk history even before
	// the watcher streams and even if the daemon is offline; re-projected
	// on every pane watcher event (ChannelMsg/ChannelMessageMsg/GoalMsg/
	// InboxMsg) via the tabsLoadedMsg fold. The lineage drill-down does
	// NOT cache here — it is resolved on demand on `enter` (the G0
	// projectLineage lib reads, not eagerly per row, mirroring G0's
	// design note project.go:166-167).
	tabs tabState
	// substrateIDs is the lineage-id carry: parallel to a.substrate, the
	// @thought `id` of each row's source stream.Event (substrateRowIDs /
	// loadSubstrateWithIDs — live_substrate.go / live_tabs.go). `enter`
	// on a decision row resolves projectLineage(root,
	// substrateIDs[selected]) VERBATIM. Threaded through the SAME
	// substrateLoadedMsg seam the rows enter through so the two cannot
	// drift; G0 projectThread / ThreadMsg / fixtures.go are NOT modified
	// (the carry lives entirely in the live path). Same length as
	// a.substrate by construction (one id per projected row, in order).
	substrateIDs []string
	// substrateSubjects is the G-interact subject carry: parallel to
	// a.substrate (one entry per projected row, same order — derived from
	// the SAME ordered events as substrateIDs so it cannot drift), the
	// `subject` field of each row's source @thought (rawField(ev,
	// "subject"), the G0 helper — NOT a second hand-rolled parse). It is
	// the approved context-aware free-text broadcast default's input:
	// resolveBroadcastSubject reads the selected/most-recent row's subject
	// from here. Threaded through the SAME substrateLoadedMsg seam the
	// rows + ids enter through so the three stay exactly parallel; a
	// directly test/gate-injected msg may leave it nil → the documented
	// opSubjectFallback is used (those gates do not exercise broadcast).
	// G0 projectThread / ThreadMsg / fixtures.go are NOT modified (the
	// carry lives entirely in the live path — exactly like substrateIDs).
	substrateSubjects []string
	// lineage is the resolved drill-down payload for the CURRENTLY-open
	// lineage overlay (built on `enter` via projectLineage; nil when no
	// overlay or the resolve failed). It is NOT a per-row cache — it is
	// the single in-flight overlay's payload, set when overlayLineage is
	// opened and cleared (left stale-but-unread) when it closes. The
	// View() lineage branch reads it.
	lineage *DecisionLineage
	// sawThought is the "any thoughts yet?" cold-start signal. It is
	// true once ANY substrate row has been loaded (from the initial
	// disk hydration OR a live ThoughtMsg). Distinguishes "fresh/empty
	// substrate" (sawThought=false → setup hint) from "loaded, just
	// scrolled past" — it never goes back to false.
	sawThought bool
	// daemonOnline mirrors the periodic PollDaemonOnline check. The TUI
	// is a filesystem console and is NEVER gated on this (history still
	// renders when false) — it only drives the quiet daemon-offline
	// indicator in the chat-chrome strip (LOCKED 2026-05-16).
	daemonOnline bool
	// watcherCmd is the self-re-issuing fsnotify drain cmd
	// (NewWatcherFor → watcherCmd(out)). Update returns it EXACTLY ONCE
	// per consumed watcher Msg (the drain pattern, tui.go:210-213): one
	// outstanding blocking receive at a time — never dropped (would
	// stop the stream), never duplicated (would double-drain). nil
	// before the watcher is armed and after WatcherClosedMsg.
	watcherCmd tea.Cmd
	// watcherStop tears the fsnotify goroutine down cleanly. Called on
	// quit BEFORE tea.Quit (tui.go:402-407) so the goroutine + watcher
	// don't leak. nil until the watcher is armed.
	watcherStop func()

	// ── G-interact: the interactive composer (operator WRITE side) ───
	//
	// The v8 substrate console can now WRITE. These fields are the
	// composer's live input + the focus/modal model; they are read by the
	// substrate render path (renderComposerLive) and folded by handleKey.
	// They are ENTIRELY ADDITIVE — the read-only G1/G2/G3 stack + every
	// other field is untouched. (Was preview-only behind the
	// RUFIO_TUI_PREVIEW gate; G4 made v8 the unconditional default,
	// 2026-05-17.)

	// composeMode is the v8 substrate focus model. The substrate view has
	// two modes: compose (typing goes to the composer textarea; ⏎ sends;
	// ⇧⏎ newline) and nav (the existing 1-5/tab/jk/enter→lineage/esc/?
	// keymap). It starts TRUE (compose) — the composer is the console's
	// primary affordance and is "always focused in a TUI" (handoff §9).
	// `esc` toggles compose→nav (and also closes an overlay first); `i` or
	// any printable key returns nav→compose. ONLY meaningful on the
	// substrate view (the other tabs are read-only lists — compose is
	// inert there, nav keys always work). Documented focus model: see
	// handleKey.
	composeMode bool
	// composeTA is the live composer text input. WHY a bubbles/textarea
	// (not the prior hand-rolled append-only `composeBuf string`): a
	// console composer must support the standard readline/terminal editing
	// set — Ctrl+U (kill to line start), Ctrl+K (kill to line end),
	// Ctrl+W/Alt+Backspace (delete word back), Ctrl+A/Ctrl+E (line
	// start/end), Ctrl+Left/Ctrl+Right/Alt+B/Alt+F (word motion),
	// left/right/home/end, backspace/delete, and multi-line entry — i.e. a
	// real cursor + bindings, not append-and-droplast. bubbles/textarea
	// (already in go.mod — github.com/charmbracelet/bubbles v1.0.0, NO new
	// dep) is the mature multi-line input that implements ALL of the above
	// natively (see newComposerTextarea for the keymap + the honest
	// Cmd-chord limitation). It is the composer's EDITING model only — the
	// v8 composer VISUAL is still rendered by renderComposerLive from
	// composeTA's value + cursor; the textarea's own chrome/border/View()
	// is never shown. ⏎ parses + emits composeTA.Value() (broadcast /
	// @directed / /slash) then Reset()s it. Empty → renderComposerLive
	// shows the Dim composerPlaceholder (no fake content). Stored by value
	// (App is a value receiver); pointer-receiver textarea methods
	// (SetValue/Reset/Focus/SetWidth/Update) operate on the field's
	// address through a local then assign back, which is correct under
	// value semantics.
	composeTA textarea.Model
	// composeNote is the transient in-pane result/error line for the LAST
	// composer action (e.g. "broadcast ✓", a validation error, the
	// @directed summon/say note). Rendered in the chat-chrome strip (the
	// existing in-pane convention — never stdout, never a crash, never an
	// exit code). Cleared on the next keystroke so it does not pin stale
	// state. NOT golden-pinned (goldens fix an empty buffer + no note).
	composeNote string
}

// substrateLoadedMsg carries a freshly-projected substrate thread (the
// already-OPEN-2-resolved []ThreadMsg). It is the SINGLE seam through
// which substrate state enters the App:
//
//   - the live binary: emitted by loadSubstrateCmd after a watcher
//     event re-reads disk (Update folds it + re-arms the watcher);
//   - tests/goldens: injected DIRECTLY via App.Update(substrateLoadedMsg
//     {...}) with a PINNED fixed thread (NO disk, NO fsnotify, NO
//     wall-clock) — the deterministic-render contract. The pinned
//     fixture for the structural gates + the substrate golden is
//     SubstrateThread (fixtures.go, byte-unchanged — it doubles as the
//     gate injection data now that the panel render is live).
type substrateLoadedMsg struct {
	rows []ThreadMsg
	// ids is the PR-G3 lineage-id carry: parallel to rows, the @thought
	// `id` of each row's source stream.Event (loadSubstrateWithIDs →
	// substrateRowIDs). The live binary's loadSubstrateCmd ALWAYS sets it
	// (rows + ids from the same event pass). It is OPTIONAL on a directly
	// test-injected Msg: a structural-gate / regression test that injects
	// `substrateLoadedMsg{rows: SubstrateThread}` leaves ids nil — the
	// fold then clears a.substrateIDs and `enter` on a decision row is a
	// no-op (no id to resolve), which is correct for those gates (they do
	// not exercise the drill-down). The dedicated live-lineage test/
	// golden injects ids (or drives off real disk) so the drill-down is
	// covered. Threaded on the SAME Msg the rows enter through so rows[i]
	// and ids[i] cannot drift.
	ids []string
	// subjects is the G-interact subject carry: parallel to rows, the
	// @thought `subject` of each row's source event (loadSubstrateAll).
	// The live binary's loadSubstrateCmd ALWAYS sets it (rows + ids +
	// subjects from the SAME event pass — they cannot drift). OPTIONAL on
	// a directly test-injected Msg: a gate/regression test that injects
	// only `rows` leaves it nil → the fold clears a.substrateSubjects and
	// resolveBroadcastSubject uses the documented opSubjectFallback (those
	// gates do not exercise the broadcast-subject path; the dedicated
	// G-interact tests set it / drive off real disk). Same exactly-
	// parallel discipline as ids.
	subjects []string
}

// meshLoadedMsg carries a freshly-projected substrate mesh (the
// operator-hub+agents nodes + the routing edges). It is the SINGLE seam
// through which mesh state enters the App — the EXACT mirror of
// substrateLoadedMsg for the chat:
//
//   - the live binary: emitted by loadMeshCmd after an AttentionMsg /
//     routing-affecting watcher event re-reads disk (Update folds it +
//     re-arms the watcher exactly once, per the drain invariant);
//   - tests/goldens: injected DIRECTLY via App.Update(meshLoadedMsg{...})
//     with a PINNED mesh (NO disk, NO fsnotify, NO wall-clock) — the
//     deterministic-render contract (the G1 pattern, extended to G2).
type meshLoadedMsg struct {
	mesh meshState
}

// tabsLoadedMsg carries a freshly-projected channels/goals/memory tab
// state. It is the SINGLE seam through which tab state enters the App —
// the EXACT mirror of substrateLoadedMsg / meshLoadedMsg for the list
// tabs (PR-G3):
//
//   - the live binary: emitted by loadTabsCmd after a pane watcher event
//     (ChannelMsg/ChannelMessageMsg/GoalMsg/InboxMsg) re-reads disk
//     (Update folds it + the pane Msg already re-armed the watcher
//     exactly once — the drain invariant);
//   - tests/goldens: injected DIRECTLY via App.Update(tabsLoadedMsg{...})
//     with a PINNED tabState (NO disk, NO fsnotify, NO wall-clock) — the
//     deterministic-render contract (the G1/G2 pattern, extended to G3).
//
// Like substrateLoadedMsg / meshLoadedMsg it MUST NOT re-arm the watcher
// (it is produced by the one-shot loadTabsCmd / a test inject, NOT the
// watcher drain — re-arming here would double-drain).
type tabsLoadedMsg struct {
	tabs tabState
}

// watcherReadyMsg hands the freshly-constructed fsnotify watcher's drain
// cmd + stop fn to Update. The watcher is built LAZILY in an Init cmd
// (not in NewApp) so NewApp stays side-effect-free: the ~40 v8 tests +
// the golden/structural-gate helpers construct App via NewApp and must
// NOT spawn a goroutine or MkdirAll under the fake test root. This
// mirrors the old pointer-Model building the watcher in Init
// (tui.go:180-194); the value-receiver App carries it forward via this
// Msg instead of a pointer field set in Init.
type watcherReadyMsg struct {
	cmd  tea.Cmd
	stop func()
}

// NewApp constructs the v8 App. Its (App, error) contract mirrored the
// (now-deleted) old tui.NewModel(root) so internal/cli/tui.go could swap
// constructors with an identical call shape — done at the G4 cutover
// (2026-05-17); NewApp is now the sole `rufio tui` constructor.
//
// PR-G1 — the error return is now REAL (the signature was forward-
// looking before): identity resolution can fail with a non-canonical
// error and substrate hydration reads disk. Per the old TUI's read-only
// inspector posture (tui.go:142-149) NoIdentityError is NON-fatal (we
// fall back to operatorFallbackID — "anyone can inspect the fleet");
// only a malformed-identity / IO-class error is surfaced. The substrate
// is hydrated SYNCHRONOUSLY here (mirrors NewModel's InitialWalk fold,
// tui.go:153-159) so the FIRST paint is populated from history-on-disk
// even before the watcher streams and even if the daemon is offline
// (the cold-start contract). The fsnotify watcher is NOT started here
// (Init does that, lazily — keeps NewApp side-effect-free for tests).
func NewApp(root string) (App, error) {
	me := operatorFallbackID
	if id, _, err := identity.Resolve(root); err == nil {
		me = id
	} else {
		var noID *rufioerr.NoIdentityError
		if !errors.As(err, &noID) {
			// A real identity failure (malformed RUFIO_AGENT_ID / IO /
			// parse) — surface it (the error return is finally used). A
			// plain NoIdentityError is swallowed (anonymous inspect is
			// fine — the old TUI's read-only posture, tui.go:146-149).
			return App{}, err
		}
	}

	now := time.Now()
	// Synchronous initial hydration: history-on-disk renders on the
	// first paint (loadSubstrate is a pure read; an empty/absent
	// substrate yields an empty slice — the fresh/empty cold-start
	// signal, never a panic). Tests bypass this entirely by injecting a
	// pinned substrateLoadedMsg.
	// PR-G3: capture the parallel lineage-id carry from the SAME load
	// (loadSubstrateWithIDs — rows + ids from one event pass so they
	// cannot drift). loadSubstrate keeps its signature; this is the
	// id-aware sibling. G0 projectThread / ThreadMsg are NOT modified.
	// G-interact: also hydrate the parallel subject carry (loadSubstrate-
	// All — rows + ids + subjects from one event pass so they cannot
	// drift) so resolveBroadcastSubject works from the first paint.
	rows, rowIDs, rowSubjects := loadSubstrateAll(root, me, now)
	// PR-G2: hydrate the live mesh SYNCHRONOUSLY too (mirrors the
	// substrate hydration above + tui.go's InitialWalk fold) so the mesh
	// is populated on the FIRST paint — even before the watcher streams
	// and even if the daemon is offline (loadMesh reads on-disk
	// attention/routing directly, never gated on the daemon). loadMesh
	// ALWAYS yields ≥1 node (the synthesized operator hub — OPEN-4) so
	// the cold-start mesh is the operator alone, never a void. Tests
	// bypass this by injecting a pinned meshLoadedMsg.
	mesh := loadMesh(root, me)
	// PR-G3: hydrate the live channels/goals/memory tabs SYNCHRONOUSLY
	// too (mirrors the substrate + mesh hydration above + the old
	// tui.go's InitialWalkPanes fold, tui.go:157) so the tabs are
	// populated on the FIRST paint — even before the watcher streams and
	// even if the daemon is offline (loadTabs reads on-disk channels/
	// goals/learned directly via the libs + G0 walkLearned, never gated
	// on the daemon). An empty/absent substrate yields empty slices (the
	// renderers handle empty — never a panic). Tests bypass this by
	// injecting a pinned tabsLoadedMsg.
	tabs := loadTabs(root, me, now)

	return App{
		root:    root,
		me:      me,
		now:     now,
		view:    viewSubstrate,
		overlay: overlayNone,
		// Substrate selection defaults to the most recent row (the
		// freshest — the decision row in the canonical arc, the
		// interesting one to drill into). Clamped to a valid index for
		// the empty cold-start (no rows → selected 0, harmless).
		selected:          lastRowIndex(rows),
		channelSel:        0,
		substrate:         rows,
		substrateIDs:      rowIDs,
		substrateSubjects: rowSubjects,
		mesh:              mesh,
		tabs:              tabs,
		sawThought:        len(rows) > 0,
		daemonOnline:      DaemonOnline(root),
		// G-interact: the composer is the console's primary affordance and
		// is "always focused in a TUI" (handoff §9) — start in compose mode
		// so typing just works. `esc` drops to nav. The default `rufio tui`
		// path is unaffected (this is preview-only, value-receiver App).
		composeMode: true,
		// The composer's editing model: a focused bubbles/textarea with
		// the full readline keymap. Rendered through the v8 composer
		// visual (renderComposerLive) — its own View() is never shown.
		composeTA: newComposerTextarea(),
		// anim is the zero value (frame-0). series starts at its seeded
		// counter-0 state so the chat chrome is byte-identical to the
		// pre-PR-F static render until the first 500ms seriesTickMsg.
		series: newSeries(),
	}, nil
}

// newComposerTextarea builds the composer's editing model: a
// bubbles/textarea configured so its VALUE + CURSOR drive the v8
// composer visual (renderComposerLive) — its own chrome/border/View()
// is NEVER rendered. WHY each setting:
//
//   - Focus(): the composer is "always focused in a TUI" (handoff §9);
//     an unfocused textarea ignores key input (textarea.Update early-
//     returns when !focus). The App's modal model (composeMode) gates
//     whether keys are ROUTED here; the textarea itself stays focused.
//   - ShowLineNumbers=false, Prompt="": the v8 render supplies the `›`
//     prompt + frame; the textarea's own gutter must not exist (we
//     never call its View(), but disabling them also keeps SetWidth's
//     reserved-width math == the content width so cursor columns line
//     up 1:1 with what renderComposerLive draws).
//   - A very wide SetWidth (no soft-wrap): the v8 composer is a
//     FIXED-HEIGHT single visible input row (the documented
//     fixed-height contract — a growing textarea would shift the chat
//     panel's composer top-rule and break the cross-column ROUTING-rule
//     alignment + every structural gate). With no soft-wrap, LineInfo()
//     reports the cursor's column WITHIN its logical line directly, so
//     renderComposerLive can place the v8 blink-caret at the exact
//     cursor cell on the cursor's logical line. ⇧⏎ still makes a real
//     multi-line buffer (newlines split logical lines); only the
//     cursor's logical line is shown, exactly as the prior buffer
//     showed only its last line.
//   - CharLimit=0 (no cap): byte-for-byte the prior hand buffer's
//     behaviour (it had no cap). The composer's `N / 2000` counter is a
//     DISPLAY shape only (jsx line 290), not an enforced limit; capping
//     would change behaviour, so it stays uncapped.
//   - KeyMap: textarea.DefaultKeyMap already binds the FULL readline
//     set (Ctrl+U/K, Ctrl+W & Alt+Backspace, Ctrl+A/E, Alt+B/F word
//     motion, left/right/home/end, backspace/delete, Ctrl+N/P line
//     motion). We additionally bind Ctrl+Left/Ctrl+Right to word
//     motion (the prompt explicitly requires them; the default only
//     has Alt+Left/Right + Alt+B/F).
//
// HONEST Cmd-chord limitation (documented, not a gap): macOS terminal
// emulators intercept Cmd-chords (e.g. Cmd+Delete) before the TUI ever
// sees them — a TUI cannot receive them. The universal readline set
// above is what "delete the whole row" etc. actually means in a
// console: Ctrl+U kills to line start, Ctrl+K to line end, Ctrl+W /
// Alt+Backspace deletes a word back.
func newComposerTextarea() textarea.Model {
	ta := textarea.New()
	ta.Prompt = ""             // v8 render supplies the `›` prompt + frame.
	ta.ShowLineNumbers = false // no gutter — the v8 frame is the chrome.
	ta.CharLimit = 0           // no cap — byte-identical to the prior buffer.
	// No placeholder on the textarea itself — the EMPTY-state Dim
	// composerPlaceholder is rendered by renderComposerLive (so the
	// empty/edited render contract is owned in one place, unchanged).
	ta.Placeholder = ""
	// A very wide content width so the buffer never SOFT-wraps: the v8
	// composer is a fixed-height single visible row (only the cursor's
	// logical line shows). SetWidth reserves prompt+gutter width; with
	// both zeroed the content width == the argument.
	ta.SetWidth(1 << 14)
	// Single logical viewport row is all we read; the v8 frame owns the
	// vertical footprint (ComposerHeight). Height only affects the
	// textarea's own View(), which we never call — keep it minimal.
	ta.SetHeight(1)
	// MaxHeight default (99) bounds ⇧⏎ lines; the v8 render only ever
	// shows ONE of them so this never affects the visible footprint. It
	// just keeps an adversarial paste/hold from growing the value grid
	// unbounded — a safety bound, not a behaviour change.

	// Add Ctrl+Left / Ctrl+Right to the word-motion bindings (the prompt
	// explicitly requires them; textarea.DefaultKeyMap only binds
	// Alt+Left/Right + Alt+B/F for word motion). The full readline set is
	// otherwise the textarea defaults, verbatim.
	km := textarea.DefaultKeyMap
	km.WordBackward = key.NewBinding(
		key.WithKeys("alt+left", "alt+b", "ctrl+left"),
		key.WithHelp("ctrl+left", "word backward"),
	)
	km.WordForward = key.NewBinding(
		key.WithKeys("alt+right", "alt+f", "ctrl+right"),
		key.WithHelp("ctrl+right", "word forward"),
	)
	ta.KeyMap = km

	// Always focused (handoff §9): the App's composeMode gates ROUTING;
	// the textarea itself must accept keys whenever they reach it.
	ta.Focus()
	return ta
}

// composeText is the composer's current text — the single read seam for
// the buffer (composeSend, the regression/keymap tests, the live render
// all go through it). It is textarea.Value() — the textarea is the sole
// source of truth for the composer buffer now (the prior `composeBuf`
// string field is gone).
func (a App) composeText() string { return a.composeTA.Value() }

// lastRowIndex returns the index of the freshest substrate row (the
// default selection — the one a `↑` walks back from), or 0 for an empty
// thread (a harmless in-range default for the cold-start frame).
func lastRowIndex(rows []ThreadMsg) int {
	if len(rows) == 0 {
		return 0
	}
	return len(rows) - 1
}

// centerHintBlock renders `hint` (Dim) on ONE vertically- and
// horizontally-centered line within a width×height block; every OTHER
// line is a GENUINE empty string (NOT a space-padded line — that is the
// whole point: space-padded blanks are exactly `width` cells and trip
// clampBlock's truncateToWidth into appending a stray `…` on each empty
// row, the cold-start void bug). The hint is hard-clamped to `width`
// (defensive; the const is sized to fit the narrowed chat content) and
// left-padded by floor((width−w)/2) so it sits centered. Used only by
// the fresh/empty substrate state.
func centerHintBlock(hint string, width, height int) string {
	if height < 1 {
		height = 1
	}
	styled := lipgloss.NewStyle().Foreground(styles.Palette.Dim).Render(hint)
	styled = clampLine(styled, width)
	pad := (width - lipgloss.Width(styled)) / 2
	if pad < 0 {
		pad = 0
	}
	line := strings.Repeat(" ", pad) + styled
	mid := height / 2
	lines := make([]string, height)
	for i := range lines {
		if i == mid {
			lines[i] = line
		} else {
			lines[i] = "" // genuine blank — NOT width-padded (no `…`)
		}
	}
	return strings.Join(lines, "\n")
}

// Init implements tea.Model. Arms, in one batch (mirrors tui.go:194
// tea.Batch(cmd, PollDaemonOnline)):
//
//   - animCmds() — the five PR-F self-rescheduling animation cadences
//     (80/90/220/500/1000ms), UNCHANGED + independent of the live data;
//   - startWatcherCmd — builds the fsnotify watcher LAZILY (NewWatcherFor)
//     and yields a watcherReadyMsg so Update can store the drain cmd +
//     stop fn and start draining (the watcher is built here, not in
//     NewApp, so NewApp stays side-effect-free for the test/golden
//     helpers — the value-receiver adaptation of tui.go:180-194);
//   - PollDaemonOnline — the 2s daemon-online ticker (re-armed in Update).
//
// The cmds only START firing once the bubbletea program loop runs;
// tests never invoke the loop AND inject substrate via a pinned
// substrateLoadedMsg, so a test App stays at anim{} (frame-0) with no
// watcher goroutine — the byte-identical-goldens + no-leak guarantee.
func (a App) Init() tea.Cmd {
	return tea.Batch(
		animCmds(),
		a.startWatcherCmd(),
		PollDaemonOnline(a.root),
	)
}

// startWatcherCmd builds the retained fsnotify watcher (NewWatcherFor —
// scoped to live/attention + live/outbox + live/confirms + the pane
// dirs; the inbox subscription is identity-scoped via `me`) and returns
// a Cmd that yields a watcherReadyMsg carrying its drain cmd + stop fn.
// A watcher-construction failure degrades to a WatcherErrMsg (soft fail,
// tui.go:185-189): the already-hydrated substrate still renders; only
// live updates are lost. The TUI never bubbles an exit code from a
// read-only watcher.
func (a App) startWatcherCmd() tea.Cmd {
	root, me := a.root, a.me
	return func() tea.Msg {
		_, cmd, stop, err := NewWatcherFor(root, me)
		if err != nil {
			return WatcherErrMsg{Err: err}
		}
		return watcherReadyMsg{cmd: cmd, stop: stop}
	}
}

// loadSubstrateCmd re-reads + re-projects the live substrate off the
// bubbletea goroutine and yields a substrateLoadedMsg. Called by Update
// after a substrate watcher event (the "fold" — but the fold is a full
// deterministic re-read of disk truth via loadSubstrate, so there is no
// dup/order/merge bug class: disk is the single source of truth and
// confirm.ReadAll/EmitCatchUp are idempotent). Pure read; never writes.
// PR-G3: also carries the parallel lineage-id slice (loadSubstrateWithIDs
// — rows + ids from the SAME event pass so they cannot drift) so the
// live decision-row drill-down can resolve projectLineage by id.
func (a App) loadSubstrateCmd() tea.Cmd {
	root, me, now := a.root, a.me, a.now
	return func() tea.Msg {
		// G-interact: also carry the parallel subject slice
		// (loadSubstrateAll — rows + ids + subjects from the SAME event
		// pass so the three cannot drift) so a free-text broadcast can
		// default its subject to the focused entity.
		rows, ids, subjects := loadSubstrateAll(root, me, now)
		return substrateLoadedMsg{rows: rows, ids: ids, subjects: subjects}
	}
}

// loadMeshCmd re-reads + re-projects the live mesh off the bubbletea
// goroutine and yields a meshLoadedMsg (the EXACT mirror of
// loadSubstrateCmd for the mesh). Called by Update after an AttentionMsg
// / routing-affecting watcher event (the "fold" — a full deterministic
// re-read of disk truth via loadMesh, so there is no dup/order/merge bug
// class: disk is the single source of truth and attention.ReadAll /
// deriveMeshEdgesLive are idempotent). Pure read; never writes.
func (a App) loadMeshCmd() tea.Cmd {
	root, me := a.root, a.me
	return func() tea.Msg {
		return meshLoadedMsg{mesh: loadMesh(root, me)}
	}
}

// loadTabsCmd re-reads + re-projects the live channels/goals/memory tab
// state off the bubbletea goroutine and yields a tabsLoadedMsg (the EXACT
// mirror of loadSubstrateCmd / loadMeshCmd for the list tabs). Called by
// Update after a pane watcher event (ChannelMsg/ChannelMessageMsg/
// GoalMsg/InboxMsg — the "fold": a full deterministic re-read of disk
// truth via loadTabs, so there is no dup/order/merge bug class: disk is
// the single source of truth and InitialWalkPanes / channels.LoadMeta /
// goal.ReadAll / walkLearned are idempotent). Pure read; never writes.
// `now` is captured at NewApp time (a.now — the production clock) so the
// memory tab's relative-time column is stable within a session; tests
// inject a pinned tabsLoadedMsg and never reach this cmd.
func (a App) loadTabsCmd() tea.Cmd {
	root, me, now := a.root, a.me, a.now
	return func() tea.Msg {
		return tabsLoadedMsg{tabs: loadTabs(root, me, now)}
	}
}

// Update implements tea.Model. Handles window resize, quit, the 1-5 /
// tab / shift+tab nav, substrate row selection (↑↓/jk), the lineage
// drill-down (enter on a decision row), the help overlay (?), esc
// (close overlay), and the five PR-F animation cadence ticks. `:` is an
// accepted no-op stub this PR (the command palette is later) — it must
// not crash.
//
// Each cadence Msg advances ONLY its own anim counter and RE-ARMS its
// own tick (returns tickEvery(period, sameMsg)) so the cadence is
// perpetual and the five stay independent. The Msgs only ever arrive
// while the bubbletea loop runs; tests deliver them directly to drive
// the frame-progression assertions deterministically.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = m.Width
		a.height = m.Height
		return a, nil
	case tea.KeyMsg:
		// #189 defence-in-depth: drop KeyMsg events that look like
		// mouse-byte fragments leaked by the bubbletea parser. SGR mouse
		// sequences (\x1b[<35;100;20M) are normally captured as MouseMsg
		// (#137, tea.WithMouseCellMotion in cli/tui.go), but a fragmented
		// read at a chunk boundary can split the sequence so that the
		// runtime emits the prefix as KeyMsg{Alt:true,Runes:['[']} and
		// the tail (<35;100;20M) as KeyMsg{Type:KeyRunes,Runes:[...]} —
		// which textarea then inserts as literal characters into the
		// composer buffer (the "occasional escape-sequence garbage" the
		// operator reported intermittently while wheel-scrolling). Filter both
		// fragment shapes BEFORE handleKey so neither nav-keymap nor the
		// composer textarea sees them. The shapes are specific enough
		// (Alt+`[`, Alt+`O`, full `<digits;digits;digitsM` tail, X10 `M`
		// + 3 printable bytes) that real keystrokes do not match.
		if isMouseFragmentLeak(m) {
			return a, nil
		}
		return a.handleKey(m)

	case tea.MouseMsg:
		// #137 mouse-wheel substrate-feed scrollback. With
		// tea.WithMouseCellMotion (cli/tui.go) the wheel is captured here
		// instead of leaking into the terminal's native alt-screen
		// scrollback. ONLY the wheel scrolls; every other mouse event
		// (clicks, motion, drag, other buttons) is a SAFE no-op — the
		// model is returned UNCHANGED so nothing else is swallowed or
		// corrupted (m.Button is not a wheel button ⇒ fall through to the
		// return below). Gated identically to where PgUp/PgDn scroll
		// today: the substrate feed only, no overlay open (an overlay
		// swallows PgUp/PgDn in handleKey, so the wheel matches — no new
		// overlay behaviour). Unlike Home/End (composer readline motion in
		// compose) the wheel is not text-motion, so — exactly like
		// PgUp/PgDn — it scrolls in BOTH nav and compose mode without a
		// mode-hop. Reuses the shared scrollBy seam (one offset/clamp
		// path; windowLines re-clamps the upper bound every paint, the
		// #134 single source of truth) — only the step differs from the
		// keys (scrollWheelStep, a small per-notch nudge, vs scrollPage).
		if a.view == viewSubstrate && a.overlay == overlayNone {
			switch m.Button {
			case tea.MouseButtonWheelUp:
				a.scrollBy(scrollWheelStep) // toward older (like PgUp)
			case tea.MouseButtonWheelDown:
				a.scrollBy(-scrollWheelStep) // toward live (like PgDn, floors at 0)
			}
		}
		return a, nil

	case spinTickMsg:
		a.anim.spin++
		return a, tickEvery(spinPeriod, spinTickMsg{})
	case meshTickMsg:
		a.anim.mesh++
		return a, tickEvery(meshPeriod, meshTickMsg{})
	case typingTickMsg:
		a.anim.typing++
		return a, tickEvery(typingPeriod, typingTickMsg{})
	case seriesTickMsg:
		a.anim.series++
		// v1.0.6.3 (Bundle F): advance the series ring with the REAL
		// events/sec rate observed since the previous tick. eventTickCount
		// has been incremented on each ThoughtMsg / ConfirmMsg /
		// AttentionMsg arrival; tick interval is 500ms so the rate is
		// eventTickCount * 2. Reset the counter for the next tick.
		// Nil-safe: a test that constructs App without NewApp still won't
		// panic.
		rate := a.eventTickCount * 2
		a.eventTickCount = 0
		if a.series == nil {
			a.series = newSeries()
		} else {
			s := *a.series
			s.advanceWithSample(rate)
			a.series = &s
		}
		return a, tickEvery(seriesPeriod, seriesTickMsg{})
	case caretTickMsg:
		a.anim.caret++
		return a, tickEvery(caretPeriod, caretTickMsg{})

	// ── PR-G1: live substrate lifecycle ─────────────────────────────
	//
	// DRAIN INVARIANT (the load-bearing correctness property): the
	// fsnotify watcher delivers ONE Msg per Update via a.watcherCmd (a
	// single blocking channel receive — watch.go:180-188). tea.Program
	// only re-issues a Cmd that is RETURNED, so to keep the stream
	// flowing, EVERY consumed watcher Msg must return a.watcherCmd
	// EXACTLY ONCE — never zero (stream stops), never twice (two
	// outstanding receives → racy double-drain). substrateLoadedMsg is
	// the ONE exception: it is produced by the one-shot loadSubstrateCmd
	// (NOT the watcher), so it MUST NOT re-arm the watcher (that would
	// duplicate the drain). This mirrors tui.go:208-228 verbatim.

	case watcherReadyMsg:
		// The lazily-built watcher is ready: store the drain cmd + stop
		// fn and start the FIRST drain (one outstanding receive).
		a.watcherCmd = m.cmd
		a.watcherStop = m.stop
		return a, a.watcherCmd

	case ThoughtMsg, ConfirmMsg:
		// A broadcast @thought or a @confirm landed → the chat content
		// OR a decision's quorum changed. Re-read+re-project disk truth
		// (loadSubstrateCmd, idempotent) AND re-arm the watcher drain
		// (exactly once). Both in one Batch: independent one-shots.
		//
		// PR-G2: a new @thought under live/outbox/<A>/<id>.gdl can ALSO
		// create a routing-delivery edge (the matching live/inbox copy
		// arrives as the SAME-id file under live/inbox — but the inbox is
		// not its own watched dir here; the broadcast write is the
		// observable signal that routing MAY have changed). Re-read the
		// mesh too so an edge appearing/disappearing reflects without
		// waiting for an attention change. loadMesh is a pure idempotent
		// re-read (outbox∩inbox), so an extra re-read is harmless and
		// keeps the mesh in lock-step with the chat. STILL exactly ONE
		// watcher re-arm (the drain invariant) — the two loaders are
		// independent one-shots batched UNDER the single re-arm.
		//
		// v1.0.6.3 (Bundle F): also bump the events-per-second counter
		// for the substrate-panel sparkline. Reset on each seriesTickMsg.
		a.eventTickCount++
		return a, a.watcherRearmWith(tea.Batch(a.loadSubstrateCmd(), a.loadMeshCmd()))

	case AttentionMsg:
		// PR-G2: a live/attention/<agent>.gdl create/modify → the mesh
		// node set changed (an agent started/updated attending). Re-read
		// + re-project the mesh (loadMeshCmd, idempotent — a full
		// deterministic re-read of attention.ReadAll + deriveMeshEdgesLive,
		// so no dup/merge bug class) AND re-arm the watcher drain EXACTLY
		// once (the drain invariant — never zero: stream stops; never
		// twice: racy double-drain). No substrate re-read (an attention
		// record is not a broadcast @thought; the chat is unchanged).
		//
		// v1.0.6.3 (Bundle F): also bump the events-per-second counter
		// for the substrate-panel sparkline. Reset on each seriesTickMsg.
		a.eventTickCount++
		return a, a.watcherRearmWith(a.loadMeshCmd())

	case ChannelMsg, ChannelMessageMsg, GoalMsg, InboxMsg:
		// PR-G3: a pane write (a channel meta/@say, a goal, an inbox
		// @goal-overlap) landed on the SAME watcher channel → the
		// channels/goals tab content changed. Re-read + re-project disk
		// truth (loadTabsCmd, idempotent — a full deterministic re-read
		// via InitialWalkPanes + walkLearned, so no dup/order/merge bug
		// class) AND re-arm the watcher drain EXACTLY ONCE — the SINGLE
		// re-arm batched with the one-shot tab loader, EXACTLY the G2
		// AttentionMsg→loadMeshCmd precedent (live_mesh §V8G2-MESH1). It
		// MUST still re-arm or the whole stream (incl. ThoughtMsg/
		// ConfirmMsg/AttentionMsg) stalls; the tab loader is an
		// independent one-shot UNDER the single re-arm (NOT a second
		// re-arm — the drain stays exactly-once). No substrate/mesh
		// re-read (a pane write does not change the broadcast chat or the
		// attention/routing mesh — those have their own watcher cases).
		// learned/ is not its own watched dir, so a memory-only change
		// has no pane Msg; the synchronous NewApp hydration + this
		// re-read on the next pane event keep memory eventually-fresh
		// (read-only console; disk-truth re-read on every pane event is
		// the same posture as G1/G2 — no incremental memory watch needed
		// for the locked G3 scope).
		return a, a.watcherRearmWith(a.loadTabsCmd())

	case substrateLoadedMsg:
		// Fold the freshly-projected thread. NOTE: produced by
		// loadSubstrateCmd (a ONE-SHOT) OR injected directly by a test —
		// NOT by the watcher drain, so it MUST NOT re-arm a.watcherCmd
		// (the drain invariant — double-re-arm class bug). selected is
		// re-clamped because the live thread length can change between
		// renders.
		//
		// OPEN-2 (LOCKED 2026-05-16) is applied HERE, the SINGLE seam
		// every substrate row enters through (loadSubstrateCmd output
		// AND direct test/golden injection). projectThread leaves
		// Quorum.Total 0 (G0 deferred the denominator); applyQuorum-
		// Threshold sets it to autopromote.MinDistinctConfirmers (the
		// constant). loadSubstrate also applies it (idempotent — its
		// own unit contract) but a directly-injected substrateLoadedMsg
		// (the gate/golden path) would otherwise render `2/0`, so the
		// fold MUST resolve OPEN-2 too. Idempotent → safe in both paths.
		a.substrate = m.rows
		applyQuorumThreshold(a.substrate)
		// PR-G3: fold the parallel lineage-id carry (same Msg, same
		// order — rows[i] ↔ ids[i] by construction in loadSubstrate-
		// WithIDs). The live loadSubstrateCmd always sets ids; a direct
		// test/gate inject leaves it nil → a.substrateIDs nil → `enter`
		// on a decision row is a guarded no-op (the structural gates do
		// not exercise the drill-down; the dedicated lineage test
		// injects ids / drives off disk). Replaced wholesale (not
		// merged) so it stays exactly parallel to a.substrate after a
		// re-read shortens/lengthens the thread.
		a.substrateIDs = m.ids
		// G-interact: fold the parallel subject carry (same Msg, same
		// order — rows[i] ↔ subjects[i], loadSubstrateAll). The live
		// loadSubstrateCmd always sets it; a direct gate inject leaves it
		// nil → a.substrateSubjects nil → resolveBroadcastSubject uses the
		// documented opSubjectFallback (correct for those gates). Replaced
		// wholesale so it stays exactly parallel after a re-read.
		a.substrateSubjects = m.subjects
		if len(m.rows) > 0 {
			a.sawThought = true
		}
		if a.selected >= len(a.substrate) {
			a.selected = lastRowIndex(a.substrate)
		}
		if a.selected < 0 {
			a.selected = 0
		}
		// #134: re-clamp the scrollback offset to the new thread, mirror
		// of the `selected` re-clamp above. The exact render-line max
		// (n_lines − threadH) is not known here (it depends on width/
		// wrap/threadH at render time — windowLines does that exact clamp
		// every paint), so bound the offset by the new ROW count: each
		// row renders to ≥1 line, so len(a.substrate) is a safe upper
		// bound. This keeps the view put when new events arrive while
		// scrolled and prevents an unbounded stale offset after a shorter
		// re-read. Pure view; no cmd, no watcher/drain interaction.
		if a.scrollOffset > len(a.substrate) {
			a.scrollOffset = len(a.substrate)
		}
		if a.scrollOffset < 0 {
			a.scrollOffset = 0
		}
		return a, nil

	case meshLoadedMsg:
		// Fold the freshly-projected mesh (PR-G2). NOTE: produced by
		// loadMeshCmd (a ONE-SHOT, fired UNDER an already-counted
		// watcher re-arm) OR injected directly by a test — NOT by the
		// watcher drain itself, so it MUST NOT re-arm a.watcherCmd (the
		// drain invariant — the same double-re-arm class bug
		// substrateLoadedMsg avoids; mirrors it exactly). The mesh render
		// is content-agnostic structurally (the panel/border/ROUTING
		// geometry does not depend on node/edge counts) so no clamp/
		// reselection is needed — just swap the projected mesh in.
		a.mesh = m.mesh
		return a, nil

	case tabsLoadedMsg:
		// Fold the freshly-projected channels/goals/memory tab state
		// (PR-G3). NOTE: produced by loadTabsCmd (a ONE-SHOT, fired
		// UNDER the already-counted pane-Msg watcher re-arm) OR injected
		// directly by a test — NOT by the watcher drain itself, so it
		// MUST NOT re-arm a.watcherCmd (the drain invariant — the same
		// double-re-arm class bug substrateLoadedMsg / meshLoadedMsg
		// avoid; mirrors them exactly). The tab renders are content-
		// agnostic structurally (the panel/border geometry does not
		// depend on row counts) so no clamp/reselection is needed — just
		// swap the projected tab state in. channelSel is left as-is
		// (preserved per the re-scope: flipping away and back keeps your
		// place); it is range-guarded at render time (tabs.go:94-96).
		a.tabs = m.tabs
		return a, nil

	case DaemonOnlineMsg:
		// The TUI is a filesystem console — NEVER gated on this. It only
		// drives the quiet chat-chrome offline indicator. Re-arm the
		// poll (tui.go:215-217).
		a.daemonOnline = m.Online
		return a, PollDaemonOnline(a.root)

	case WatcherErrMsg:
		// Soft-log (read-only TUI never bubbles an exit code, tui.go:
		// 218-224). Re-arm so subsequent events still flow — UNLESS the
		// watcher never came up (watcherCmd nil), in which case there is
		// nothing to re-arm.
		return a, a.watcherRearmWith(nil)

	case WatcherClosedMsg:
		// The watcher goroutine exited (shutdown). Drop the cmd so we do
		// not re-issue a closed-channel read forever (tui.go:225-228).
		a.watcherCmd = nil
		return a, nil
	}
	return a, nil
}

// watcherRearmWith returns the Cmd to run after consuming ONE watcher
// Msg: the watcher drain cmd re-armed EXACTLY ONCE (so the stream keeps
// flowing — never dropped, never duplicated) optionally batched with one
// extra one-shot (e.g. loadSubstrateCmd for a substrate re-read). If the
// watcher never came up (watcherCmd nil — construction failed, soft
// fail) only the extra runs. Centralised so every watcher-Msg arm site
// shares the exact same drain discipline (no per-site drift).
func (a App) watcherRearmWith(extra tea.Cmd) tea.Cmd {
	switch {
	case a.watcherCmd != nil && extra != nil:
		return tea.Batch(a.watcherCmd, extra)
	case a.watcherCmd != nil:
		return a.watcherCmd
	default:
		return extra // watcher down → just the extra (may be nil)
	}
}

// quitCmd tears the fsnotify goroutine + watcher down BEFORE tea.Quit
// (so it does not leak past program exit) — mirrors tui.go:402-407 /
// PR-G1's quit path, factored so every quit site (nav `q`, the universal
// `ctrl+c`) shares the exact same teardown.
func (a App) quitCmd() (tea.Model, tea.Cmd) {
	if a.watcherStop != nil {
		a.watcherStop()
		a.watcherStop = nil
	}
	return a, tea.Quit
}

// handleKey is the keypress dispatcher (split out of Update so the
// keymap stays auditable against keys.go).
//
// ── G-interact FOCUS / MODAL MODEL (documented; must not break nav/quit)
//
// The v8 composer is always rendered (PR-E). The substrate view has TWO
// modes; the other tabs are read-only lists (compose is inert there — nav
// keys always work):
//
//   - COMPOSE (a.composeMode true; the DEFAULT — the composer is the
//     console's primary affordance, "always focused in a TUI", handoff
//     §9): keystrokes EDIT the composer's bubbles/textarea (composeTA —
//     the full readline set: Ctrl+U/K, Ctrl+W & Alt+Backspace,
//     Ctrl+A/E, Ctrl+Left/Right & Alt+B/F word motion,
//     left/right/home/end, backspace/delete, cursor). Only four keys
//     are INTERCEPTED before the textarea (the v8 composer's modal
//     contract differs from textarea's defaults): `esc` → drop to NAV
//     mode; ⏎ → parse + emit composeTA.Value() (broadcast / @directed /
//     /slash) then Reset(); ⇧⏎ (shift+enter) or ctrl+j → insert a
//     newline (multi-line buffer; the RENDERED composer stays fixed at
//     ComposerHeight — the documented fixed-height contract); KeySpace
//     → insert a literal space (robust to the headless empty-Runes
//     KeySpace shape). Everything else is delegated to textarea.Update.
//     See handleComposeKey + the composeTA field comment.
//   - NAV (a.composeMode false): the EXISTING keymap UNCHANGED — 1-5 /
//     tab / shift+tab / ↑↓jk / enter→lineage / esc(close overlay) / ? /
//     `:`. PLUS `i` → return to COMPOSE; `c`/`r` → the contextual
//     one-key confirm/refute of the selected decision row (the
//     affordance that "falls out cleanly"; the slash form is the
//     requirement, this is the bonus).
//
// QUIT CONTRACT (the load-bearing constraint):
//
//   - `ctrl+c` ALWAYS quits, in EVERY mode/overlay — the universal,
//     always-available quit (the documented "obvious alternative").
//   - `q` quits in NAV mode and on every non-substrate view. In COMPOSE
//     mode on the substrate `q` is a LITERAL character typed into the
//     buffer — the deliberate, documented exception (you cannot both
//     type the letter q and have it quit; `ctrl+c` is the obvious
//     always-available alternative, and `esc` then `q` also quits). This
//     is the ONLY nav/quit deviation and it is intentional.
//
// Overlays (help / lineage) still swallow input exactly as before and
// take precedence over compose; `ctrl+c` still quits over an overlay.
func (a App) handleKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := m.String()

	// `ctrl+c` ALWAYS quits — every mode, every overlay (the universal
	// quit; the documented obvious alternative to `q`). PR-G1 teardown.
	if key == "ctrl+c" {
		return a.quitCmd()
	}

	// Overlay-focused input: any overlay swallows navigation. The help
	// overlay closes on ?/esc/any key (matches PR-C / old-TUI help
	// dismissal). The lineage drill-down closes on esc only (so a
	// stray key while reading the chain doesn't lose your place).
	// (ctrl+c already handled above — it quits even over an overlay.)
	if a.overlay == overlayHelp {
		a.overlay = overlayNone
		return a, nil
	}
	if a.overlay == overlayLineage {
		if key == "esc" {
			a.overlay = overlayNone
		}
		return a, nil
	}

	// COMPOSE mode (substrate view only): the composer has focus. Typing
	// goes to the buffer; ⏎ sends; ⇧⏎ newlines; esc → nav. `q` here is a
	// literal char (the documented quit exception — ctrl+c still quits).
	if a.composeMode && a.view == viewSubstrate {
		return a.handleComposeKey(m, key)
	}

	// NAV mode (and every non-substrate view): `q` quits (the normal
	// quit; ctrl+c also quits, handled above). In compose mode on
	// substrate `q` never reaches here (it is a literal — see above).
	if key == "q" {
		return a.quitCmd()
	}

	switch key {
	case "i":
		// Return to COMPOSE (nav→compose). Only meaningful on the
		// substrate view (the composer is substrate-only); a harmless
		// no-op switch elsewhere (compose is inert on read-only tabs).
		a.composeMode = true
		a.composeNote = ""
		return a, nil
	case "c", "r":
		// Contextual one-key confirm/refute of the selected decision row
		// (the affordance that "falls out cleanly" — the /confirm /refute
		// slash forms are the requirement, this is the welcome bonus).
		// Substrate view only; no-op on the read-only tabs.
		if a.view == viewSubstrate {
			return a.contextualVote(key == "c")
		}
		return a, nil
	case "?":
		a.overlay = overlayHelp
		return a, nil
	case ":":
		// Command palette is a later PR — accepted no-op stub (must not
		// crash). Intentionally does nothing this PR.
		return a, nil
	case "esc":
		// No overlay open — nothing to close. No-op.
		return a, nil
	case "1":
		return a.switchView(viewSubstrate), nil
	case "2":
		return a.switchView(viewFleet), nil
	case "3":
		return a.switchView(viewChannels), nil
	case "4":
		return a.switchView(viewGoals), nil
	case "5":
		return a.switchView(viewMemory), nil
	case "tab":
		return a.cycleView(1), nil
	case "shift+tab":
		return a.cycleView(-1), nil
	case "pgup", "pgdown", "home", "end":
		// #134 substrate-feed scrollback (NAV mode). Plain up/down/k/j
		// are UNCHANGED below (they move `selected`); the dedicated
		// scrollback keys move the render window instead. Substrate view
		// only — a harmless no-op on the read-only tabs (windowLines
		// re-clamps every paint and the tab panels never read
		// scrollOffset). Mirrors the compose-mode intercept so the
		// operator can scroll the live debate in EITHER mode.
		if a.view == viewSubstrate {
			a.applyScrollKey(key)
		}
		return a, nil
	case "up", "k":
		if a.view == viewSubstrate && a.selected > 0 {
			a.selected--
		}
		return a, nil
	case "down", "j":
		// PR-G1: bound on the LIVE thread length, not the fixture.
		if a.view == viewSubstrate && a.selected < len(a.substrate)-1 {
			a.selected++
		}
		return a, nil
	case "enter":
		// PR-G3: open the lineage drill-down on a decision row. The
		// payload is resolved LIVE via the G0 projectLineage(root,
		// <that row's thought-id>) VERBATIM — the id is the parallel
		// carry a.substrateIDs[selected] (loadSubstrateWithIDs;
		// data-mapping §1 :115). Two paths, both honoured:
		//
		//  (a) LIVE: a real decision row → resolve projectLineage by
		//      its real thought-id and stash a.lineage; open the
		//      overlay only if the resolve SUCCEEDS (a decision with no
		//      @reason chain / a transient read error → no drill-down
		//      rather than an empty/garbled box — the read-only console
		//      degrades, never crashes; G0 propagates the error, the
		//      live read path swallows it here like everywhere else);
		//  (b) FIXTURE: the structural-gate / regression path injects
		//      SubstrateThread whose decision row carries an eager
		//      row.Lineage and NO id (ids nil) — fall back to that so
		//      those gates + the existing TestAppGoldenLineage stay
		//      green unchanged.
		//
		// G0 projectThread / projectLineage are REUSED, not
		// reimplemented (project*.go byte-unchanged); the id thread-
		// through does not mutate G0.
		if a.view == viewSubstrate {
			row := a.currentRow()
			if row != nil && row.Role == roleDecision {
				if id := a.currentRowID(); id != "" {
					if dl, err := projectLineage(a.root, id); err == nil && dl != nil {
						a.lineage = dl
						a.overlay = overlayLineage
					}
					// resolve failed (no chain / transient IO) -> no
					// drill-down (degrade, do not crash/blank).
				} else if row.Lineage != nil {
					// Fixture path (injected SubstrateThread, no id):
					// keep the eager payload so the gates/golden stay
					// green unchanged.
					a.lineage = row.Lineage
					a.overlay = overlayLineage
				}
			}
		}
		return a, nil
	}
	return a, nil
}

// handleComposeKey is the COMPOSE-mode keypress handler (substrate view,
// a.composeMode true). The composer has focus: keystrokes EDIT the
// bubbles/textarea (the full readline set — Ctrl+U/K, Ctrl+W &
// Alt+Backspace, Ctrl+A/E, Ctrl+Left/Right & Alt+B/F word motion,
// left/right/home/end, backspace/delete, cursor); ⏎ parses + emits; ⇧⏎
// inserts a newline; esc → nav. `ctrl+c` is already handled (always
// quits) before this is reached; `q` here is a LITERAL char (the
// documented quit exception — see handleKey's QUIT CONTRACT — it is just
// an ordinary rune to the textarea). Returns the model + any post-write
// reload cmd (the snappy-feedback one-shot — it NEVER re-arms the
// watcher; same exception class as substrateLoadedMsg).
//
// WHY the three intercepts come BEFORE the textarea: the v8 composer's
// modal contract differs from textarea's defaults — Enter is SEND here
// (textarea would InsertNewline), Shift+Enter/Ctrl+J is the newline
// (the composer hint advertises `⇧⏎ newline`), and Esc is the
// compose→nav focus toggle. Everything else (the entire readline
// keymap + printable runes + space) is delegated to textarea.Update so
// the standard console line-editing "just works" — that delegation IS
// the feature.
func (a App) handleComposeKey(m tea.KeyMsg, keyStr string) (tea.Model, tea.Cmd) {
	// Any keystroke clears the stale result/error note (it pinned the
	// LAST action; a new edit starts fresh).
	a.composeNote = ""

	switch keyStr {
	case "pgup", "pgdown":
		// #134 substrate-feed scrollback, intercepted BEFORE the textarea
		// (alongside esc/enter/⇧⏎) so the operator can scroll the live
		// debate WITHOUT mode-hopping out of compose. ONLY PgUp/PgDn are
		// intercepted in compose mode: Home/End are LOAD-BEARING composer
		// readline motion here (line-start / line-end cursor — the
		// documented textarea-delegated editing set above, locked by
		// TestComposerEdit_Readline "home/end motion"); stealing them in
		// compose would weaken that existing contract. PgUp/PgDn carry
		// the full scroll range (older/newer a page at a time), and
		// Home/End jump-to-extremes are available in NAV mode (`esc`),
		// where Home/End were previously unbound — so scrollback works in
		// BOTH modes without restructuring the composer or its tests.
		// Pure view — moves the render window only; the buffer is
		// untouched.
		a.applyScrollKey(keyStr)
		return a, nil
	case "esc":
		// Drop to NAV mode (the documented compose→nav toggle). The
		// buffer is PRESERVED (you can nav, then `i` back and keep
		// typing) — esc is a focus switch, not a discard. NOT routed to
		// the textarea (it has no esc binding; keeping it here makes the
		// modal contract explicit).
		a.composeMode = false
		return a, nil
	case "enter":
		// SEND: parse the buffer + emit via the lib-backed path, then
		// clear. A bad parse/validation renders an in-pane note (never a
		// crash / exit code) and PRESERVES the buffer so the operator can
		// fix it. A successful write returns the post-write reload cmd.
		// Intercepted BEFORE the textarea (whose Enter binding is
		// InsertNewline — wrong for the composer; ⇧⏎ is our newline).
		return a.composeSend()
	case "shift+enter", "ctrl+j":
		// ⇧⏎ newline (the composer hint advertises `⇧⏎ newline`). Some
		// terminals deliver shift+enter as ctrl+j — accept both. A real
		// multi-line buffer (newlines split logical lines); the RENDER
		// stays ComposerHeight (only the cursor's logical line shows —
		// the documented fixed-height contract; renderComposerLive caps
		// it). InsertRune is the textarea's own newline-insert at the
		// cursor (NOT a string append) so the cursor + multi-line edit
		// stay consistent.
		ta := a.composeTA
		ta.InsertRune('\n')
		a.composeTA = ta
		return a, nil
	}

	// Space: insert a literal space rune. WHY explicit (not just routed
	// to textarea.Update): tea delivers space as KeyMsg{Type: KeySpace}
	// whose Runes the runtime sets to []rune{' '} — BUT the headless test
	// harness (and historically this handler) constructs
	// KeyMsg{Type: tea.KeySpace} with EMPTY Runes; textarea.Update's
	// default-case rune-insert would then insert nothing (the prior
	// hand-buffer had its own `case tea.KeySpace: += " "` for exactly
	// this reason). Normalising to an InsertRune(' ') here is robust
	// whether or not Runes is populated, preserving the prior space
	// behaviour byte-for-byte while the rest of editing is the textarea.
	if m.Type == tea.KeySpace {
		ta := a.composeTA
		ta.InsertRune(' ')
		a.composeTA = ta
		return a, nil
	}

	// Everything else — the FULL readline editing set (Ctrl+U/K, Ctrl+W &
	// Alt+Backspace, Ctrl+A/E, Ctrl+Left/Right & Alt+B/F word motion,
	// left/right/home/end, backspace/delete) + printable runes + space —
	// is delegated to textarea.Update. The textarea is FOCUSED (handoff
	// §9, set in newComposerTextarea) so it accepts the input; that
	// delegation IS the standard-console-line-editing feature. A named/
	// control key the textarea does not bind (tab, etc.) is a harmless
	// textarea no-op — it must not leak to nav (compose is modal), which
	// is exactly textarea.Update's behaviour (unknown keys fall to its
	// default rune-insert with empty Runes ⇒ nothing inserted). The
	// returned cmd is the textarea's cursor-blink cmd; the v8 render uses
	// its OWN blink-caret (anim.caret) so we DISCARD it (wiring a second
	// blink loop would double-tick; the v8 caret semantics are preserved
	// — see renderComposerLive). App is a value receiver: copy the
	// textarea, Update it, assign back.
	ta := a.composeTA
	ta, _ = ta.Update(m)
	a.composeTA = ta
	return a, nil
}

// scrollPage is the PgUp/PgDn jump size in RENDERED lines. The exact
// visible feed height (threadH) is computed deep in renderChatPanel from
// panel geometry and is not cheaply available at the key-handler level;
// a fixed near-screenful is the spec-sanctioned fallback (windowLines
// re-clamps to the true content every paint so an over-large jump just
// lands at the oldest window — never out of range).
const scrollPage = 10

// scrollWheelStep is the per-notch mouse-wheel jump in RENDERED lines
// (#137). Deliberately small (a few lines, not a near-screenful like
// scrollPage) so a trackpad/wheel flick scrolls the feed smoothly rather
// than leaping a page per notch. windowLines re-clamps to the true
// content every paint, so an accumulated over-large offset just lands at
// the oldest window — never out of range (same property scrollPage
// relies on).
const scrollWheelStep = 3

// isMouseFragmentLeak returns true when a tea.KeyMsg looks like a
// fragmented mouse-byte sequence the bubbletea parser emitted as a
// KeyMsg instead of a MouseMsg. Defence-in-depth for #189: SGR mouse
// events (\x1b[<35;100;20M) are normally captured by tea.MouseMsg
// (#137, tea.WithMouseCellMotion in cli/tui.go), but if a single Read()
// boundary splits the sequence the runtime falls through to its
// generic rune path and produces TWO KeyMsg events:
//
//   - prefix: KeyMsg{Alt: true, Runes: ['[']} (the \x1b[ head)
//   - tail:   KeyMsg{Type: KeyRunes, Runes: ['<','3','5',';',...,'M']}
//
// Either of those, delivered to the composer textarea, gets inserted as
// literal characters (textarea sanitises \x1b away but keeps the
// printable `[`, `<`, digits, `;`, `M`) — the "occasional escape-
// sequence garbage" the operator reported intermittently while wheel-scrolling.
// The patterns below are deliberately specific (Alt+`[`, Alt+`O`, a
// full SGR-tail `<\d+;\d+;\d+[Mm]`, an X10 tail `M` + exactly 3
// printable bytes) so real keystrokes (`<3`, `;)`, `M`, etc.) never
// match. Returns true → caller drops the message (return a, nil) before
// any view sees it.
//
// # Implementation anchor
//
// Validated against bubbletea v1.3.10; see `key.go:680-708` for the
// runtime path that converts an unrouted CSI/SS3 sequence into the
// alt-prefix + rune-tail pair this filter catches.
// If a future bubbletea version changes its alt-introducer behaviour,
// re-run the chunk-boundary tests (TestMouseFragmentPrefixDoesNotLeak…,
// TestMouseFragmentTailDoesNotLeak…, TestX10MouseFragmentDoesNotLeak…)
// and update the prefix patterns to match the new shape.
// If a real keystroke ever gets dropped, add a new `false` case to
// TestRealKeystrokesStillReachComposer / TestIsMouseFragmentLeakUnitMatrix
// so the regression is pinned at the unit layer.
//
// # Known trade-offs
//
//   - SGR paste false-positive: a literal `<\d+;\d+;\d+[Mm]` payload
//     (e.g. `<1;2;3M`, `<0;0;0m`, math expressions of that exact shape)
//     pasted as a single KeyRunes event will be dropped. The runtime
//     has already routed the bytes to KeyMsg (not MouseMsg), so we have
//     only shape to work with at this layer — a check loose enough to
//     admit `<1;2;3M` as "real typing" would also admit a genuine
//     fragment. Pinned by TestKnownFalsePositive_SGRPasteShape.
//   - X10 paste false-positive: 4-rune `M`+3-printable bursts that look
//     like English words ("Mary", "Mike") or short alphanumeric pastes
//     ("M123", "MABC") arriving as a single KeyRunes event will be
//     dropped. Pinned by TestKnownFalsePositive_X10Paste; the X10
//     branch carries a TODO to remove it if we ever go SGR-only.
//   - Alt+[ collision: a vim user pressing Esc then `[` in quick
//     succession could in principle look like the CSI prefix orphan
//     this filter catches. The compose keymap does not bind Alt+[, so
//     dropping it is acceptable, but it is worth flagging here so a
//     future debugger does not chase a phantom regression.
func isMouseFragmentLeak(m tea.KeyMsg) bool {
	// Prefix shape: KeyMsg{Alt: true, Type: KeyRunes, Runes: [c]} where
	// c is the CSI/SS3 introducer (`[` or `O`). Alt-modified single `[`
	// has no legitimate binding in nav or compose; same for Alt-`O`.
	if m.Alt && m.Type == tea.KeyRunes && len(m.Runes) == 1 {
		switch m.Runes[0] {
		case '[', 'O':
			return true
		}
	}
	// Tail shape: KeyMsg{Type: KeyRunes, Alt: false} whose runes spell
	// out a residual mouse-event payload. Two terminal encodings:
	//
	//   SGR: `<` + digits/semicolons + `M`|`m` (e.g. <35;100;20M).
	//        Requires at least one digit before the M/m and at least one
	//        `;` (every real SGR mouse event has three params).
	//   X10: `M` + exactly 3 printable bytes (button, col, row encoded
	//        as printable chars +32). Length is exactly 4.
	if m.Type == tea.KeyRunes && !m.Alt && len(m.Runes) >= 4 {
		runes := m.Runes
		// SGR tail: leading `<`, trailing `M`/`m`, body is digits + `;`.
		if runes[0] == '<' && (runes[len(runes)-1] == 'M' || runes[len(runes)-1] == 'm') {
			sawDigit, sawSemi := false, false
			body := runes[1 : len(runes)-1]
			ok := true
			for _, r := range body {
				switch {
				case r >= '0' && r <= '9':
					sawDigit = true
				case r == ';':
					sawSemi = true
				default:
					ok = false
				}
				if !ok {
					break
				}
			}
			if ok && sawDigit && sawSemi {
				return true
			}
		}
		// X10 tail: `M` + 3 printable ASCII bytes (button/col/row each
		// encoded as a single byte with offset 32). Exactly 4 runes.
		//
		// TODO: remove if SGR-only. Modern terminals emit SGR mouse
		// reports (the branch above) and effectively never emit X10. This
		// branch is the larger false-positive surface of the filter: any
		// 4-rune `M`+3-printable paste collides ("Mary", "M123", "MABC",
		// "Mike" — all pinned by TestKnownFalsePositive_X10Paste).
		// Tightening the button byte to a realistic X10 range
		// (0x20-0x5F) helps for some pastes (`Mary`, `Mike`) but cannot
		// distinguish e.g. `M123` from a real X10 event without context
		// the runtime has already discarded — so we keep the broader
		// check until either (a) we confirm no SGR-only deployment paths
		// need this guard and remove the branch outright, or (b) we
		// surface richer routing context and can tighten it without the
		// pinned trade-off above.
		if len(runes) == 4 && runes[0] == 'M' {
			printable := true
			for _, r := range runes[1:] {
				if r < 0x20 || r > 0x7e {
					printable = false
					break
				}
			}
			if printable {
				return true
			}
		}
	}
	return false
}

// scrollBy is the SINGLE relative-offset seam shared by PgUp/PgDn (#134)
// and the mouse wheel (#137): it nudges scrollOffset by delta lines
// (delta>0 = toward older, delta<0 = toward live) and applies the only
// hard lower bound (0 = the live tail). The UPPER bound is intentionally
// NOT applied here — windowLines re-clamps to the true rendered max
// every paint (the single source of truth via chatScrollMax), exactly as
// PgUp always relied on. Behaviour-identical to the previous inline
// math: +scrollPage never hits the floor (old PgUp), -scrollPage floors
// at 0 (old PgDn). One place does the offset arithmetic — the wheel
// reuses it rather than duplicating clamp/offset logic.
func (a *App) scrollBy(delta int) {
	a.scrollOffset += delta
	if a.scrollOffset < 0 {
		a.scrollOffset = 0
	}
}

// applyScrollKey moves the substrate-feed scrollback window (#134). It
// is the SINGLE offset-math seam, called from BOTH the NAV switch and
// the compose-mode intercept so the four keys behave identically
// regardless of mode (PgUp = older, PgDn = newer, Home = oldest, End =
// live). PgUp/PgDn delegate to scrollBy (the shared relative seam the
// #137 wheel also uses — one offset arithmetic, no duplicate clamp).
// scrollOffset is the lines scrolled UP from the live tail; the lower
// bound is 0 (live), the upper bound is the real clamped max
// (chatScrollMax — exactly what windowLines enforces every paint). Pure
// view — App is a value receiver, the caller assigns the returned copy
// back.
func (a *App) applyScrollKey(key string) {
	switch key {
	case "pgup":
		a.scrollBy(scrollPage)
	case "pgdown":
		a.scrollBy(-scrollPage)
	case "home":
		// Oldest: store the REAL clamped maximum, not a 1<<30 sentinel.
		// The sentinel rendered correctly (windowLines re-clamps every
		// paint) but left the STORED offset gigantic, so a subsequent
		// PgDn (scrollOffset -= scrollPage) was an invisible no-op for
		// ~1e8 presses — Home then PgDn looked frozen. chatScrollMax
		// derives the bound from the SAME rendered thread + threadH the
		// paint feeds windowLines (single source of truth via
		// chatThreadGeom), so it is the exact value windowLines would
		// clamp to — and correct even when events wrap to multiple
		// rendered lines (the true max = renderedLineCount−threadH,
		// which can EXCEED the row count len(a.substrate); a naive
		// row-count cap would stop Home short of the oldest wrapped
		// line). A subsequent PgDn now pages back by exactly scrollPage.
		a.scrollOffset = a.chatScrollMax()
	case "end":
		// Live tail (exactly the pre-#134 render — offset 0).
		a.scrollOffset = 0
	}
}

// chatScrollMax is the real upper bound for scrollOffset: the largest
// offset windowLines will accept for the CURRENT frame's rendered feed.
// It reproduces the View→renderBody outer-width decision via the shared
// chatPanelOuterWidth seam, then the shared chatThreadGeom seam (the
// EXACT chrome/composer/threadH/pre-window-thread the paint computes),
// and applies maxScrollOffset — the same n−maxRows clamp windowLines
// uses internally. Because every input is the SAME computation the paint
// performs (not a reimplementation), the value is provably identical to
// what windowLines clamps to at render time, including under word-wrap
// (renderChatSelectedAt wraps long events so renderedLineCount can far
// exceed len(a.substrate); maxScrollOffset is measured on that exact
// rendered string, so Home reaches the absolute-oldest rendered line).
// Pure view; no state mutation, no cmd.
func (a App) chatScrollMax() int {
	w := a.width
	h := a.height
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	// Mirror View(): body height = total − header − footer − (offline hint, if any).
	hintH := 0
	if hint := a.renderOfflineHint(w); hint != "" {
		hintH = lipgloss.Height(hint)
	}
	bodyH := h - lipgloss.Height(a.renderHeader(w)) - lipgloss.Height(a.renderFooter(w)) - hintH
	if bodyH < 1 {
		bodyH = 1
	}
	// Mirror renderBody(): the chat panel's outer width is the shared
	// split decision (two-panel vs single-panel fallback). innerW is the
	// body width after the side gutters.
	innerW := w - 2*bodyGutter
	if innerW < 1 {
		innerW = 1
	}
	chatOuter := chatPanelOuterWidth(innerW)
	// chatThreadGeom is the SAME seam renderChatPanel uses to produce the
	// pre-window thread + threadH; maxScrollOffset is the SAME clamp
	// windowLines applies. So this == what the paint clamps to.
	_, _, thread, threadH := a.chatThreadGeom(chatOuter, bodyH)
	return maxScrollOffset(thread, threadH)
}

// composeSend parses the composer buffer and emits the right substrate
// record via the lib-backed path (live_write.go), then clears the buffer
// on success. The approved routing (locked 2026-05-16):
//
//  1. `/cmd …`  → a foundational slash command (the 7-verb set).
//  2. `@agent …`→ a directed message (emitDirected: reuse-or-summon).
//  3. plain text→ a broadcast operator @thought (emitThought; type=focus,
//     scope=fleet, subject = the resolved focused entity).
//
// On a successful write the buffer is cleared, an in-pane ✓ note is set,
// and the post-write reload cmd is returned (snappy feedback — the
// watcher fold ALSO catches it; loadSubstrate re-reads disk wholesale &
// idempotently so the immediate reload + the watcher both catching it
// cannot double-insert). On a parse/validation error the buffer is
// PRESERVED and the error is rendered in-pane (never a crash / exit).
func (a App) composeSend() (tea.Model, tea.Cmd) {
	// The buffer is the textarea's value now (composeText() — the single
	// read seam; Reset() is the single clear seam, used on success / a
	// whitespace-only buffer).
	raw := a.composeText()
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		// Empty send: no-op (do not write an empty thought; no note —
		// nothing happened). Reset any trailing whitespace-only buffer.
		ta := a.composeTA
		ta.Reset()
		a.composeTA = ta
		return a, nil
	}

	var note string
	var err error
	switch {
	case strings.HasPrefix(trimmed, "/"):
		note, err = a.runSlash(trimmed)
	case strings.HasPrefix(trimmed, "@"):
		note, err = a.runDirected(trimmed)
	default:
		// Plain text → broadcast operator @thought. Subject = the
		// resolved focused entity (selected row's subject → most-recent
		// thread subject → opSubjectFallback). Authored as `me`.
		subj := a.resolveBroadcastSubject()
		err = emitThought(a.root, a.me, subj, trimmed)
		if err == nil {
			note = "broadcast ✓ (focus · fleet · " + subj + ")"
		}
	}

	if err != nil {
		// In-pane error — buffer PRESERVED so the operator can fix it.
		// Never a crash, never an exit code (the read-only console
		// posture extended to writes).
		a.composeNote = "✗ " + err.Error()
		return a, nil
	}
	// Success: clear the buffer (Reset — also re-homes the cursor to 0,0,
	// the correct post-send empty state), set the ✓ note, and reload for
	// snappy feedback (the watcher ALSO catches it — idempotent wholesale
	// re-read; cannot double-insert).
	ta := a.composeTA
	ta.Reset()
	a.composeTA = ta
	a.composeNote = note
	return a, a.postWriteReloadCmd()
}

// runDirected handles `@agent <text>` — a directed message to that agent
// (emitDirected: say into a reusable open channel, else summon — the
// honest summon→accept handshake; see live_write.go). Returns the
// in-pane note + any error.
func (a App) runDirected(s string) (string, error) {
	body := strings.TrimPrefix(s, "@")
	agent, text, _ := strings.Cut(body, " ")
	agent = strings.TrimSpace(agent)
	text = strings.TrimSpace(text)
	if agent == "" || text == "" {
		return "", &rufioerr.InvalidContentError{Field: "@agent <message>"}
	}
	return emitDirected(a.root, a.me, agent, text)
}

// runSlash parses a `/cmd …` foundational slash command and emits via the
// lib-backed path. The set is EXACTLY the 7 approved verbs (no tier-3/4
// creep): /confirm /refute /attend /goal /observe /summon /say. /confirm
// & /refute with NO id argument act on the CURRENTLY-SELECTED substrate
// row (the G3 row-id carry currentRowID() — the demo centerpiece).
// Argument parsing is minimal (the approved "parse minimally"); a bad
// command / bad args returns a clean error (rendered in-pane, never a
// crash / exit code).
func (a App) runSlash(s string) (string, error) {
	cmd, rest, _ := strings.Cut(strings.TrimPrefix(s, "/"), " ")
	rest = strings.TrimSpace(rest)
	switch cmd {
	case "confirm":
		// /confirm [id] — no id ⇒ the selected row's thought-id (the
		// demo centerpiece: confirm the selected DECISION → quorum
		// advances live). evidence: the remainder after the id, if any.
		id, ev := a.slashTargetAndRest(rest)
		if id == "" {
			return "", &rufioerr.InvalidContentError{Field: "/confirm needs a selected decision row or an id"}
		}
		if err := emitConfirm(a.root, a.me, id, ev); err != nil {
			return "", err
		}
		return "confirmed ✓ " + id, nil
	case "refute":
		// /refute [id] <reason> — no id ⇒ the selected row. The
		// remainder after the (optional) id is the required reason.
		id, reason := a.slashTargetAndRest(rest)
		if id == "" {
			return "", &rufioerr.InvalidContentError{Field: "/refute needs a selected decision row or an id"}
		}
		if err := emitRefute(a.root, a.me, id, reason, ""); err != nil {
			return "", err
		}
		return "refuted ✓ " + id, nil
	case "attend":
		// /attend <intent> | <entity[,entity…]> — minimal: split on `|`.
		intent, entCSV, ok := strings.Cut(rest, "|")
		if !ok {
			return "", &rufioerr.InvalidContentError{Field: "/attend <intent> | <entities,csv>"}
		}
		ents := splitCSV(entCSV)
		if err := emitAttend(a.root, a.me, strings.TrimSpace(intent), ents); err != nil {
			return "", err
		}
		return "attention set ✓", nil
	case "goal":
		// /goal <statement> — minimal (by/scope use lib defaults).
		// Intentional per-verb scope default: emitGoal's "" → scope=agent
		// (mirrors cli/goal.go:61's own --scope flag default — a goal is
		// agent-owned by default). NOT an inconsistency with /observe
		// below (which defaults to fleet — each verb mirrors its OWN
		// lib/CLI default; see emitGoal/emitObserve in live_write.go).
		if err := emitGoal(a.root, a.me, rest, "", ""); err != nil {
			return "", err
		}
		return "goal declared ✓", nil
	case "observe":
		// /observe <subject> <predicate> <object…> (s p o — the CLI
		// triple; minimal whitespace split, object is the remainder).
		// Intentional per-verb scope default: emitObserve's "" →
		// scope=fleet (a TUI observe is fleet-visible like a broadcast —
		// documented in emitObserve). Deliberately DIFFERENT from /goal
		// above (scope=agent) — each verb mirrors its OWN lib default, not
		// a shared one; this divergence is by design, not a bug.
		subj, pr2 := nextToken(rest)
		pred, obj := nextToken(pr2)
		if subj == "" || pred == "" || strings.TrimSpace(obj) == "" {
			return "", &rufioerr.InvalidContentError{Field: "/observe <subject> <predicate> <object>"}
		}
		if err := emitObserve(a.root, a.me, subj, pred, strings.TrimSpace(obj), ""); err != nil {
			return "", err
		}
		return "observation set ✓", nil
	case "summon":
		// /summon <agent> <topic> <intent…> (minimal: agent, topic,
		// then the remainder is the intent).
		agent, r2 := nextToken(rest)
		topic, intent := nextToken(r2)
		if agent == "" || topic == "" || strings.TrimSpace(intent) == "" {
			return "", &rufioerr.InvalidContentError{Field: "/summon <agent> <topic> <intent>"}
		}
		if err := emitSummon(a.root, a.me, agent, topic, strings.TrimSpace(intent)); err != nil {
			return "", err
		}
		return "summoned ✓ @" + agent, nil
	case "say":
		// /say <channel> <message…> (minimal: channel id, then body).
		ch, body := nextToken(rest)
		if ch == "" || strings.TrimSpace(body) == "" {
			return "", &rufioerr.InvalidContentError{Field: "/say <ch-id> <message>"}
		}
		if err := emitSay(a.root, a.me, ch, strings.TrimSpace(body)); err != nil {
			return "", err
		}
		return "said ✓ " + ch, nil
	default:
		// Unknown verb — clean in-pane error, NOT a crash / exit code.
		// Explicitly NOT tier-3/4 (no structured admin / swarm / parity).
		return "", &rufioerr.InvalidContentError{Field: "unknown /" + cmd + " (valid: /confirm /refute /attend /goal /observe /summon /say)"}
	}
}

// slashTargetAndRest splits a /confirm|/refute argument string into an
// (id, rest) pair. If `rest` starts with a thought-id-shaped token
// (contains a '-' and no spaces before it — the <unix-millis>-<rand6>
// shape), that is the explicit target and the remainder is evidence/
// reason. Otherwise the WHOLE string is the rest and the id falls back to
// the CURRENTLY-SELECTED substrate row's thought-id (currentRowID() — the
// G3 carry; the demo "confirm the selected DECISION row" path).
func (a App) slashTargetAndRest(rest string) (id, after string) {
	first, tail := nextToken(rest)
	if first != "" && strings.Contains(first, "-") {
		// Looks like an explicit thought-id (id-shaped).
		// known limitation: see V8GI-M1 — any first token containing '-'
		// is treated as an id, so a pathological hyphenated evidence/
		// reason word with no selected row is misparsed → a clean
		// retract.Lookup in-pane error (no integrity risk; buffer
		// preserved). Acceptable under the approved "parse minimally"
		// scope; tighten to a real id-shape check in a later polish PR.
		return first, strings.TrimSpace(tail)
	}
	// No explicit id → the selected row (the contextual demo path).
	return a.currentRowID(), rest
}

// contextualVote is the one-key c/r affordance (nav mode): confirm
// (yes==true) or refute the SELECTED decision row. It reuses the SAME
// currentRowID() carry + the SAME emit funcs as /confirm //refute (the
// slash form is the requirement; this is the welcome bonus). A refute
// needs a reason — the one-key form supplies a minimal documented stock
// reason ("refuted via console") since nav mode has no text entry; the
// /refute slash form is the full-control path. In-pane note on success/
// error; never a crash.
func (a App) contextualVote(yes bool) (tea.Model, tea.Cmd) {
	row := a.currentRow()
	if row == nil || row.Role != roleDecision {
		a.composeNote = "✗ select a decision row first"
		return a, nil
	}
	id := a.currentRowID()
	if id == "" {
		a.composeNote = "✗ no thought-id for the selected row"
		return a, nil
	}
	var err error
	if yes {
		err = emitConfirm(a.root, a.me, id, "")
	} else {
		err = emitRefute(a.root, a.me, id, "refuted via console", "")
	}
	if err != nil {
		a.composeNote = "✗ " + err.Error()
		return a, nil
	}
	if yes {
		a.composeNote = "confirmed ✓ " + id
	} else {
		a.composeNote = "refuted ✓ " + id
	}
	return a, a.postWriteReloadCmd()
}

// resolveBroadcastSubject is the approved context-aware subject for a
// plain free-text broadcast (locked 2026-05-16): the entity currently in
// focus —
//
//  1. the SELECTED substrate row's subject if a row is selected & has one
//     (e.g. customer:5821 — via the parallel substrateSubjects carry);
//  2. else the MOST-RECENT thread row's subject;
//  3. else the documented general fallback opSubjectFallback
//     ("fleet:general" — the entity-form constant; see its definition in
//     live_write.go for why a bare `general` fails the shared validator).
func (a App) resolveBroadcastSubject() string {
	if s := a.selectedRowSubject(); s != "" {
		return s
	}
	if s := a.lastRowSubject(); s != "" {
		return s
	}
	return opSubjectFallback
}

// selectedRowSubject returns the selected row's subject from the parallel
// substrateSubjects carry (loadSubstrateWithIDs), or "" (cold-start /
// no-subject row / a directly test-injected msg that carried no
// subjects). Length-guarded independently so a parallel-slice skew can
// never panic (the currentRowID() discipline).
func (a App) selectedRowSubject() string {
	if a.selected < 0 || a.selected >= len(a.substrateSubjects) {
		return ""
	}
	return a.substrateSubjects[a.selected]
}

// lastRowSubject returns the freshest (most-recent) thread row's subject
// from the parallel carry, or "" if none.
func (a App) lastRowSubject() string {
	for i := len(a.substrateSubjects) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(a.substrateSubjects[i]); s != "" {
			return s
		}
	}
	return ""
}

// postWriteReloadCmd is the snappy post-write feedback one-shot: an
// immediate substrate + tabs + mesh re-read so an operator-authored
// record appears WITHOUT waiting for the fsnotify debounce. It mirrors
// the existing loadSubstrateCmd/loadTabsCmd/loadMeshCmd one-shot pattern
// and — CRITICALLY — it NEVER re-arms the watcher (exactly the
// substrateLoadedMsg/meshLoadedMsg/tabsLoadedMsg exception class): it is
// produced by a key handler, NOT the watcher drain, so re-arming would
// double-drain. loadSubstrate/loadTabs/loadMesh re-read disk WHOLESALE &
// idempotently, so the immediate reload AND the watcher both catching the
// same on-disk write cannot double-insert (verified: the fold replaces
// state wholesale, never appends). The proven exactly-once
// watcherRearmWith drain is NOT touched by this path.
func (a App) postWriteReloadCmd() tea.Cmd {
	return tea.Batch(a.loadSubstrateCmd(), a.loadTabsCmd(), a.loadMeshCmd())
}

// splitCSV splits a comma string, trimming each token, dropping empties.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// nextToken splits s into (first whitespace-delimited token, remainder).
// Leading space is trimmed; the remainder keeps its internal spacing.
func nextToken(s string) (tok, rest string) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeft(s[i:], " \t")
}

// currentRow returns the selected LIVE substrate row, or nil if the
// index is out of range (defensive — selected is clamped in Update on
// every substrateLoadedMsg, but the empty cold-start has 0 rows).
func (a App) currentRow() *ThreadMsg {
	if a.selected < 0 || a.selected >= len(a.substrate) {
		return nil
	}
	return &a.substrate[a.selected]
}

// currentRowID returns the @thought id of the selected substrate row
// (the PR-G3 lineage-id carry — substrateIDs is parallel to substrate by
// construction, loadSubstrateWithIDs). "" when there is no id at that
// index: either the cold-start (no rows), a non-@thought row, OR a
// directly test/gate-injected substrateLoadedMsg that carried rows but
// no ids (a.substrateIDs nil — those gates do not exercise the
// drill-down; the live path always sets ids). Length-guarded
// independently of currentRow so a parallel-slice skew can never panic.
func (a App) currentRowID() string {
	if a.selected < 0 || a.selected >= len(a.substrateIDs) {
		return ""
	}
	return a.substrateIDs[a.selected]
}

// switchView sets the active tab. Selection state is preserved per the
// re-scope (you can flip away and back without losing your place).
func (a App) switchView(v AppView) App {
	a.view = v
	return a
}

// cycleView advances the active tab by delta (+1 forward, -1 back),
// wrapping around appViewOrder (tab / shift+tab).
func (a App) cycleView(delta int) App {
	idx := 0
	for i, v := range appViewOrder {
		if v == a.view {
			idx = i
			break
		}
	}
	n := len(appViewOrder)
	idx = (idx + delta%n + n) % n
	a.view = appViewOrder[idx]
	return a
}

// ── v8 shell geometry (PR-E LAYOUT CONTRACT) ──────────────────────────
//
// The v8 shell (rufio-bubbletea-v8.jsx 196-362) is NOT one panel: it is
// a border-LESS header/footer on the screen bg + a body of TWO bordered
// panels side by side (a flexible chat panel + a fixed-width mesh rail).
// The structure reads from the two Panel-bg fill-blocks on the darker bg
// + the inter-panel gap — NOT from a thick border (Line #2d2742 is a
// deliberate faint hairline; brightening it is explicitly forbidden).
const (
	// bodyGutter is the side gutter on header/body/footer. jsx
	// `padding: '… 18px'` (lines 204/225/343); the established px→cell
	// ratio (handoff §6 "18px ≈ 2 cells") gives 2 cells each side.
	bodyGutter = 2
	// panelGap is the gap between the two body panels. jsx `gap: 14`
	// (line 225) → round(14 / 2.6) ≈ 2 cells (same chat.go ratio).
	panelGap = 2

	// meshGridRows / meshGridCols are the mesh grid dimensions — jsx
	// `<MeshGrid rows={9} cols={36}>` (line 325).
	meshGridRows = 9
	meshGridCols = 36
)

// meshRailInner is the mesh rail's inner (border-stripped) width: the
// 36-col grid + the shared 2-cell interior h-pad each side = 40.
// meshRailOuter adds the 1-cell rounded border each side = 42. The rail
// is FIXED width (jsx `flex: '0 0 280px'`, line 309; the 280px portrait
// figure is re-derived here for the landscape 9×36 grid the contract
// mandates — the grid + pad is the real constraint, not the raw px).
const (
	meshRailInner = meshGridCols + 2*chatPanelHPad // 36 + 4 = 40
	meshRailOuter = meshRailInner + 2              // + border = 42
)

// #67-P5 (maintainer decision: SHRINK the rail at narrow widths, NEVER
// fully drop it until a MUCH smaller floor). The DEFAULT rail is still
// the fixed meshRailOuter (42) above — wide/default compositions are
// byte-identical. When the terminal cannot seat the full rail + a
// readable chat panel the rail COMPRESSES (a smaller mesh-grid col
// count, shorter labels via the existing renderer's clamp) down to a
// floor, and only THEN does the layout fall to single-panel.
//
//   - minChatOuter: the chat panel's readable minimum outer width
//     (promoted from the old local const in chatPanelOuterWidth — now
//     the SINGLE source the split + the chatScrollMax mirror share).
//   - meshRailFloorCols: the smallest mesh grid the compressed rail
//     renders. 20 cols still seats the operator hub + the radial agents
//     legibly (the existing renderer scales node columns + the labels
//     elide via clampLine — no second renderer). Below this the rail is
//     too cramped to read, so the layout single-panels.
const (
	minChatOuter       = 24                                  // below this the two-panel split is unreadable
	meshRailFloorCols  = 20                                  // smallest compressed mesh grid
	meshRailFloorInner = meshRailFloorCols + 2*chatPanelHPad // 20 + 4 = 24
	meshRailFloorOuter = meshRailFloorInner + 2              // + border = 26
)

// meshRailSplit is the SINGLE source of truth for the substrate body's
// horizontal split, given the body's border-stripped inner width. It
// returns the chat panel's outer width, the mesh rail's outer width, and
// whether the layout degrades to a single (rail-less) panel.
//
// Decision (P5 — shrink, never drop until the floor):
//
//   - If the FULL rail (meshRailOuter) fits beside a readable chat
//     panel (chat ≥ minChatOuter): rail = meshRailOuter, chat = the
//     remainder. This is the pre-P5 split EXACTLY — wide/default
//     compositions are byte-identical (the rail NEVER grows past
//     meshRailOuter).
//   - Else if a COMPRESSED rail (down to meshRailFloorOuter) still fits
//     beside a readable chat panel: the rail takes whatever width is
//     left after the chat floor + the gap, clamped to
//     [meshRailFloorOuter, meshRailOuter]. The rail SHRINKS instead of
//     being dropped — the established narrow degrade.
//   - Else: single-panel — the much smaller floor (terminal ≈ 56 cols)
//     vs the old ≈ 72-col drop. chatOuter == innerW, railOuter == 0,
//     single == true (the caller's single-panel signal, unchanged).
func meshRailSplit(innerW int) (chatOuter, railOuter int, single bool) {
	if innerW < 1 {
		innerW = 1
	}
	// Full rail beside a readable chat panel — the pre-P5 split verbatim.
	if c := innerW - panelGap - meshRailOuter; c >= minChatOuter {
		return c, meshRailOuter, false
	}
	// Compressed rail: give the chat its readable minimum, the rail gets
	// the rest (bounded to the floor..default band). Single-panel only
	// when even the floor rail cannot seat beside the chat minimum.
	railOuter = innerW - panelGap - minChatOuter
	if railOuter < meshRailFloorOuter {
		return innerW, 0, true // single-panel (the much smaller floor)
	}
	if railOuter > meshRailOuter {
		railOuter = meshRailOuter // never grow past the default
	}
	return innerW - panelGap - railOuter, railOuter, false
}

// View implements tea.Model. Composes, top to bottom:
//
//	header  (gradient wordmark + sync spinner + quiet tabs)  — NO border, on bg
//	body    (substrate: chat panel | gap | mesh rail; other  — FLEXES
//	         tabs: a single bordered panel)
//	footer  (numbered keybinds — NO attribution)             — NO border, on bg
//
// The body height is height − headerHeight − footerHeight so it fills
// the terminal (the old-TUI fill-bug fix). When an overlay is open it is
// composited over the full screen (help / lineage drill-down). Tiny
// terminals are clamped, never panicked.
func (a App) View() string {
	w := a.width
	h := a.height
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}

	header := a.renderHeader(w)
	footer := a.renderFooter(w)
	hint := a.renderOfflineHint(w) // F5: empty string when daemon online (no chrome impact)
	headerH := lipgloss.Height(header)
	footerH := lipgloss.Height(footer)
	hintH := 0
	if hint != "" {
		hintH = lipgloss.Height(hint)
	}

	bodyH := h - headerH - footerH - hintH
	if bodyH < 1 {
		bodyH = 1
	}

	var body string
	switch a.overlay {
	case overlayHelp:
		body = renderHelpOverlay(w, bodyH)
	case overlayLineage:
		// PR-G3: the drill-down payload is a.lineage — set by the enter
		// handler from EITHER the LIVE projectLineage(root, <thought-id>)
		// resolve (a real decision row) OR the fixture row.Lineage
		// fallback (the injected SubstrateThread gate path). The overlay
		// is only ever opened when a.lineage is non-nil (the enter guard
		// does not flip overlayLineage on a failed resolve), so this is
		// the single read site. Reuses the existing overlay renderer
		// (tabs.go renderLineageOverlay) unchanged.
		if a.lineage != nil {
			// #132 backstop: renderLineageOverlay now bounds the
			// box itself, but clamp every composed line to the
			// terminal width here too (mirrors the renderBody
			// path's clampBlock discipline ~:1831/2123) so a
			// future content path that mis-budgets can never
			// bleed past the screen — belt-and-suspenders.
			body = clampBlock(renderLineageOverlay(a.lineage, w, bodyH), w)
			// #134 G1: HEIGHT backstop (companion to the width
			// clampBlock above). renderLineageOverlay centers the
			// box with lipgloss.Place(w, bodyH, …); if a future /
			// pathological lineage (very long chain, tall content)
			// produces a box TALLER than bodyH, Place overflows
			// the body region (pushing the footer off / scrolling
			// the screen). Clamp to EXACTLY bodyH lines with the
			// established topTruncate→padBottom pattern (same
			// discipline as the chat feed): an over-tall overlay
			// degrades (clamped) instead of bleeding past the
			// screen. Minimal safety only — NO overlay scroll/keys,
			// renderLineageOverlay internals (#60) untouched. At
			// bodyH lines this is a byte-identical no-op for the
			// normal short fixture (the existing lineage golden is
			// unaffected).
			body = padBottom(topTruncate(body, bodyH), bodyH)
		} else {
			// Defensive: overlay set but no payload — fall back to the
			// tab body rather than render an empty box.
			body = a.renderBody(w, bodyH)
		}
	default:
		body = a.renderBody(w, bodyH)
	}

	// PR-E.1 — NO forced screen background. The v8 prototype paints the
	// whole root div `p.bg` and each panel `p.panel`; PR-E ported that
	// as a full-screen Bg-painted wrapper. That is REMOVED: the forced
	// two-tone bg renders patchy in real terminals and is not
	// theme-portable (it fights the user's terminal theme; broken on
	// non-dark themes). Rufio deliberately uses the terminal's NATIVE
	// background — exactly like the old/default `rufio tui`, which works
	// well. The composed screen is just JoinVertical of header/body/
	// footer; lines may be ragged-right (the terminal's own bg shows
	// through the gutters / gap / void) — that is INTENDED and correct.
	// Structure is carried by the Ring-toned panel borders + the
	// Ring-toned full-width section rules, not by bg contrast.
	if hint != "" {
		return lipgloss.JoinVertical(lipgloss.Left, header, body, hint, footer)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// renderOfflineHint is the v1.0.6.3 (F5) one-line teaching footer that
// surfaces ABOVE the keybind row when the daemon is offline. Converts a
// confusing UI moment ("why isn't anything happening?") into a teaching
// moment ("oh — run `rufio dev`"). Returns "" when the daemon is online
// so the View() height math is byte-identical to v1.0.6.2 for the
// daemon-up case. Dim styling — informational, never alarming. Inset
// by bodyGutter to align with the header/footer columns.
func (a App) renderOfflineHint(width int) string {
	if a.daemonOnline {
		return ""
	}
	msg := lipgloss.NewStyle().
		Foreground(styles.Palette.Dim).
		Render("daemon offline — run `rufio dev` for live updates")
	return gutter(msg, width)
}

// gradientWordmark applies a per-character color sweep across s, cycling
// Accent → Label → Accent2 (handoff §9: "A simple per-character color
// sweep ... recomputed on each render"). The distribution is static this
// PR (no tick offset); PR-E may animate the phase.
func gradientWordmark(s string) string {
	sweep := []lipgloss.Color{
		styles.Palette.Accent,
		styles.Palette.Label,
		styles.Palette.Accent2,
	}
	var b strings.Builder
	i := 0
	for _, r := range s {
		c := sweep[i%len(sweep)]
		b.WriteString(lipgloss.NewStyle().
			Foreground(c).
			Bold(true).
			Render(string(r)))
		i++
	}
	return b.String()
}

// renderTabs renders the quiet top tabs (jsx Tabs component / handoff
// §7.1) — KEPT VERBATIM visually, only the labels are Rufio's. For each
// view: a `●` prefix (Accent if active, else a blank cell so the columns
// stay stable) + the label. Active = Accent bold. Inactive = Dim. Tabs
// are separated by ` · ` in Line. No VDim placeholder slot (the `rules`
// placeholder is dropped — re-scope §1).
func (a App) renderTabs() string {
	dot := lipgloss.NewStyle().Foreground(styles.Palette.Accent).Render("●")
	sepStr := styles.Hairline.Render(" · ")

	var b strings.Builder
	for i, v := range appViewOrder {
		if i > 0 {
			b.WriteString(sepStr)
		}
		active := v == a.view
		if active {
			b.WriteString(dot)
		} else {
			// Hidden dot slot — keep the label columns stable whether or
			// not the tab is active (jsx renders a 0-opacity dot; in a
			// cell grid that is a single blank).
			b.WriteString(" ")
		}
		b.WriteString(" ")
		if active {
			b.WriteString(styles.TabActive.Render(string(v)))
		} else {
			b.WriteString(styles.TabInactive.Render(string(v)))
		}
	}
	return b.String()
}

// gutter is the bodyGutter-wide left/right inset applied to the
// border-less header/footer so they line up with the bodyGutter the
// bordered panels sit inside (the v8 shell aligns header/body/footer to
// the same 18px≈2-cell side padding, jsx 204/225/343). content is
// rendered to (width − 2·bodyGutter); a too-narrow terminal degrades to
// a 0-gutter render rather than a negative width (tiny-terminal safety).
func gutter(content string, width int) string {
	inner := width - 2*bodyGutter
	if inner < 1 {
		// No room for gutters — emit the content as-is, clamped to width.
		return clampBlock(content, width)
	}
	pad := strings.Repeat(" ", bodyGutter)
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		lines[i] = pad + clampLine(ln, inner) + pad
	}
	return strings.Join(lines, "\n")
}

// renderHeader renders the top header row: the gradient wordmark, the
// ` ⠋ syncing` spinner (the 80ms dots cadence in Accent + "syncing" in
// Dim — jsx `SPINNERS.dots[t % len]`, rufio-bubbletea-v8.jsx line 211),
// and the right-aligned quiet tabs. Border-LESS, on the screen bg,
// inset by the bodyGutter (jsx header, lines 204-222). At anim.spin==0
// the frame is dots[0]=⠋ — byte-identical to the pre-PR-F static const.
//
// v1.0.6.3 (F1): when the daemon is offline the "syncing" indicator
// LIES — nothing is syncing because nothing is being watched. The
// spinner + "syncing" label are suppressed when !a.daemonOnline; a
// quiet Dim "offline" tag takes their place so the slot is still
// occupied (preserves the wordmark/tabs alignment) but reads honestly.
func (a App) renderHeader(width int) string {
	inner := width - 2*bodyGutter
	if inner < 1 {
		inner = width
	}

	mark := gradientWordmark(wordmark)

	var statusSegment string
	if a.daemonOnline {
		spinner := lipgloss.NewStyle().
			Foreground(styles.Palette.Accent).
			Render(spinnerFrame(spinnerDots, a.anim.spin))
		syncing := lipgloss.NewStyle().
			Foreground(styles.Palette.Dim).
			Render(" syncing")
		statusSegment = spinner + syncing
	} else {
		statusSegment = lipgloss.NewStyle().
			Foreground(styles.Palette.Dim).
			Render("· offline")
	}

	left := mark + "  " + statusSegment
	tabs := a.renderTabs()

	leftW := lipgloss.Width(left)
	tabsW := lipgloss.Width(tabs)

	var row string
	if leftW+1+tabsW <= inner {
		pad := inner - leftW - tabsW
		row = left + strings.Repeat(" ", pad) + tabs
	} else {
		// Too narrow to right-align tabs on the same row — stack them so
		// nothing is clipped (tiny-terminal safety).
		row = left + "\n" + tabs
	}
	return gutter(row, width)
}

// renderFooter renders the bottom footer row (handoff §7.8) with Rufio's
// keybinds. RE-SCOPE: the labels are Rufio's tabs and the right-aligned
// `built with Bubble Tea + Lip Gloss · v8 …` attribution is DELETED
// entirely (prototype chrome, not product — re-scope §1 / PR-D §2). Keys
// in Accent, ` · ` in Line, labels in Dim.
func (a App) renderFooter(width int) string {
	keyStyle := lipgloss.NewStyle().Foreground(styles.Palette.Accent)
	labelStyle := styles.Footer // Dim
	dotSep := styles.Hairline.Render(" · ")

	// #68: the Esc→nav / i→compose modality is surfaced HERE so it is
	// discoverable WITHOUT the `?` overlay (static, always shown — no
	// mode logic, no renderFooter signature change; lowest-risk
	// affordance). The longer footer still degrades safely at narrow
	// widths: gutter()/clampLine HARD-TRUNCATES it to one line (it never
	// wraps), so footerH stays 1 and bodyH = h − headerH − footerH is
	// unchanged (TestFooterModalityHintAndHeightInvariant).
	binds := []struct{ key, label string }{
		{"1", "substrate"}, {"2", "fleet"}, {"3", "channels"},
		{"4", "goals"}, {"5", "memory"}, {":", "cmd"}, {"?", "help"},
		{"esc", "nav"}, {"i", "compose"},
	}
	var left strings.Builder
	for i, b := range binds {
		if i > 0 {
			left.WriteString(" ")
		}
		left.WriteString(keyStyle.Render(b.key))
		left.WriteString(dotSep)
		left.WriteString(labelStyle.Render(b.label))
	}
	// NO right-aligned attribution (deleted — re-scope §1). The footer is
	// the keybind row only, border-LESS on the screen bg, inset by the
	// bodyGutter so it aligns with the header + the bordered panels.
	return gutter(left.String(), width)
}

// renderBody renders the active tab's content for the full body region
// (the bodyGutter is applied here, NOT inside the panels — the panels
// keep their own border-integrity budget). substrate is the two-panel
// composition (flexible chat panel + fixed mesh rail); the other four
// tabs are a single bordered panel filling the gutter-inset width.
//
// substrate composition (jsx body, lines 224-339):
//
//	┌ chat panel (flex) ┐  gap  ┌ mesh rail (fixed) ┐
//
// Width math (NO outer border; bodyGutter each side; panelGap between):
//
//	chatOuter = width − 2·bodyGutter − panelGap − meshRailOuter
//
// When the terminal is too narrow to seat both panels + the gap, the
// mesh rail is DROPPED and the chat panel takes the full gutter-inset
// width (the established tiny-terminal degrade — never panic, never
// break a border). The other tabs always use the single full-width
// panel; the fleet tab's BODY is the mesh per the contract.
func (a App) renderBody(width, height int) string {
	innerW := width - 2*bodyGutter
	if innerW < 1 {
		innerW = 1
	}

	if a.view != viewSubstrate {
		panel := a.renderTabPanel(innerW, height)
		return gutter(panel, width)
	}

	// Substrate: two panels side by side, or — only below the much
	// smaller P5 floor — a graceful single-panel fallback. meshRailSplit
	// is the single geometry source: it COMPRESSES the rail at narrow
	// widths (a smaller mesh grid) instead of dropping it, falling to
	// single-panel only when even the floor rail cannot seat.
	chatOuter, railOuter, single := meshRailSplit(innerW)
	if single {
		// Single-panel fallback (even the compressed-floor rail + gap
		// will not fit — the much smaller floor, ≈ 56-col terminal).
		panel := a.renderChatPanel(innerW, height)
		return gutter(panel, width)
	}

	chat := a.renderChatPanel(chatOuter, height)
	// railOuter is meshRailOuter at wide/default widths (byte-identical)
	// and a compressed width at narrow ones — the SAME meshPanel
	// renderer, parameterised on the panel width (it derives the mesh
	// grid cols from its own contentWidth, capped at meshGridCols, so
	// the default is an identity no-op and only the compressed rail
	// renders a smaller grid). NO second mesh renderer.
	rail := a.meshRail(railOuter, height)
	gap := strings.Repeat(" ", panelGap)
	body := lipgloss.JoinHorizontal(lipgloss.Top, chat, gap, rail)
	return gutter(body, width)
}

// chatPanelHPad is the chat/tab panel's interior HORIZONTAL padding, in
// cells, applied on EACH side inside the rounded border. 2 cells per
// side is the pragmatic terminal-scale pick (PR-C Defect 2): it gives
// the v8 "breathing room" so nothing is jammed against the border and
// keeps the reply rail off the panel `│` (Defect 3), while staying
// affordable at 80 cols. Deliberate terminal-scale deviation from the
// raw px ratio, scoped to the panel interior only.
const chatPanelHPad = 2

// chatPanelTopPad is the interior VERTICAL padding (blank lines) at the
// TOP of the content region — one blank line, the terminal-scale
// equivalent of the jsx thread top inset.
const chatPanelTopPad = 1

// chatPanelOuterWidth is the chat panel's outer width given the body's
// border-stripped inner width — a thin adapter over the meshRailSplit
// single source (P5). It returns the chat outer width, or innerW to
// signal the single-panel fallback (the caller compares == innerW —
// contract UNCHANGED, so chatScrollMax (#134) keeps reproducing the
// EXACT geometry the paint sees and Home's stored bound can never drift
// from what windowLines clamps to at render time). The split decision
// itself (full rail / compressed rail / single-panel) now lives in
// meshRailSplit so renderBody and this mirror can never disagree.
func chatPanelOuterWidth(innerW int) int {
	chatOuter, _, single := meshRailSplit(innerW)
	if single {
		return innerW
	}
	return chatOuter
}

// panelInner computes the inner (border-stripped) and content (after
// h-padding) widths/heights for a Panel-bordered box of the given outer
// width/height. Centralised so the chat panel and the tab panels share
// the EXACT border-integrity budget (the PR-C contract).
func panelInner(width, height int) (innerWidth, innerHeight, contentWidth int) {
	innerWidth = width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}
	innerHeight = height - 2
	if innerHeight < 1 {
		innerHeight = 1
	}
	contentWidth = innerWidth - 2*chatPanelHPad
	if contentWidth < 1 {
		contentWidth = 1
	}
	return innerWidth, innerHeight, contentWidth
}

// renderChatPanel renders the substrate chat panel: a rounded
// Panel-bordered box containing a chrome strip, the threaded
// SubstrateThread + typing indicator, and the composer. The selected
// row is highlighted (a `▸` gutter marker) so the user can see what
// `enter` will drill into.
//
// Hard inner-width contract (border NEVER breaks): every interior line
// is rendered to contentWidth then clampBlock'd as a final backstop, so
// the rounded border stays intact at every width (PR-C Defect 1+3). If
// content overflows the inner height the thread is top-truncated (oldest
// rows first) so the latest rows + composer stay visible (PR-C clamp;
// full scrollback is PR-F).
// chatThreadGeom is the SHARED chat-panel geometry seam: from a panel
// outer width/height it computes the chrome, the composer, the visible
// thread height (threadH) and the pre-window rendered thread string — the
// EXACT inputs renderChatPanel feeds windowLines every paint. Factored
// out so chatScrollMax (#134 Home) derives its upper bound from the SAME
// computation the paint uses (single source of truth — the stored Home
// offset can never drift from what windowLines clamps to at render time).
// renderChatPanel's behaviour is byte-unchanged: it calls this then does
// exactly the windowLines+padBottom+compose it always did.
func (a App) chatThreadGeom(width, height int) (chrome, composer, thread string, threadH int) {
	_, innerHeight, contentWidth := panelInner(width, height)

	chrome = clampBlock(a.renderChatChrome(contentWidth), contentWidth)
	// G-interact: the composer is now the LIVE buffer-aware renderer
	// (renderComposerLive — composer.go). It is EXACTLY ComposerHeight
	// rows with the SAME top-rule as the lower interior hairline (the
	// structural-gate contract is preserved — only the input row's
	// glyphs + the mode whisper change with state). `focused` is the
	// compose/nav mode so the whisper tells the operator where keystrokes
	// go. The static RenderComposer/renderComposer path stays for the
	// PR-C/D composer unit tests + the read-only composer goldens.
	//
	// The composer's editing model is the bubbles/textarea (composeTA);
	// renderComposerLive draws the v8 visual from its VALUE + CURSOR.
	// curLine is the cursor's LOGICAL row (textarea.Line() == m.row);
	// curCol is the cursor's column WITHIN that logical line. The
	// textarea is configured wide enough that no SOFT-wrap occurs (see
	// newComposerTextarea), so LineInfo().ColumnOffset is exactly the
	// cursor's rune column within its logical line (StartColumn==0,
	// RowOffset==0) — the v8 blink-caret is then placed at that cell on
	// the cursor's logical line (renderComposerLive). The fixed
	// ComposerHeight footprint is unchanged regardless of cursor/content
	// (the cross-column ROUTING-rule-alignment contract).
	curLine := a.composeTA.Line()
	curCol := a.composeTA.LineInfo().ColumnOffset
	composer = clampBlock(renderComposerLive(contentWidth, a.composeMode, a.anim.caret, a.composeText(), curLine, curCol), contentWidth)

	chromeH := lipgloss.Height(chrome)
	composerH := lipgloss.Height(composer)

	threadH = innerHeight - chromeH - composerH - chatPanelTopPad
	if threadH < 1 {
		threadH = 1
	}

	// PR-G1: the LIVE projected thread replaces the static
	// SubstrateThread fixture render. The decision-row caret still
	// blinks at the 500ms cadence (frame-0 ▮ at anim.caret==0). The
	// hardcoded renderTypingIndicatorAt("data-analyst",…) is REMOVED:
	// it was fixture decoration; Rufio has no presence/typing primitive
	// and faking it is explicitly out of scope (OPEN-4, locked — real
	// liveness is a later slice). The composer caret below is the
	// operator's own real caret and stays.
	if !a.sawThought {
		// Fresh / empty substrate: render the normal v8 frame (the
		// panel/border/chrome/composer are all intact around this) with
		// a single quiet centered setup hint — NOT a blank void, NOT a
		// crash, NOT a modal block. Copy reuses the old TUI's
		// attend-guidance wording (tui.go:119), re-toned to the v8
		// borderless language (Dim). Hand-centered (NOT lipgloss.Place,
		// which space-pads EVERY line to contentWidth — those padded
		// blanks then trip clampBlock's truncateToWidth and sprout a
		// trailing `…` on every empty row): the hint is one centered
		// line, the rest are GENUINE empty lines (truly blank, no `…`),
		// and padBottom below fills the region.
		thread = centerHintBlock(substrateEmptyHint, contentWidth, threadH)
	} else {
		thread = renderChatSelectedAt(a.substrate, contentWidth, a.selected, a.anim.caret)
	}
	thread = clampBlock(thread, contentWidth)
	return chrome, composer, thread, threadH
}

func (a App) renderChatPanel(width, height int) string {
	innerWidth, innerHeight, _ := panelInner(width, height)

	chrome, composer, thread, threadH := a.chatThreadGeom(width, height)

	// #134: the offset-aware render window. a.scrollOffset == 0 is the
	// live tail — byte-identical to the pre-#134 topTruncate render (the
	// goldens are frame-0, no keys driven, so scrollOffset is 0). A
	// non-zero offset (PgUp/Home) slides the maxRows-tall window UP toward
	// older events; windowLines clamps it so it can never overrun.
	thread = windowLines(thread, threadH, a.scrollOffset)
	thread = padBottom(thread, threadH)

	topPad := strings.Repeat("\n", chatPanelTopPad)
	inner := lipgloss.JoinVertical(lipgloss.Left, chrome, topPad+thread, composer)

	// Height-set the panel so lipgloss paints the Panel bg (#1a1726) on
	// EVERY interior row — including the blank rows below a short thread.
	// Without this the void below the 6-message thread falls back to the
	// terminal-default screen and reads as a dead black gap instead of a
	// filled panel slightly lighter than the #13111c screen bg. The
	// border is NOT brightened to compensate — the structure reads from
	// the #13111c-vs-#1a1726 two-tone (Fix 2).
	return styles.Panel.
		Width(innerWidth).
		Height(innerHeight).
		PaddingLeft(chatPanelHPad).
		PaddingRight(chatPanelHPad).
		Render(inner)
}

// renderTabPanel renders a non-substrate tab's content inside the SAME
// rounded Panel-bordered box as the chat panel (so the border-integrity
// invariants hold for every tab, PR-C). Per the PR-E contract the FLEET
// tab's body IS the Rufio mesh (the substrate right-rail AND the fleet
// tab render the same mesh, jsx 308-338) — it goes through the shared
// meshPanel renderer (header + centered mesh + ROUTING strip). The other
// three tabs render their fixture (tabs.go) to contentWidth, clamped,
// top-truncated if it overflows, and bottom-padded so the panel fills.
func (a App) renderTabPanel(width, height int) string {
	if a.view == viewFleet {
		// Fleet tab content = the mesh (PR-E contract). renderFleetTab
		// (the roster) stays a directly-tested renderer but the fleet
		// *screen* is the mesh per v8.
		return a.meshPanel(width, height)
	}

	innerWidth, innerHeight, contentWidth := panelInner(width, height)

	var content string
	// PR-G3: feed the renderers the LIVE projected tab state (a.tabs —
	// loadTabs / tabsLoadedMsg) instead of the fixtures.go globals. The
	// renderers are data-source-agnostic (they take their slice as a
	// param now); the v8 visual language is unchanged. The lineage
	// drill-down (the 4th G3 surface) is the enter-handler/overlay path,
	// not a tab — resolved live via projectLineage.
	switch a.view {
	case viewChannels:
		content = renderChannelsTab(a.tabs.Channels, contentWidth, a.channelSel)
	case viewGoals:
		content = renderGoalsTab(a.tabs.Goals, contentWidth)
	case viewMemory:
		content = renderMemoryTab(a.tabs.Memory, contentWidth)
	default:
		// Unreachable (substrate goes through renderChatPanel) — render
		// nothing rather than a placeholder so there is no dead "(not in
		// scope)" string anywhere (re-scope drops placeholders).
		content = ""
	}

	content = clampBlock(content, contentWidth)
	bodyH := innerHeight - chatPanelTopPad
	if bodyH < 1 {
		bodyH = 1
	}
	content = topTruncate(content, bodyH)
	content = padBottom(content, bodyH)

	topPad := strings.Repeat("\n", chatPanelTopPad)
	inner := topPad + content

	// Height-set so the Panel bg fills every interior row incl. the
	// void below short content (Fix 2 — same rationale as
	// renderChatPanel).
	return styles.Panel.
		Width(innerWidth).
		Height(innerHeight).
		PaddingLeft(chatPanelHPad).
		PaddingRight(chatPanelHPad).
		Render(inner)
}

// routingBottomPad is the number of blank lines AFTER the ROUTING row.
// The chat composer block is ComposerHeight inner lines (top-rule /
// padV / input row / gapV / hint row / padV — composer.go). The mesh
// rail's bottom block must be the SAME height with its hairline on the
// SAME inner row so the ROUTING rule lines up across columns with the
// composer's TOP-rule — the deliberate v8 cross-column detail (jsx
// 327-336 "sized to match the composer's top-rule … so the two
// horizontal lines line up"). The ROUTING block is `renderRoutingStrip`
// (2 lines: rule + ROUTING row) + routingBottomPad blanks, so for the
// block heights (and thus the top-rule screen rows) to match:
//
//	2 + routingBottomPad == ComposerHeight  ⇒  routingBottomPad = ComposerHeight − 2
//
// Derived from ComposerHeight (NOT hardcoded) so the alignment holds
// automatically if the composer's vertical padding ever changes.
const routingBottomPad = ComposerHeight - 2

// renderMeshHeader renders the mesh panel header strip: `◆ MESH` (Accent
// bold) + `N nodes · N links` (Dim) on the left; the 80ms bouncing
// spinner + `live` (Accent2) pushed right (jsx 315-321, `SPINNERS.
// bouncing[t % len]`). PR-G2: N nodes / N links are the REAL LIVE
// projected counts (len(mesh.Nodes) = synthesized operator hub +
// attention-bearing agents; len(mesh.Edges) = the outbox∩inbox routing
// deliveries) — never hardcoded, never the fixture. spin is the shared
// 80ms cadence counter (anim.spin); at spin==0 the frame is
// bouncing[0]=⠁ — byte-identical to the pre-PR-F static const.
//
// v1.0.6.3 (F3): the right-side `· live` badge implies live mesh
// updates. When the daemon is offline the mesh state is stale — the
// fsnotify watcher isn't running, so the badge is a lie. Suppress
// both the spinner and the `live` label when daemonOnline is false;
// the panel still renders normally (the mesh body remains accurate
// for whatever was on disk at startup).
func renderMeshHeader(innerWidth, spin int, mesh meshState, daemonOnline bool) string {
	title := lipgloss.NewStyle().
		Foreground(styles.Palette.Accent).
		Bold(true).
		Render("◆ MESH")
	meta := lipgloss.NewStyle().
		Foreground(styles.Palette.Dim).
		Render("  " + itoa(len(mesh.Nodes)) + " nodes · " + itoa(len(mesh.Edges)) + " links")
	left := title + meta

	if !daemonOnline {
		// No spinner, no `live` badge — daemon-offline whisper is
		// surfaced in the chat chrome (substrateOfflineNote) and the
		// new F5 footer hint. The mesh header reads as a pure title.
		return left
	}

	spin2 := lipgloss.NewStyle().
		Foreground(styles.Palette.Accent2).
		Render(spinnerFrame(spinnerBouncing, spin))
	live := lipgloss.NewStyle().
		Foreground(styles.Palette.Accent2).
		Render(" live")
	right := spin2 + live

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	if leftW+1+rightW <= innerWidth {
		return left + strings.Repeat(" ", innerWidth-leftW-rightW) + right
	}
	return left
}

// renderRoutingStrip renders the ROUTING strip (PR-E product decision —
// replaces the dropped jsx GOV strip, no governance primitive in Rufio):
// `ROUTING` (Label bold) + ` N linked · quorum X/Y · append-only` (Dim).
// PR-G2: `linked` is the LIVE mesh node count (len(mesh.Nodes) =
// synthesized operator hub + attention-bearing agents), never the
// fixture. `quorum X/Y` is taken from the LIVE substrate (G1) decision
// row's Quorum (the OPEN-2-resolved `2/3`) — falling back to the
// SubstrateThread gate fixture only when the live thread carries no
// decision-with-quorum yet (cold start / no decision), so the strip
// still renders a sensible value rather than `—` while the structural
// gate (which injects the fixture) stays byte-stable. The leading
// hairline is the section divider, aligned across columns with the
// composer's top-rule (see routingBottomPad).
func renderRoutingStrip(innerWidth int, mesh meshState, substrate []ThreadMsg) string {
	// Full-width section rule — Ring tone so it reads on the native
	// terminal bg (PR-E.1); the inline ` · ` dots below stay Hairline.
	rule := styles.SectionRule.Render(strings.Repeat("─", innerWidth))

	label := styles.GovLabel.Render("ROUTING") // Label color, bold

	linked := len(mesh.Nodes)
	quorum := routingQuorum(substrate)

	// #67-P3: the rail is fixed at meshRailInner−2·hpad = 36 cols, where
	// the full `…  N linked · quorum X/Y · append-only` overflows and the
	// old clampLine hard-truncated mid-word into an ugly dangling
	// `quorum 2/3 · ap…`. Degrade cleanly: keep the legible CORE
	// (`  N linked · quorum X/Y`) and DROP the ` · append-only` suffix
	// whenever it will not fit alongside the core. When it DOES fit (the
	// full mesh panel, contentWidth ≫ 36) the string is byte-identical to
	// before — only the previously-`…`-clamped narrow path changes.
	core := "  " + itoa(linked) + " linked · quorum " + quorum
	const suffix = " · append-only"
	metaStyle := lipgloss.NewStyle().Foreground(styles.Palette.Dim)
	full := metaStyle.Render(core + suffix)
	row := label + full
	if lipgloss.Width(row) > innerWidth {
		// Suffix does not fit — drop it cleanly rather than `…`-clamp
		// through `append-only`. clampLine stays as the final
		// border-integrity backstop for the (already much shorter) core.
		row = clampLine(label+metaStyle.Render(core), innerWidth)
	}
	return rule + "\n" + row
}

// routingQuorum returns the `X/Y` quorum fragment for the ROUTING strip.
// PR-G2: it reads the LIVE substrate (G1) — the first decision row that
// carries a Quorum (OPEN-2-resolved by the substrateLoadedMsg fold:
// Total = autopromote.MinDistinctConfirmers). When the live thread has
// no decision-with-quorum yet (cold start / no decision / no confirms)
// it renders the live empty-state `—` — the same "degrade, never
// substitute fixture data" convention the live path uses elsewhere
// (e.g. the projectLineage/contextualVote degrade paths and the mesh
// header's live zero counts). The denominator is NEVER a hardcoded
// literal: it comes from each Quorum.Total, which the fold set from the
// auto-promote constant.
//
// V8G2-M2 (RESOLVED, G4 cutover / PR #50): the previous SubstrateThread
// gate-fixture fallback (which surfaced a fake `2/3` at cold start to
// keep the structural ROUTING-alignment gate byte-stable) is retired.
// It was the one place the live render path still referenced a fixture;
// fixtures.go itself stays (the v8 test helpers still inject it via
// substrateLoadedMsg), only this LIVE-path dependency is removed.
func routingQuorum(substrate []ThreadMsg) string {
	for _, m := range substrate {
		if m.Role == roleDecision && m.Quorum != nil {
			return itoa(len(m.Quorum.Yes)) + "/" + itoa(m.Quorum.Total)
		}
	}
	return "—"
}

// meshPanel renders the full bordered mesh panel (header / centered mesh
// body / hairline / ROUTING strip). Shared by the substrate right-rail
// (meshRail) and the fleet tab — the SAME mesh per the PR-E contract
// (jsx 308-338). The mesh body FLEXES to fill the panel height and is
// vertically centered (jsx line 322 `alignItems:'center'`); the ROUTING
// strip is pinned at the bottom with its hairline aligned to the chat
// composer's top-rule across columns.
func (a App) meshPanel(width, height int) string {
	innerWidth, innerHeight, contentWidth := panelInner(width, height)

	// PR-G2: header/routing/grid all read the LIVE projected mesh
	// (a.mesh — synthesized operator hub + attention-bearing agents +
	// outbox∩inbox routing edges), NOT the MeshNodes/deriveMeshEdges
	// fixture. This is the SAME a.mesh in BOTH the substrate right-rail
	// (meshRail) and the fleet tab (renderTabPanel) — same renderer, same
	// data, per the v8 design.
	header := clampLine(renderMeshHeader(contentWidth, a.anim.spin, a.mesh, a.daemonOnline), contentWidth)
	routing := clampBlock(renderRoutingStrip(contentWidth, a.mesh, a.substrate), contentWidth)

	headerH := lipgloss.Height(header)
	routingH := lipgloss.Height(routing) + routingBottomPad

	// Mesh body flexes between the header and the ROUTING strip.
	bodyH := innerHeight - headerH - routingH - chatPanelTopPad
	if bodyH < 1 {
		bodyH = 1
	}

	// #67-P5: the mesh grid col count is derived from THIS panel's own
	// contentWidth, capped at meshGridCols. At the default rail
	// (contentWidth == meshGridCols == 36) and the full-width fleet mesh
	// (contentWidth ≫ 36, capped to 36) this is the canonical 36 and
	// scaleMeshNodesToCols is an IDENTITY no-op — byte-identical to
	// pre-P5. Only a COMPRESSED rail (contentWidth < 36) renders a
	// smaller grid with rescaled node columns, reusing the SAME
	// RenderMeshFrom renderer (no fork). Single-sourced: the grid
	// geometry follows the panel width, never a separate constant.
	meshCols := contentWidth
	if meshCols > meshGridCols {
		meshCols = meshGridCols
	}
	if meshCols < 1 {
		meshCols = 1
	}
	mesh := clampBlock(
		RenderMeshFrom(meshGridRows, meshCols, a.anim.mesh,
			scaleMeshNodesToCols(a.mesh.Nodes, meshCols), a.mesh.Edges),
		contentWidth)
	// Vertically center the mesh in the flexed body region (jsx
	// `justifyContent:'center'`). lipgloss.Place keeps each line's width
	// stable; clampBlock is the final border-integrity backstop.
	mesh = clampBlock(
		lipgloss.Place(contentWidth, bodyH, lipgloss.Center, lipgloss.Center, mesh),
		contentWidth,
	)

	bottomPad := strings.Repeat("\n", routingBottomPad)
	topPad := strings.Repeat("\n", chatPanelTopPad)
	inner := lipgloss.JoinVertical(
		lipgloss.Left, header, topPad+mesh, routing+bottomPad,
	)

	// Height-set so the Panel bg fills every interior row of the mesh
	// rail incl. the centered-mesh void above/below (Fix 2).
	return styles.Panel.
		Width(innerWidth).
		Height(innerHeight).
		PaddingLeft(chatPanelHPad).
		PaddingRight(chatPanelHPad).
		Render(inner)
}

// meshRail renders the substrate screen's fixed-width right rail — the
// mesh panel at meshRailOuter width (jsx `flex:'0 0 280px'` re-derived
// for the landscape 9×36 grid; see meshRailOuter).
func (a App) meshRail(width, height int) string {
	return a.meshPanel(width, height)
}

// itoa is strconv.Itoa under a short name (the mesh header / ROUTING
// strip build several small int→string fragments; keeps the call sites
// terse).
func itoa(n int) string {
	return strconv.Itoa(n)
}

// clampLine guarantees a single line's visible width does not exceed w,
// hard-truncating with `…` if it does. The FINAL backstop for the
// border-intact invariant: even if an upstream renderer mis-budgets, no
// panel-interior line can overflow and wrap against the border. Rune-
// and runewidth-aware (reuses truncateToWidth from chat.go) and
// ANSI-tolerant (lipgloss.Width ignores escapes).
func clampLine(s string, w int) string {
	if w < 1 {
		w = 1
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return truncateToWidth(s, w)
}

// clampBlock applies clampLine to every line of a multi-line block.
func clampBlock(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = clampLine(ln, w)
	}
	return strings.Join(lines, "\n")
}

// distinctAuthors counts the unique row authors in the live thread —
// the REAL "N minds" the chat-chrome strip shows (PR-G1: driven by data,
// never a fixed literal). The operator is one of the minds (it posts the
// op row); an empty thread is 0 minds (the cold-start frame).
func distinctAuthors(rows []ThreadMsg) int {
	if len(rows) == 0 {
		return 0
	}
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		if r.Who != "" {
			seen[r.Who] = true
		}
	}
	return len(seen)
}

// renderChatChrome renders the chat header strip: `◆ #substrate`
// (Accent bold) + `N minds · <me> linked` (Dim) [+ ` · daemon offline ·`
// (Warm)] on the left; the live 500ms sparkline + `{rate}/s` + the 80ms
// arc spinner + `live` on the right; then a full-inner-width
// bottom-border separator.
//
// PR-G1 — driven by REAL data, not fixed strings:
//   - "N minds" = the REAL distinct-author count of the live thread
//     (distinctAuthors; never the old `3` literal);
//   - "<me>"    = the resolved operator identity (a.me; not hardcoded);
//   - the quiet ` · daemon offline ·` whisper (Warm — non-alarming, NOT
//     a modal block; LOCKED 2026-05-16: the console is a filesystem
//     console, NEVER gated on the daemon — history still renders) is
//     appended to the LEFT meta when PollDaemonOnline reports the daemon
//     down. It is on the LEFT (a status whisper next to "minds/linked",
//     where the old TUI also surfaced it, tui.go:661) so the daemon
//     FACT is named in plain text, not implied by the absence of
//     spinners on the right.
//
// v1.0.6.3 (Bundle F / F2 + F4): the right-side activity readout —
// sparkline + `N/s` rate + arc spinner + `live` badge — is now GATED
// on a.daemonOnline. When the daemon is offline the entire right
// segment collapses to empty; when online it renders byte-identically
// to v1.0.6.2 (frame-0 still byte-identical at anim==0 + seeded
// series + online). The pre-Bundle-F framing — "the right-side `live`
// is the view-is-live-updating label, history live-renders regardless
// of daemon state — accurate" — was the v1.0.6.2 design but read as
// a lie next to the truthful LEFT `· daemon offline ·` whisper when
// the daemon was down (the sparkline series is a deterministic
// stand-in pending PR-G real event-rate data, NOT a live feed). The
// LEFT whisper is now the SINGLE truth-bearing daemon-state marker
// in the chrome strip; the right segment is the explicit live-data
// readout it always claimed to be.
func (a App) renderChatChrome(innerWidth int) string {
	channel := lipgloss.NewStyle().
		Foreground(styles.Palette.Accent).
		Bold(true).
		Render("◆ #substrate")
	// G-interact: the transient composer result/error note (composeNote)
	// is surfaced HERE — in the existing chrome strip's meta slot — so it
	// is rendered in-pane (the existing convention; never stdout, never a
	// crash/exit code) WITHOUT adding a row (the structural gates pin the
	// 2-row chrome strip: strip + bottom-rule). When a note is present it
	// REPLACES the `N minds · me linked` meta for one render cycle (a ✓
	// success in Accent2, a ✗ error in Warm); cleared on the next
	// keystroke. Goldens fix an empty buffer + no note, so this slot is
	// byte-identical to the read-only render in every golden/gate.
	var meta string
	if a.composeNote != "" {
		tone := styles.Palette.Accent2
		if strings.HasPrefix(a.composeNote, "✗") {
			tone = styles.Palette.Warm
		}
		meta = lipgloss.NewStyle().Foreground(tone).Render("  " + a.composeNote)
	} else {
		meta = lipgloss.NewStyle().
			Foreground(styles.Palette.Dim).
			Render(" " + itoa(distinctAuthors(a.substrate)) + " minds · " + a.me + " linked")
	}
	left := channel + meta
	if !a.daemonOnline {
		// Quiet daemon-offline whisper (Warm, non-alarming). The console
		// stays fully usable — this is informational, never a block.
		left += lipgloss.NewStyle().
			Foreground(styles.Palette.Warm).
			Render("  " + substrateOfflineNote)
	}
	if a.scrollOffset > 0 {
		// #134: the scrolled-back affordance. Appended to the EXISTING
		// LEFT segment (Dim, like the meta whisper) so it lives WITHIN
		// the existing chrome strip — it does NOT add a row (the strip is
		// still strip + bottom-rule; the structural gates' 2-row chrome
		// contract is intact, threadH is unchanged). It is rendered ONLY
		// when scrolled, so at offset 0 `left` (hence the whole chrome)
		// is byte-identical to the pre-#134 render and every frame-0
		// golden stays byte-stable. If the left segment now overflows
		// innerWidth the existing fit policy below drops the right
		// readout (graceful — never a border break, never an extra
		// line).
		left += lipgloss.NewStyle().
			Foreground(styles.Palette.Dim).
			Render("  ↑scrolled · End=live")
	}

	// Defensive: series is created in NewApp; a hand-constructed App
	// (some unit tests) may have a nil series — fall back to the seeded
	// frame-0 ring so the chrome still renders the static window.
	ser := a.series
	if ser == nil {
		ser = newSeries()
	}
	// v1.0.6.3 (Bundle F): the right-side activity readout — sparkline,
	// `N/s` rate, arc spinner, and `live` badge — is now WIRED to real
	// substrate event rate (#226: events_per_sec sampled at 2 Hz over
	// ThoughtMsg + ConfirmMsg + AttentionMsg arrivals; see app.go's
	// seriesTickMsg handler). The series advances with a real sample
	// each tick; ser.window() and ser.rate() report observed activity.
	//
	// Daemon offline (F2 + F4): whole right segment is empty; the
	// left-side `· daemon offline ·` whisper names the state. Without a
	// daemon, no fsnotify drain is running so eventTickCount would stay
	// at 0 (rate=0) regardless — but suppressing the segment is the
	// stronger guarantee.
	//
	// Mesh particles + node pulse rings (rendered elsewhere) animate
	// over real edges (outbox∩inbox routing) and real attention-bearing
	// nodes — ambient over REAL state.
	var spark, rate, dotS, arc, live string
	if a.daemonOnline {
		spark = renderSparkline(ser.window())
		rate = lipgloss.NewStyle().
			Foreground(styles.Palette.Dim).
			Render(" " + itoa(ser.rate()) + "/s")
		dotS = styles.Hairline.Render(" · ")
		arc = lipgloss.NewStyle().
			Foreground(styles.Palette.Accent).
			Render(spinnerFrame(spinnerArc, a.anim.spin))
		live = lipgloss.NewStyle().
			Foreground(styles.Palette.Dim).
			Render(" live")
	}
	right := spark + rate + dotS + arc + live

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)

	var strip string
	if leftW+1+rightW <= innerWidth {
		pad := innerWidth - leftW - rightW
		strip = left + strings.Repeat(" ", pad) + right
	} else {
		strip = left
	}

	// Full-width chrome bottom-rule — Ring tone so it reads on the
	// native terminal bg (PR-E.1); the inline ` · ` dot above (dotS)
	// stays the quiet Hairline tone.
	sepRule := styles.SectionRule.Render(strings.Repeat("─", innerWidth))
	return strip + "\n" + sepRule
}

// windowLines is the offset-aware render window (#134 scrollback). It
// keeps a maxRows-tall slice of s; offset is how many lines the window
// is scrolled UP from the live bottom (0 = the live tail). offset is
// clamped to [0, n-maxRows] so it can never point past the content;
// when the content already fits (n <= maxRows) s is returned verbatim
// (nothing to scroll — and this is the byte-identical short-thread
// path). This is the SINGLE windowing primitive: topTruncate delegates
// to it at offset 0 (its 2-arg contract + last-line-anchored behaviour
// are preserved exactly — the existing panel-height-invariant test
// calls topTruncate directly and stays green untouched), and the
// substrate feed calls it with a.scrollOffset.
func windowLines(s string, maxRows, offset int) string {
	if maxRows < 1 {
		maxRows = 1
	}
	lines := strings.Split(s, "\n")
	n := len(lines)
	if n <= maxRows {
		return s
	}
	maxOffset := maxScrollOffset(s, maxRows)
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	return strings.Join(lines[n-maxRows-offset:n-offset], "\n")
}

// maxScrollOffset is the SINGLE source of truth for the scrollback upper
// bound: the largest offset windowLines will accept for a string of s's
// rendered-line count at maxRows visible rows. It is exactly the clamp
// windowLines applies internally — n−maxRows, or 0 when the content
// already fits (n <= maxRows: nothing to scroll). #134: the Home key
// stores THIS value so the stored offset equals what the paint clamps to
// every frame (no sentinel → a subsequent PgDn pages back responsively).
// Because it is computed from the SAME rendered string the paint feeds
// windowLines, it is correct even when events wrap to multiple rendered
// lines (the true max = renderedLineCount−threadH, which can EXCEED the
// row count len(a.substrate) — a naive row-count cap would stop Home
// short of the oldest wrapped line; this does not).
func maxScrollOffset(s string, maxRows int) int {
	if maxRows < 1 {
		maxRows = 1
	}
	n := len(strings.Split(s, "\n"))
	if n <= maxRows {
		return 0
	}
	return n - maxRows
}

// topTruncate keeps at most the last maxRows lines of s (oldest lines
// dropped first). The PR-C conceptual-scroll clamp: when content
// overflows the panel, the LATEST rows stay visible. #134: this is now
// the offset-0 case of windowLines (the live tail) — its 2-arg contract
// and last-line-anchored behaviour are byte-identical to before;
// interactive scrollback is the windowLines call at the feed site.
func topTruncate(s string, maxRows int) string {
	return windowLines(s, maxRows, 0)
}

// padBottom appends blank lines to s until it has exactly rows lines
// (no-op if s already has >= rows lines). Keeps the composer / panel
// content pinned so the panel fills the terminal.
func padBottom(s string, rows int) string {
	lines := strings.Split(s, "\n")
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
