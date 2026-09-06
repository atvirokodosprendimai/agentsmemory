package palace

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
)

// BootstrapTier separates content that travels INLINE from content that travels
// as a pointer.
//
// The distinction is the whole reason a bootstrap is smaller than the protocol it
// replaces. Inlining everything reproduces the problem — a client-side protocol
// measured at ~99KB — inside one call, and pointing at everything reproduces the
// 13 calls.
type BootstrapTier string

// The two tiers. The server distinguishes eager from on-demand; which drawers a
// team calls which is a team convention the server does not bless.
const (
	// TierEager: content a session needs before it can do anything, sent inline.
	TierEager BootstrapTier = "eager"
	// TierOnDemand: content a session may need, sent as an id to fetch.
	TierOnDemand BootstrapTier = "on_demand"
	// TierMust and TierRef: the two-tier entry the shipped protocol tells an
	// agent to author on the wing's by-name ROOT, served as pointers. These are
	// the team convention the server did not bless until it had to: the protocol
	// named am_bootstrap as the call that serves the tier, and it served the
	// entry ROOM's overflow instead (issue #218).
	TierMust BootstrapTier = "must"
	TierRef  BootstrapTier = "ref"
)

// BootstrapPointer names something the response did not inline.
type BootstrapPointer struct {
	ID   string        `json:"id"`
	Tier BootstrapTier `json:"tier"`
	// Fetch is the call that retrieves it. A pointer without the call that
	// resolves it is a riddle: the protocol this replaces lost 74% of a
	// prescribed tier to an unreported cap, and reporting "3 omitted" without
	// saying how to get them repeats that in a politer form.
	Fetch string `json:"fetch"`
	// Hint is the edge's source_file — the label an author puts on a leaf so a
	// session can decide whether to fetch it without fetching it. Only tier
	// pointers carry one; an unlabelled leaf arrives with it empty, which the
	// protocol forbids and the server does not enforce.
	Hint string `json:"hint,omitempty"`
	// Under is the node the leaf hangs from (wing_x.root.must.ops), which is the
	// only thing that says what a leaf is FOR when several namespaces share a
	// tier.
	Under string `json:"under,omitempty"`
}

// BootstrapTruncation reports what a bounded response left out.
//
// Always present, never inferred from a short list: a caller cannot tell a
// complete answer from a capped one by counting, which is exactly how the
// original loss went unnoticed.
type BootstrapTruncation struct {
	Omitted int    `json:"omitted"`
	Reason  string `json:"reason,omitempty"`
	// HowToFetch names the call that retrieves what was dropped.
	HowToFetch string `json:"how_to_fetch,omitempty"`
	// TiersOmitted counts tier leaves this response neither listed nor could
	// list: refused by WingPolicy, past bootstrapTierLimit, or on a node whose
	// out-degree overflowed one graph page. Separate from Omitted because the
	// two are fetched differently and a single number would hide which.
	TiersOmitted int `json:"tiers_omitted,omitempty"`
}

// BootstrapResult is everything a session needs to start work in a wing, in ONE
// call: no second round trip, and no id carried in from a skill file.
type BootstrapResult struct {
	// Wing is the wing this bootstraps, resolved and echoed.
	Wing string `json:"wing"`
	// EntryPoint is the wing's front door, from T7's direct resolution.
	EntryPoint EntryPointResult `json:"entry_point"`
	// Eager is the inline content.
	Eager []Drawer `json:"eager,omitempty"`
	// OnDemand names what was not inlined.
	OnDemand []BootstrapPointer `json:"on_demand,omitempty"`
	// Tiers are the must and ref leaves authored on the wing's by-name root,
	// as pointers with their hints — never inline. The tier exists so a wing
	// carries a small always-loaded spine and points at everything else, and a
	// bootstrap that inlined it would be the 99KB protocol it replaced. Before
	// this field the tier was reachable only by am_kg_query("<wing>.root"), a
	// call a waking session has no reason to make (issue #218).
	Tiers []BootstrapPointer `json:"tiers,omitempty"`
	// Corrections are the retracts/supersedes/qualifies edges already swept
	// server-side, so a session that bootstraps perfectly does not still read
	// whatever the tier got wrong and believe it.
	Corrections map[string][]Correction `json:"corrections,omitempty"`
	// Truncation says what this response left out and how to get it.
	Truncation BootstrapTruncation `json:"truncation"`
}

