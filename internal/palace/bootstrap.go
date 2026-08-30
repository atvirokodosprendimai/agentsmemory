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
	// Corrections are the retracts/supersedes/qualifies edges already swept
	// server-side, so a session that bootstraps perfectly does not still read
	// whatever the tier got wrong and believe it.
	Corrections map[string][]Correction `json:"corrections,omitempty"`
	// Truncation says what this response left out and how to get it.
	Truncation BootstrapTruncation `json:"truncation"`
}

// bootstrapEagerLimit bounds the inline tier. A bootstrap that grows without
// limit becomes the thing it replaced.
const bootstrapEagerLimit = 10

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
	if len(ids) > bootstrapEagerLimit {
		inline, deferred = ids[:bootstrapEagerLimit], ids[bootstrapEagerLimit:]
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
		found := make(map[string]bool, len(drawers))
		for _, d := range drawers {
			found[d.ID] = true
			// The bootstrap's inline content goes through the SAME wing rule as
			// every other response path. An entry edge can name a record in
			// another wing, and inlining it here would be the leak that a
			// subject/predicate/object check never sees.
			placement, _ := policy.Place(ctx, d.ID)
			if policy.MayReturnContent(placement) {
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
