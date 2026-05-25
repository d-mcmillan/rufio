// project_walk.go — PR-G0: the filesystem-walking half of the pure
// projection layer (the learned/ memory walker, the live routing-
// delivery mesh-edge derivation, and the attention→mesh-node
// projection). Split from project.go purely for review locality; the
// same HARD CONSTRAINTS apply (pure, ADD-ONLY, deterministic, inject
// `now`, reuse the substrate libs, OPEN-2/OPEN-4 deferred). See
// project.go's file header.
package tui

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// ── learned/ memory walker ────────────────────────────────────────────

// walkLearned recursively walks <root>/learned/ for `.gdlm` files,
// parses every `@observation` record, and projects each to a
// MemoryEntry (fixtures.go:282-288). There is no existing learned/
// walker — recall/lineage scan outbox/expired, not learned/ — so this
// is net-new, but the per-record PARSE is the observation lib's format
// (observation.go:101-116 BuildObservationRecord field set: id, author,
// subject, predicate, object, scope, topics?, confidence, ts) read back
// via gdl.ParseDocument; we do NOT hand-roll the record parse (handoff:
// reuse the libs).
//
// Field mapping (MemoryEntry, fixtures.go:282-288):
//
//   - Subject   = @observation `subject`  (e.g. "customer:5821")
//   - Predicate = @observation `predicate`
//   - Object    = @observation `object`
//   - Author    = @observation `author`
//   - Ago       = tsToAgo(@observation `ts`, now)  ("2m"/"1h"/"2h"…,
//     fixtures.go:292-295) — `now` is INJECTED (handoff hard
//     constraint), never time.Now().
//
// Determinism: results are sorted by (Subject, ts, id-path) so a
// directory-walk order change can never reorder the rail. The on-disk
// layout is observation.SubjectPath = learned/<seg1>/<seg2>/<id>.gdlm
// (observation.go:64-78); the recursive walk handles arbitrarily-nested
// subjects (e.g. "customer:5821:contact" → learned/customer/5821/
// contact/). A missing learned/ dir → (nil, nil) — an empty knowledge
// base is not an error (matches attention.ReadAll / confirm.ReadAll's
// missing-dir → empty convention).
//
// Malformed/non-@observation records inside a .gdlm file are skipped
// (best-effort, matching lineage.WalkReasoning's
// skip-malformed-and-continue audit posture, lineage.go:300-305); a
// parse error on a whole file is also skipped rather than aborting the
// whole walk (one bad memory file must not blank the tab).
func walkLearned(root string, now time.Time) ([]MemoryEntry, error) {
	learnedDir := filepath.Join(root, "learned")
	if _, err := os.Stat(learnedDir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	type sortable struct {
		e    MemoryEntry
		ts   string // raw ts for stable secondary sort
		path string // rel path for stable tertiary sort
	}
	var rows []sortable

	walkErr := filepath.WalkDir(learnedDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable entry — best-effort: skip, don't abort the tab.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".gdlm") {
			return nil
		}
		bs, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil // skip unreadable file
		}
		records, parseErr := gdl.ParseDocument(string(bs))
		if parseErr != nil {
			return nil // skip malformed file (best-effort, lineage.go:303)
		}
		rel, _ := filepath.Rel(learnedDir, p)
		for _, r := range records {
			if r.Type != "observation" {
				continue
			}
			rows = append(rows, sortable{
				e: MemoryEntry{
					Subject:   r.Get("subject"),
					Predicate: r.Get("predicate"),
					Object:    r.Get("object"),
					Author:    r.Get("author"),
					Ago:       tsToAgo(r.Get("ts"), now),
				},
				ts:   r.Get("ts"),
				path: filepath.ToSlash(rel),
			})
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].e.Subject != rows[j].e.Subject {
			return rows[i].e.Subject < rows[j].e.Subject
		}
		if rows[i].ts != rows[j].ts {
			return rows[i].ts < rows[j].ts
		}
		return rows[i].path < rows[j].path
	})

	out := make([]MemoryEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.e)
	}
	return out, nil
}

// ── live mesh-edge derivation (routing deliveries) ────────────────────

