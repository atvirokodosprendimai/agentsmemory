package palace

import "context"

// WingPlacement is where a fact sits relative to the wing being searched.
//
// Three values, not two, because provenance does not always resolve. Measured
// 2026-08-26 on the live palace: of 196 triples, 106 carry a source_drawer_id,
// 90 resolve to a drawer that exists, and 16 dangle. So the majority of facts
// cannot be placed in ANY wing, and a design with only "here" and "elsewhere"
// would have to lie about them — either claiming them for the searched wing,
// which leaks, or dropping them silently, which is the failure this whole
// decision exists to remove.
type WingPlacement string

// The three placements. See WingPlacement for why there is no fourth.
const (
	// PlacementLocal: provenance resolves to a drawer in the searched wing.
	PlacementLocal WingPlacement = "local"
	// PlacementForeign: provenance resolves to a drawer in a DIFFERENT wing. The
	// wing is named so an agent can go and query it; the content never crosses.
	PlacementForeign WingPlacement = "foreign"
	// PlacementUnlocatable: provenance is absent or dangles. Counted, never
	// attributed — a fact that cannot be placed is not evidence about the
	// searched wing.
	PlacementUnlocatable WingPlacement = "unlocatable"
)

// WingPolicy is the ONE place that decides whether something may cross a wing
// boundary, and what a caller is told when it may not.
//
// It exists as a type rather than a helper because F-19 requires one rule across
// four response paths — the fact block, the sibling pointer, an entry point's
// outgoing edges, and the bootstrap's inline content. Four filters that agree the
// day they are written diverge on the path nobody tested, and the one that
// diverges is a tenancy leak rather than a formatting bug.
// NOT SAFE FOR CONCURRENT USE. wingPolicyFor gives each call its own instance
// with its own resolution cache, and no caller fans out today — verified
// 2026-08-26 across service.go, memory_search.go, eval.go, bootstrap.go and
// graphquery.go, with `go test -race` clean. That safety is held by CONVENTION,
// not by construction: a future caller that resolves several records in parallel
// would race on the cache and the tests would not show it. Give such a caller its
// own policy, or put a mutex here before sharing one.
type WingPolicy struct {
	// Viewer is the wing being searched. Empty means an unscoped recall, where
	// every resolvable fact is local by definition.
	Viewer string
	// wingOf resolves a drawer id to the wing that holds it. Injected so the
	// policy can be driven in a test without a store, and so every caller shares
	// one resolution rule rather than each doing its own join.
	wingOf func(ctx context.Context, drawerID string) (string, bool)
}

// NewWingPolicy builds the policy for one recall.
func NewWingPolicy(viewer string, wingOf func(ctx context.Context, drawerID string) (string, bool)) WingPolicy {
	return WingPolicy{Viewer: viewer, wingOf: wingOf}
}

// Place classifies one item by the provenance it carries.
//
// An empty or unresolvable sourceDrawerID is UNLOCATABLE, never local. That
// asymmetry is the whole point: defaulting an unplaceable fact into the searched
// wing would return another project's content under this project's name, and it
// would do so most of the time on today's corpus.
func (p WingPolicy) Place(ctx context.Context, sourceDrawerID string) (WingPlacement, string) {
	if sourceDrawerID == "" || p.wingOf == nil {
		return PlacementUnlocatable, ""
	}
	wing, ok := p.wingOf(ctx, sourceDrawerID)
	if !ok || wing == "" {
		return PlacementUnlocatable, ""
	}
	if p.Viewer == "" || wing == p.Viewer {
		return PlacementLocal, wing
	}
	return PlacementForeign, wing
}

// MayReturnContent reports whether an item's CONTENT may be returned to this
// viewer. Only local content may. A foreign item contributes its wing NAME to the
// pointer and nothing else, and an unlocatable one contributes only to a count.
func (p WingPolicy) MayReturnContent(placement WingPlacement) bool {
	return placement == PlacementLocal
}

// wingPolicyFor builds the policy for one recall, resolving drawer wings from the
// store. It is the shared constructor every response path uses, so "which wing is
// this in" is answered one way rather than per call site.
func (s *Service) wingPolicyFor(ctx context.Context, teamID, viewer string) WingPolicy {
	cache := map[string]string{}
	return NewWingPolicy(viewer, func(ctx context.Context, id string) (string, bool) {
		if w, ok := cache[id]; ok {
			return w, w != ""
		}
		wings, err := s.repo.WingsForDrawers(ctx, teamID, []string{id})
		if err != nil {
			return "", false
		}
		w := wings[id]
		cache[id] = w
		return w, w != ""
	})
}