// BootstrapEagerLimit bounds the inline tier. A bootstrap that grows without
// limit becomes the thing it replaced.
// ⚠ EXPORTED SO THE SENTENCES DESCRIBING IT CANNOT FREEZE. Review of PR #325:
// "first ten" had just been typed into three shipped surfaces, deriving from an
// unexported constant, which is how "roughly 800 characters" came to sit in seven
// documents against a ChunkSize of 1600. am_add_drawer's description already
// interpolates palace.ChunkSize for exactly this reason.
const BootstrapEagerLimit = 10

// bootstrapTierLimit bounds the tier pointers. A pointer with a hint is ~200
// bytes, and the protocol's own invariant is at most ~35 leaves per node, so a
// six-namespace must tier could legally reach 200 leaves — 40KB of pointers on
// the one call no session skips. The bound is generous for a curated tier and
// hostile to an uncurated one, which is the right way round; what it cuts is
// counted in TiersOmitted and reachable by the walk it names.
const bootstrapTierLimit = 64

// Bootstrap assembles a wing's starting context in one call.
//
// It replaces a client-side protocol measured at 13 calls and ~99KB, which every
// session paid before it could do anything, and which required a hardcoded root
// drawer id that only worked in one wing.
//
// Every piece is CONSUMED rather than reimplemented: the entry point is T7's, the
// corrections are T5's single incoming sweep, and the wing rule is T3's
// WingPolicy. Two implementations of the same rule diverge on the path nobody
// tested, and here that path would be a tenancy boundary.
func (s *Service) Bootstrap(ctx context.Context, teamID, wing string) (BootstrapResult, error) {
	// Required means present, not non-empty: an empty wing would make WingPolicy
	// read every resolvable record as local. Refused at the service boundary so
	// the guarantee does not depend on which caller got there.
	if strings.TrimSpace(wing) == "" {
		return BootstrapResult{}, fmt.Errorf("%w: bootstrap needs a wing", ErrInvalidInput)
	}
	out := BootstrapResult{Wing: wing}

	// Direct resolution, not a graph walk. am_traverse's max_hops is inert, so a
	// bootstrap built on it would silently return only hop 1 while looking
	// correct — which is why F-17 is asserted against THIS surface and not only
	// against EntryPoint.
	entry, err := s.EntryPoint(ctx, teamID, wing)
	if err != nil {
		return BootstrapResult{}, err
	}
	out.EntryPoint = entry

	policy := s.wingPolicyFor(ctx, teamID, wing)

	// Collect the ids the entry point points at, in order.
	ids := make([]string, 0, len(entry.Edges))
	for _, e := range entry.Edges {
		if e.Object != "" {
			ids = append(ids, e.Object)
		}
	}

	// Everything past the eager bound becomes a pointer rather than vanishing.
	inline, deferred := ids, []string(nil)
	if len(ids) > BootstrapEagerLimit {
		inline, deferred = ids[:BootstrapEagerLimit], ids[BootstrapEagerLimit:]
	}

	// Omitted means: a record the wing offered that this response NEITHER inlined
	// NOR named as a pointer. Nothing else.
	//
	// It previously counted len(deferred) wholesale, which counted the pointers as
	// omitted although they are named right there in the response — and then a
	// dead increment above it tried to count policy-refused ids into a field that
	// was reassigned two lines later. Both errors pointed the same way: the number
	// described the tier split rather than the loss.
	//
	// The loss is what matters, because an eager-tier record refused by WingPolicy
	// or missing from the store used to vanish with no count at all — the exact
	// silent drop F-18 exists to remove, one response surface over from where
	// F-18 removed it.
	//
	// It starts at the ENTRY filter's count, because that is where the loss
	// actually occurs: every id in `ids` has already passed MayReturnContent
	// inside EntryPoint, so the tier-level refusal branches below can fire only
	// when the store mutates between the two checks. Counting only the tiers made
	// Omitted structurally zero while the entry node's out-degree and the
	// response's account visibly disagreed. With the refusals seeded here,
	// eager + pointers + omitted partitions the entry node's full offer.
	omitted := entry.Refused

	if len(inline) > 0 {
		drawers, err := s.repo.DrawersByIDs(ctx, teamID, inline)
		if err != nil {
			return BootstrapResult{}, err
		}
		// A record over ChunkSize is several rows sharing a parent, and DrawersByIDs
		// returns the ROOT. Serving that row alone gave a session the first 1600
		// runes of the entry protocol and reported truncation.omitted: 0 — measured
		// 2026-09-01 on a 3,600-rune record, cut mid-sentence with nothing marking
		// it partial. That silent cut is what prepareWrite's entry-room refusal
		// existed to prevent, which made the refusal a workaround for this bug.
		//
		// ByRoots and not per id: this is the one call no session skips, and
		// resolving up to BootstrapEagerLimit memories one at a time would be N
		// queries for an answer one query gives. Reassembly is reassembleMemory,
		// the SAME function the search path uses (memory_search.go) — a second
		// implementation would be a second answer to one question, and the seam it
		// removes is exactly where a hand-rolled join goes wrong.
		chunks, err := s.repo.MemoryChunksByRoots(ctx, teamID, inline)
		if err != nil {
			return BootstrapResult{}, err
		}
		found := make(map[string]bool, len(drawers))
		for _, d := range drawers {
			found[d.ID] = true
			// The bootstrap's inline content goes through the SAME wing rule as
			// every other response path. An entry edge can name a record in
			// another wing, and inlining it here would be the leak that a
			// subject/predicate/object check never sees.
			//
			// ⚠ Placed on the ROOT's id, before reassembly and unchanged by it. The
			// rule is about where the memory is filed, and every chunk of a memory
			// shares that — deciding on a reassembled body would be deciding on
			// text rather than on placement.
			placement, _ := policy.Place(ctx, d.ID)
			if policy.MayReturnContent(placement) {
				if whole := reassembleMemory(chunks[d.ID]); whole != "" {
					d.Content = whole
				}
				out.Eager = append(out.Eager, d)
				continue
			}
			omitted++
		}
		// An id the entry point named and the store no longer holds is a loss
		// too, and a dangling pointer is precisely what a reader cannot discover
		// by counting what arrived.
		for _, id := range inline {
			if !found[id] {
				omitted++
			}
		}
	}

	for _, id := range deferred {
		// A pointer names an id AND the call that fetches it, so an unauthorized
		// pointer is actionable disclosure rather than an inert string. Placed
		// like every other exit; refused ones are counted, never listed.
		placement, _ := policy.Place(ctx, id)
		if !policy.MayReturnContent(placement) {
			omitted++
			continue
		}
		out.OnDemand = append(out.OnDemand, BootstrapPointer{
			ID: id, Tier: TierOnDemand, Fetch: "am_get_drawer",
		})
	}

	out.Truncation = BootstrapTruncation{Omitted: omitted}
	if omitted > 0 {
		out.Truncation.Reason = "beyond the eager tier's bound, or not readable from this wing"
		// Only claims what is true: the pointers are fetchable, the refused ids
		// are not, and saying "fetch each id in on_demand" while the count also
		// covers ids deliberately absent from that list was a false instruction.
		out.Truncation.HowToFetch = "am_get_drawer for each id in on_demand; the remainder is not readable from this wing"
	}

	// The tier the protocol authors on the wing ROOT. EntryPoint resolves the
	// entry ROOM node, and nothing above walks from one to the other, which is
	// why a correctly built must/ref tier returned on_demand: null (issue #218).
	tiers, tiersOmitted, err := s.tierPointers(ctx, teamID, wing, policy)
	if err != nil {
		return BootstrapResult{}, err
	}
	out.Tiers = tiers
	out.Truncation.TiersOmitted = tiersOmitted
	if tiersOmitted > 0 {
		out.Truncation.HowToFetch = strings.TrimSpace(out.Truncation.HowToFetch +
			" — am_kg_query(entity: \"" + WingRootSubject(wing) + ".must\" or \".ref\", direction: \"outgoing\") walks the tier the pointers were cut from")
	}

	// T5's sweep, consumed. Corrections attach as INCOMING edges, so no outgoing
	// walk from a bootstrapped record can see that it has been retracted — which
	// is why a session that bootstraps perfectly still reads what the tier got
	// wrong unless the server sweeps for it.
	all := append(append([]string{}, inline...), deferred...)
	corrections, err := s.CorrectionsFor(ctx, teamID, all, policy)
	if err != nil {
		return BootstrapResult{}, err
	}
	if len(corrections) > 0 {
		out.Corrections = corrections
	}
	return out, nil
}