// deriveMeshEdgesLive derives the substrate mesh edges from ROUTING
// DELIVERIES (data-mapping §0 OPEN-3-resolved :32-43): a thought id
// present in BOTH live/outbox/<A>/<id>.gdl AND live/inbox/<B>/<id>.gdl
// ⇒ an A–B edge. This is a DIFFERENT algorithm from the throwaway
// fixture deriveMeshEdges() (fixtures.go:375-426), which walks
// SubstrateThread; that one is fixture-only encoding. The live one is
// the real routing-delivery derivation OPEN-3 resolves to.
//
// The directional source is read from the inbox file's `@route` `from`
// field (routing.go:446-450 writes @route|to:<B>|from:<A>|ts:…). The
// outbox owner (the `<A>` directory) is the same author; we key the
// edge on (from, inbox-owner) so a misfiled @route still yields a
// sensible edge from the on-disk routing truth rather than guessing.
//
// Return shape MATCHES deriveMeshEdges()'s contract in spirit but uses
// [][2]string (agent-id pairs) rather than [][2]int (MeshNodes index
// pairs): G0 has NO MeshNodes table to index into (that hand-placed
// fixture is throwaway and node identity here comes from the live
// attention set via projectMeshNodes, not a fixed slice). G1 maps
// id-pairs → node indices against whatever node set projectMeshNodes
// produces. This is the documented shape gap — see the PR body. Each
// pair is ordered (lo, hi) lexicographically and the slice is sorted +
// de-duplicated for deterministic rendering/goldens. Self-edges
// (A delivered to its own inbox) are dropped.
//
// Missing live/outbox or live/inbox → no edges (empty, nil error) — an
// un-routed substrate is not an error (missing-dir → empty convention).
func deriveMeshEdgesLive(root string) ([][2]string, error) {
	outboxDir := filepath.Join(root, "live", "outbox")
	inboxDir := filepath.Join(root, "live", "inbox")

	// id → set of outbox owners that authored a file with this id.
	outboxOwners, err := collectIDOwners(outboxDir)
	if err != nil {
		return nil, err
	}
	if len(outboxOwners) == 0 {
		return nil, nil
	}

	seen := make(map[[2]string]bool)
	var edges [][2]string
	add := func(a, b string) {
		if a == "" || b == "" || a == b {
			return
		}
		if a > b {
			a, b = b, a
		}
		key := [2]string{a, b}
		if seen[key] {
			return
		}
		seen[key] = true
		edges = append(edges, key)
	}

	// Walk every inbox file; if its id was also produced into some
	// outbox, that's a delivery ⇒ an edge from the @route `from`
	// (source A) to the inbox owner (recipient B).
	inboxEntries, err := os.ReadDir(inboxDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, recipientDir := range inboxEntries {
		if !recipientDir.IsDir() {
			continue
		}
		recipient := recipientDir.Name()
		files, derr := os.ReadDir(filepath.Join(inboxDir, recipient))
		if derr != nil {
			return nil, derr
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".gdl") {
				continue
			}
			id := strings.TrimSuffix(f.Name(), ".gdl")
			owners, delivered := outboxOwners[id]
			if !delivered {
				// id present only in an inbox (no matching outbox file)
				// ⇒ NOT a derived edge (data-mapping §0 :39-40 requires
				// presence in BOTH). Also covers an id only in outbox:
				// it never appears in this inbox walk, so no edge.
				continue
			}
			from := readRouteFrom(filepath.Join(inboxDir, recipient, f.Name()))
			if from != "" {
				add(from, recipient)
				continue
			}
			// No @route from (defensive: malformed delivery) — fall
			// back to the outbox owner(s) as the source so on-disk
			// truth still yields an edge.
			for owner := range owners {
				add(owner, recipient)
			}
		}
	}

	sort.Slice(edges, func(i, j int) bool {
		if edges[i][0] != edges[j][0] {
			return edges[i][0] < edges[j][0]
		}
		return edges[i][1] < edges[j][1]
	})
	return edges, nil
}

// collectIDOwners walks live/outbox/<owner>/<id>.gdl and returns
// id → set-of-owners. Missing dir → empty map (not an error).
func collectIDOwners(outboxDir string) (map[string]map[string]bool, error) {
	owners := make(map[string]map[string]bool)
	entries, err := os.ReadDir(outboxDir)
	if errors.Is(err, fs.ErrNotExist) {
		return owners, nil
	}
	if err != nil {
		return nil, err
	}
	for _, ownerDir := range entries {
		if !ownerDir.IsDir() {
			continue
		}
		owner := ownerDir.Name()
		files, derr := os.ReadDir(filepath.Join(outboxDir, owner))
		if derr != nil {
			return nil, derr
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".gdl") {
				continue
			}
			id := strings.TrimSuffix(f.Name(), ".gdl")
			if owners[id] == nil {
				owners[id] = make(map[string]bool)
			}
			owners[id][owner] = true
		}
	}
	return owners, nil
}

// readRouteFrom parses an inbox file and returns the first @route
// record's `from` field (routing.go:446-450), or "" if absent/malformed.
// Parsed via the gdl lib — not a hand-rolled split.
func readRouteFrom(path string) string {
	bs, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return ""
	}
	for _, r := range records {
		if r.Type == "route" {
			return r.Get("from")
		}
	}
	return ""
}

// ── attention → mesh nodes ────────────────────────────────────────────

