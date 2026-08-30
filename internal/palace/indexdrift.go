package palace

import (
	"context"
	"fmt"
	"sort"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// driftBatch bounds how many ids are looked up per round, keeping the `IN (...)`
// list inside SQLite's parameter limit and the Qdrant request body small — the
// same reason deleteBatch exists.
const driftBatch = 500

// DriftedPoint is one stored point whose payload wing disagrees with the drawer
// it indexes.
//
// It names the STORE because there are two and they fail differently: a stale
// index makes a memory unreachable now, and a stale source of truth makes it
// unreachable again after the next sync. A repair that fixed one and not the
// other would look complete from either side alone.
type DriftedPoint struct {
	Store    string `json:"store"`
	DrawerID string `json:"drawer_id"`
	Indexed  string `json:"indexed_wing"`
	Actual   string `json:"actual_wing"`
	// Missing marks a drawer the store holds NO point for. It is a different and
	// worse fault than a wrong label: a mislabelled memory answers the wrong wing,
	// an absent one answers nothing at all.
	Missing bool `json:"missing,omitempty"`
}

// NamespaceSplit names a count split across the two namespaces, so every
// per-namespace raw field has a named residence instead of one combined number.
// The recovery tools act per namespace (rebuild/relabel take a namespace), so
// the breakdown is visible wherever the blend is reported.
type NamespaceSplit struct {
	Drawers int `json:"drawers"`
	Closets int `json:"closets"`
}

// DriftReport is what IndexDrift found. Drifted is sorted for a stable report and
// bounded: a fully drifted palace must produce a report an operator can read and
// a process can hold in memory, so the count is exact and the listing is a sample.
//
// The counters are per store half and per namespace: the source-of-truth half's
// drift does not depress a SERVING number (it is invisible to serving until the
// next sync), so coverage is built from the Index* counters only, while the Sot*
// counters keep the other half's state in the same report. Checked and Pending
// split the same way, so expected and pending have a named residence per
// namespace rather than one combined number.
type DriftReport struct {
	Checked          NamespaceSplit `json:"checked"`
	Total            int            `json:"total_drifted"`
	Drifted          []DriftedPoint `json:"drifted"`
	Pending          NamespaceSplit `json:"pending_embedding"`
	IndexMissing     NamespaceSplit `json:"index_missing"`
	IndexMislabelled NamespaceSplit `json:"index_mislabelled"`
	// IndexCount is the index half's REAL point population per namespace, read
	// from the index's own Count rather than derived from the checked rows. An
	// over-count (orphans, or the transient upsert-before-stamp window) renders
	// Indexed > Expected in the coverage view; without it the raw fields show
	// indexed == expected and the over-count is indistinguishable from a perfect
	// index (ADR-033 R3).
	IndexCount     NamespaceSplit `json:"index_count"`
	SotMissing     NamespaceSplit `json:"sot_missing"`
	SotMislabelled NamespaceSplit `json:"sot_mislabelled"`
}

// NamespaceCoverageView is one namespace's serving-health view: the number plus
// the raw fields it is built from, so a clamped or over-counted value never
// hides the inputs. Indexed is the INDEX's REAL point population — an over-count
// (orphans, or the transient upsert-before-stamp window) reads indexed >
// expected, never masked as a perfect index — while Expected is the row count
// that should hold a point.
type NamespaceCoverageView struct {
	Coverage    float64 `json:"coverage"`
	Expected    int     `json:"expected"`
	Indexed     int     `json:"indexed"`
	Missing     int     `json:"index_missing"`
	Mislabelled int     `json:"index_mislabelled"`
}

// Coverage is the serving-side population health number: the fraction of rows
// that should hold a point in the index half that actually do. The source of
// truth half's drift does not depress it (that half does not serve) and is
// reported separately. Pending rows are excluded from the denominator by
// construction — Checked is embedded rows only — so a busy palace reads healthy.
// Nothing embedded yet is 1.0, vacuously.
func (r DriftReport) Coverage() float64 {
	return coverage(r.Checked.Drawers+r.Checked.Closets,
		r.IndexMissing.Drawers+r.IndexMissing.Closets,
		r.IndexMislabelled.Drawers+r.IndexMislabelled.Closets)
}

// CoverageView reports the serving coverage per namespace, each with the raw
// fields it is built from. The blend (Coverage) is the row-weighted version of
// these two numbers. Indexed is the index half's REAL population (IndexCount),
// so an over-count renders indexed > expected instead of saturating at expected.
func (r DriftReport) CoverageView() map[string]NamespaceCoverageView {
	return map[string]NamespaceCoverageView{
		"drawers": coverageView(r.Checked.Drawers, r.IndexCount.Drawers, r.IndexMissing.Drawers, r.IndexMislabelled.Drawers),
		"closets": coverageView(r.Checked.Closets, r.IndexCount.Closets, r.IndexMissing.Closets, r.IndexMislabelled.Closets),
	}
}

func coverageView(expected, indexed, missing, mislabelled int) NamespaceCoverageView {
	return NamespaceCoverageView{
		Coverage:    coverage(expected, missing, mislabelled),
		Expected:    expected,
		Indexed:     indexed,
		Missing:     missing,
		Mislabelled: mislabelled,
	}
}

// coverage is the index-half serving number, clamped to [0, 1]. The clamp is
// defensive: missing and mislabelled are disjoint subsets of expected, so a
// negative value is reachable only through a counting bug — and the clamp must
// not hide the inputs, which is why the raw fields ride alongside in
// CoverageView. An over-count (indexed > expected, orphans) saturates at 1.0;
// the raw indexed/expected fields carry the divergence.
func coverage(expected, missing, mislabelled int) float64 {
	if expected == 0 {
		return 1.0 // nothing embedded yet is vacuously healthy
	}
	c := float64(expected-missing-mislabelled) / float64(expected)
	if c < 0 {
		return 0
	}
	if c > 1 {
		return 1
	}
	return c
}

// driftSample bounds the listing. The COUNT is always exact; only the listing is
// capped, because a palace whose index was rebuilt into the wrong shape would
// otherwise print a line per memory.
const driftSample = 50

// Clean reports whether every point agrees with its drawer.
func (r DriftReport) Clean() bool { return r.Total == 0 }

// Truncated reports whether the listing is a sample of a larger set.
func (r DriftReport) Truncated() bool { return r.Total > len(r.Drifted) }

// splitStore is a VectorStore that pairs a durable store with a search index.
// Both copies of a payload must agree with the rows, so a check that could see
// only one of them would report clean while the other was wrong.
type splitStore interface {
	Halves() (store.SourceOfTruth, store.VectorStore)
}

// IndexDrift reports every stored point whose payload wing no longer matches the
// wing its drawer is filed in.
//
// This is not a hypothetical consistency check. A scoped search filters at the
// INDEX, on the payload — Search passes the wing to the vector store, and the
// drawer-row comparison that follows can only remove candidates, never add one
// back — so a point whose payload says the wrong wing is unreachable from the
// wing it actually lives in. Measured 2026-08-21 on a live palace: 13 of 359
// points had drifted that way after wing merges, in BOTH stores, and the
// memories were returned only by an unscoped search.
//
// It reads and never writes. The repair is a separate operation on purpose: a
// checker that also fixes cannot be trusted to report honestly about its own
// fixes, and this report is the acceptance for the code that does the fixing.
func (s *Service) IndexDrift(ctx context.Context, teamID string) (DriftReport, error) {
	var report DriftReport

	// Name each store so a reader can tell which half is wrong. The index half
	// also supplies the REAL population count: the per-id audit below only asks
	// for drawer ids, so an over-count (orphans, or the transient
	// upsert-before-stamp window) is invisible to it — the coverage view's
	// indexed field must come from the index's own Count (ADR-033 R3).
	var indexHalf store.VectorStore
	stores := []struct {
		name string
		vs   store.VectorStore
	}{}
	if split, ok := s.vectors.(splitStore); ok {
		sot, index := split.Halves()
		indexHalf = index
		stores = append(stores, struct {
			name string
			vs   store.VectorStore
		}{"source of truth", sot}, struct {
			name string
			vs   store.VectorStore
		}{"index", index})
	} else {
		indexHalf = s.vectors
		stores = append(stores, struct {
			name string
			vs   store.VectorStore
		}{"index", s.vectors})
	}

	// Best-effort: the real population is a display input, so a count failure
	// leaves the raw field absent rather than failing the read-only check (the
	// audit below fails first when the half is genuinely down).
	if n, err := indexHalf.Count(ctx, teamID); err == nil {
		report.IndexCount.Drawers = n
	}

	// Closets are a second namespace with a second copy of the same wing, and a
	// check that only looked at drawers reported clean over a split closet index.
	closetWings, err := s.repo.ClosetWings(ctx, teamID)
	if err != nil {
		return DriftReport{}, fmt.Errorf("load closet wings: %w", err)
	}

	wings, pending, err := s.repo.DrawerWings(ctx, teamID)
	if err != nil {
		return DriftReport{}, fmt.Errorf("load drawer wings: %w", err)
	}
	pendingClosets, err := s.repo.PendingClosetCount(ctx, teamID)
	if err != nil {
		return DriftReport{}, fmt.Errorf("count pending closets: %w", err)
	}
	report.Checked.Drawers = len(wings)
	report.Pending.Drawers = len(pending)
	report.Pending.Closets = int(pendingClosets)

	ids := make([]string, 0, len(wings))
	for id := range wings {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic batching, so a truncated run is repeatable

	record := func(d DriftedPoint) {
		report.Total++
		if len(report.Drifted) < driftSample {
			report.Drifted = append(report.Drifted, d)
		}
	}

	for _, st := range stores {
		missing := &report.IndexMissing
		mislabelled := &report.IndexMislabelled
		if st.name == "source of truth" {
			missing, mislabelled = &report.SotMissing, &report.SotMislabelled
		}
		for start := 0; start < len(ids); start += driftBatch {
			end := start + driftBatch
			if end > len(ids) {
				end = len(ids)
			}
			batch := ids[start:end]
			points, err := st.vs.PointsByIDs(ctx, teamID, batch)
			if err != nil {
				return DriftReport{}, fmt.Errorf("read points from the %s: %w", st.name, err)
			}
			// Index what came back, so a point the store did NOT return can be
			// noticed. Reading only the returned points made an omission read as
			// agreement: a memory the index had lost entirely — unreachable by any
			// search, not merely by a scoped one — reported clean.
			seen := make(map[string]string, len(points))
			for _, p := range points {
				if _, asked := wings[p.ID]; !asked {
					// A point the caller did not ask for. Comparing it against an
					// absent row would invent drift out of a driver's own bug.
					continue
				}
				indexed, _ := p.Payload["wing"].(string)
				seen[p.ID] = indexed
			}
			for _, id := range batch {
				indexed, ok := seen[id]
				if !ok {
					record(DriftedPoint{Store: st.name, DrawerID: id, Actual: wings[id], Missing: true})
					missing.Drawers++
					continue
				}
				if indexed != wings[id] {
					record(DriftedPoint{Store: st.name, DrawerID: id, Indexed: indexed, Actual: wings[id]})
					mislabelled.Drawers++
				}
			}
		}
	}
	// The closet namespace, checked the same way. Its ids live under a different
	// namespace, so it cannot share the loop above.
	if len(closetWings) > 0 {
		closetIDs := make([]string, 0, len(closetWings))
		for id := range closetWings {
			closetIDs = append(closetIDs, id)
		}
		sort.Strings(closetIDs)
		report.Checked.Closets = len(closetIDs)
		ns := closetNamespace(teamID)
		if n, err := indexHalf.Count(ctx, ns); err == nil {
			report.IndexCount.Closets = n
		}
		for _, st := range stores {
			missing := &report.IndexMissing
			mislabelled := &report.IndexMislabelled
			if st.name == "source of truth" {
				missing, mislabelled = &report.SotMissing, &report.SotMislabelled
			}
			for start := 0; start < len(closetIDs); start += driftBatch {
				end := start + driftBatch
				if end > len(closetIDs) {
					end = len(closetIDs)
				}
				batch := closetIDs[start:end]
				points, err := st.vs.PointsByIDs(ctx, ns, batch)
				if err != nil {
					return DriftReport{}, fmt.Errorf("read closet points from the %s: %w", st.name, err)
				}
				seen := make(map[string]string, len(points))
				for _, p := range points {
					if _, asked := closetWings[p.ID]; !asked {
						continue
					}
					indexed, _ := p.Payload["wing"].(string)
					seen[p.ID] = indexed
				}
				for _, id := range batch {
					indexed, ok := seen[id]
					if !ok {
						record(DriftedPoint{Store: st.name + " (closets)", DrawerID: id, Actual: closetWings[id], Missing: true})
						missing.Closets++
						continue
					}
					if indexed != closetWings[id] {
						record(DriftedPoint{Store: st.name + " (closets)", DrawerID: id, Indexed: indexed, Actual: closetWings[id]})
						mislabelled.Closets++
					}
				}
			}
		}
	}

	sort.Slice(report.Drifted, func(a, b int) bool {
		x, y := report.Drifted[a], report.Drifted[b]
		if x.Store != y.Store {
			return x.Store < y.Store
		}
		return x.DrawerID < y.DrawerID
	})
	return report, nil
}