// BootstrapBaseline is the REDACTED record of the client-side protocol this
// surface replaces: how many calls it took and what it cost, with no transcript
// content. The transcript itself stays untracked under ADR-003 T2, which closed
// committing such material permanently.
type BootstrapBaseline struct {
	Calls        int `json:"calls"`
	OutputTokens int `json:"output_tokens"`
	Bytes        int `json:"bytes"`
	// Tokenizer names what counted the tokens. A cost comparison under an unnamed
	// tokenizer compares two different units and reports the difference as a win.
	Tokenizer  string `json:"tokenizer"`
	ModelBuild string `json:"model_build"`
	Date       string `json:"date"`
	Provenance string `json:"provenance"`
}

// LoadBootstrapBaseline reads the redacted baseline manifest.
func LoadBootstrapBaseline(path string) (BootstrapBaseline, error) {
	var b BootstrapBaseline
	raw, err := os.ReadFile(path)
	if err != nil {
		return b, fmt.Errorf("open baseline: %w", err)
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// The parts the 13-call protocol delivered. Any replacement must deliver each
// one that the wing actually has, and this list is the canonical spelling of them
// so a test and the checker cannot drift apart.
const (
	parityEntryPoint  = "entry point"
	parityWing        = "resolved wing"
	parityEager       = "eager content"
	parityPointers    = "on-demand pointers"
	parityCorrections = "corrections"
	parityTruncation  = "truncation report"
)

// BootstrapOffer is what the wing HAS, independent of what the response carried.
//
// Parity needs both halves. Checking the response alone is either vacuous — an
// empty wing legitimately carries no eager content, so nothing can be demanded —
// or wrong, because demanding content from an empty wing fails a correct answer.
// Comparing against the offer is what makes "did it deliver the same payload"
// answerable.
type BootstrapOffer struct {
	Records     int
	Corrections int
}

// MissingParityParts names the parts of the replaced protocol this response does
// not carry, given what the wing had to offer. Empty means parity holds.
//
// An earlier version checked only the entry point, a non-empty wing, and one
// truncation condition — so removing eager assembly, pointer assembly or the
// correction sweep entirely still passed, and F-16 degraded into a bare token
// comparison that the cheapest response wins by returning nothing.
func (r BootstrapResult) MissingParityParts(offer BootstrapOffer) []string {
	var missing []string
	if r.EntryPoint.Resolution == "" {
		missing = append(missing, parityEntryPoint)
	}
	if r.Wing == "" {
		missing = append(missing, parityWing)
	}
	// A wing with records must deliver them, as inline content or as pointers.
	// Which of the two is the tier split's business; that NEITHER is empty when
	// the wing has records is parity's.
	if offer.Records > 0 {
		if len(r.Eager) == 0 && len(r.OnDemand) == 0 {
			missing = append(missing, parityEager)
		}
		// Eager, pointers and omissions PARTITION the offer — every record is in
		// exactly one — so this sum is exact rather than a lower bound. It used to
		// double-count, because Omitted was len(deferred) and every pointer was
		// also inside deferred, which meant a response that silently dropped eager
		// records still cleared the check the drop exists to catch.
		if len(r.Eager)+len(r.OnDemand)+r.Truncation.Omitted < offer.Records {
			missing = append(missing, parityPointers)
		}
	}
	// Corrections the graph holds for these records must arrive. This is the part
	// the client-side protocol needed three separate predicate queries for, and
	// dropping it silently is the failure that made a perfect bootstrap read
	// something already contradicted.
	if offer.Corrections > 0 && len(r.Corrections) == 0 {
		missing = append(missing, parityCorrections)
	}
	// The truncation report is unconditional: its absence is exactly the failure
	// that lost 74% of a tier unnoticed.
	if r.Truncation.Omitted > 0 && r.Truncation.HowToFetch == "" {
		missing = append(missing, parityTruncation)
	}
	return missing
}

// WireShape is the response as the MCP layer actually emits it.
//
// The token comparison used to marshal the internal struct, which carries
// `omitempty` on nearly every field and a different shape from the map the tool
// handler builds. So it measured something no caller ever receives, and measured
// it smaller. A cost gate must count the bytes that leave the process.
func (r BootstrapResult) WireShape() map[string]any {
	return map[string]any{
		"wing":        r.Wing,
		"entry_point": r.EntryPoint,
		"eager":       r.Eager,
		"on_demand":   r.OnDemand,
		"tiers":       r.Tiers,
		"corrections": r.Corrections,
		"truncation":  r.Truncation,
	}
}

// OutputTokens estimates this response's cost in tokens, counting the JSON the
// MCP layer emits.
//
// The estimate is ~4 bytes per token and it FAILS CLOSED: a marshal error returns
// the largest possible count rather than zero, because a cost gate that reports
// "free" when it cannot measure is a gate that passes hardest exactly when
// something is wrong.
//
// Crude on purpose, and honest about it: 4 bytes/token is a rough English
// average and is optimistic for ids and non-English text. It is adequate only
// because the baseline it is compared against is nearly an order of magnitude
// larger. If a response ever lands within ~2x of the baseline, stop estimating
// and tokenize with the manifest's named tokenizer — the error bar would then be
// wider than the margin, and the gate would be asserting something it cannot see.
func (r BootstrapResult) OutputTokens() int {
	raw, err := json.Marshal(r.WireShape())
	if err != nil {
		return math.MaxInt32
	}
	return len(raw) / 4
}

// tierPointers follows the must and ref edges from a wing's by-name root and
// returns their leaves as pointers, bounded and counted.
//
// It follows EDGES rather than assuming names: the protocol says the tier node
// is `<wing>.root.must`, but the root's `must` edge is what an author actually
// wrote, and the object it names is the tier wherever it points. Below the tier
// a node is structural when its name carries the `<wing>.root.` prefix and a
// leaf otherwise — the protocol's own naming rule, and the only thing that keeps
// this walk from descending into a DRAWER id, whose outgoing fan-out is the
// 63KB spill start-here warns every session against. Derived edges are skipped
// because the root's `holds` edge names a room, not a record.
//
// Two bounds, both reported: bootstrapTierLimit on the pointers, and one graph
// page per node — a node past DefaultKGQueryLimit edges has broken the tier's
// ~35-leaf invariant, and its overflow is counted once rather than paged, since
// a bootstrap that pages the graph is the walk it exists to replace.
func (s *Service) tierPointers(ctx context.Context, teamID, wing string, policy WingPolicy) ([]BootstrapPointer, int, error) {
	root := WingRootSubject(wing)
	prefix := root + "."
	rootQ, err := s.KGQuery(ctx, teamID, KGQueryInput{Entity: root, Direction: "outgoing", Status: KGStatusCurrent})
	if err != nil {
		return nil, 0, err
	}
	if rootQ.Resolution == KGResolutionUnknownTerm {
		return nil, 0, nil // no by-name root: nothing to walk, and not an error
	}
	var out []BootstrapPointer
	omitted := 0
	for _, tier := range []BootstrapTier{TierMust, TierRef} {
		for _, edge := range rootQ.Facts {
			if edge.Derived || edge.Predicate != string(tier) {
				continue
			}
			type node struct {
				name  string
				depth int
			}
			queue := []node{{edge.Object, 0}}
			for len(queue) > 0 {
				cur := queue[0]
				queue = queue[1:]
				q, err := s.KGQuery(ctx, teamID, KGQueryInput{Entity: cur.name, Direction: "outgoing", Status: KGStatusCurrent})
				if err != nil {
					return nil, 0, err
				}
				if q.NextCursor != "" {
					omitted++ // the node overflowed a page; counted, not paged
				}
				for _, f := range q.Facts {
					if f.Derived {
						continue
					}
					if strings.HasPrefix(f.Object, prefix) {
						// Structural. Two levels below the tier is the protocol's
						// depth (tier → namespace → leaf); anything deeper is
						// counted as cut rather than walked forever.
						if cur.depth < 2 {
							queue = append(queue, node{f.Object, cur.depth + 1})
						} else {
							omitted++
						}
						continue
					}
					if len(out) >= bootstrapTierLimit {
						omitted++
						continue
					}
					placement, _ := policy.Place(ctx, f.Object)
					if !policy.MayReturnContent(placement) {
						omitted++
						continue
					}
					out = append(out, BootstrapPointer{
						ID: f.Object, Tier: tier, Fetch: "am_get_drawer", Hint: f.SourceFile, Under: f.Subject,
					})
				}
			}
		}
	}
	return out, omitted, nil
}