// projectMeshNodes projects the live attention set into mesh nodes
// (data-mapping §1 :119 — "Nodes = agents with an attention record").
// Input is the already-read attention.Attention slice
// (attention.ReadAll, attention.go:155-187 — already sorted by agent
// asc); G0 takes it as data so the projection is pure/testable without
// touching the filesystem here.
//
// It does NOT synthesize an `operator` node and does NOT decide
// presence: OPEN-4 (the operator/presence-node product policy) is the
// G2 decision, explicitly deferred (handoff hard constraint;
// data-mapping §0/§3 OPEN-4). projectMeshNodes only projects the agents
// that actually carry an attention record — nothing more.
//
// Placement: the fixture hand-places 4 nodes inside the 9×36 landscape
// rail (fixtures.go:340-345). G0 needs a DETERMINISTIC layout for
// ARBITRARY N, so nodes are fanned on a RADIAL ring centred in the
// 9×36 grid (origin top-left, r∈[0,8], c∈[0,35], matching MeshNode's
// documented coordinate space, fixtures.go:324-327). The fan starts at
// 12-o'clock and steps clockwise by 2π/N; radius is chosen so the ring
// fits the shorter (row) axis. Coordinates are rounded and clamped into
// the grid; on the rare collision after rounding the node is nudged to
// the next free cell in a deterministic scan so two nodes never stack.
// Output order follows the sorted input (deterministic given sorted
// input — attention.ReadAll already sorts by agent, attention.go:185).
//
// Glyph: every projected node is a spoke "●" (fixtures.go:342-345 — the
// "◉" hub is the operator, which G0 does NOT synthesize per OPEN-4).
// A single node (N==1) is centre-placed but still a spoke — hub-vs-spoke
// is the OPEN-4 policy, not a G0 call.
func projectMeshNodes(attns []attention.Attention) []MeshNode {
	if len(attns) == 0 {
		return nil
	}

	const rows, cols = 9, 36
	const cr, cc = (rows - 1.0) / 2.0, (cols - 1.0) / 2.0 // grid centre (4, 17.5)

	// Stable input order: copy + sort by agent so the projection is
	// deterministic even if a caller passes an unsorted slice (defence;
	// attention.ReadAll already sorts, attention.go:185).
	ids := make([]string, len(attns))
	for i, a := range attns {
		ids[i] = a.Agent
	}
	sort.Strings(ids)

	n := len(ids)
	out := make([]MeshNode, 0, n)
	used := make(map[[2]int]bool, n)

	place := func(r, c int) (int, int) {
		// Clamp into the grid.
		if r < 0 {
			r = 0
		}
		if r > rows-1 {
			r = rows - 1
		}
		if c < 0 {
			c = 0
		}
		if c > cols-1 {
			c = cols - 1
		}
		// Deterministic collision nudge: scan the grid row-major from
		// the target for the first free cell. Bounded (≤ rows*cols).
		if used[[2]int{r, c}] {
			for rr := 0; rr < rows; rr++ {
				for cc2 := 0; cc2 < cols; cc2++ {
					if !used[[2]int{rr, cc2}] {
						r, c = rr, cc2
						goto done
					}
				}
			}
		}
	done:
		used[[2]int{r, c}] = true
		return r, c
	}

	if n == 1 {
		r, c := place(int(math.Round(cr)), int(math.Round(cc)))
		return []MeshNode{{ID: ids[0], R: r, C: c, Glyph: "●"}}
	}

	// Radial fan: radius fits the shorter (row) axis so the ring never
	// clips top/bottom; the wider column axis gets the same radius
	// scaled by the grid aspect so the fan reads as a circle in the
	// landscape rail rather than an ellipse hugging the centre.
	const rowRadius = (rows - 1.0) / 2.0 // 4.0 — fills the 9-row height
	colRadius := colRadiusFor(rowRadius, rows, cols)
	for i, id := range ids {
		// Start at 12-o'clock (-π/2), step clockwise.
		theta := -math.Pi/2 + 2*math.Pi*float64(i)/float64(n)
		r := cr + rowRadius*math.Sin(theta)
		c := cc + colRadius*math.Cos(theta)
		rr, ccc := place(int(math.Round(r)), int(math.Round(c)))
		out = append(out, MeshNode{ID: id, R: rr, C: ccc, Glyph: "●"})
	}
	return out
}

// colRadiusFor scales the row radius by the grid aspect (cols/rows) so
// the radial fan reads as a circle in the 9×36 landscape rail (a raw
// equal radius would collapse to a thin vertical ellipse). Kept a named
// pure function so the layout math is documented + unit-testable.
func colRadiusFor(rowRadius float64, rows, cols int) float64 {
	return rowRadius * float64(cols-1) / float64(rows-1)
}
