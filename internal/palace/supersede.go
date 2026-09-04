package palace

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
)

// SupersedeResult names the record a correction created and the one it replaced.
// Both, because the caller needs the new id to keep working with the memory and
// the old one to know what it just ended — an agent told only "ok" learns
// neither.
type SupersedeResult struct {
	ID         string `json:"id"`
	Supersedes string `json:"supersedes"`
	Reason     string `json:"reason"`
	EndedAt    string `json:"ended_at"`
}

// Supersede corrects a memory by writing a NEW record and ending the old one
// with the reason it stopped applying.
//
// This replaces the in-place content edit. The old behaviour destroyed the
// rejected alternative — the one thing irrecoverable at any price, because a
// rejected alternative leaves no trace in the artifact — and reported success
// while doing it.
//
// THE WHOLE MEMORY IS REPLACED, not one chunk. `Service.Update` used to refuse a
// multi-chunk content edit and tell the caller to "delete the memory and file it
// again as one piece"; a supersede is that instruction, performed correctly and
// without the delete. Every chunk of the old memory ends, and the new content is
// filed at the same wing/room/source, chunked by the path that handles arbitrary
// length.
//
// The anchors CARRY to the successor with status reset to unchecked. Verification
// is client-side — list_anchors hands them out, the client checks its working
// tree, mark_anchors takes verdicts back — so the server cannot re-check here,
// and an anchor that has not been looked at must never read as verified. Anchors
// are scarce (41 of 2,029 drawers carry one, measured 2026-08-27); clearing them
// on every correction would spend what the palace barely has.
func (s *Service) Supersede(ctx context.Context, teamID, id, content, reason string) (SupersedeResult, error) {
	chunks, err := s.repo.MemoryChunks(ctx, teamID, id)
	if err != nil {
		return SupersedeResult{}, fmt.Errorf("look up the memory this drawer belongs to: %w", err)
	}
	if len(chunks) == 0 {
		return SupersedeResult{}, ErrNotFound
	}
	return s.supersedeInto(ctx, teamID, id, content, reason, chunks[0].Wing, chunks[0].Room)
}

// supersedeInto is Supersede with the successor's destination chosen by the
// caller, so one call can correct a memory and relocate it at the same time —
// update_drawer accepts content and wing/room together, and filing the correction
// at the old address would silently drop half of what was asked for.
func (s *Service) supersedeInto(ctx context.Context, teamID, id, content, reason, wing, room string) (SupersedeResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return SupersedeResult{}, fmt.Errorf("%w: a reason is required to correct a memory — a "+
			"correction that records only THAT something changed destroys the one thing worth keeping "+
			"about the change", ErrInvalidInput)
	}
	if strings.TrimSpace(content) == "" {
		return SupersedeResult{}, fmt.Errorf("%w: content cannot be empty; to retract a memory that "+
			"nothing replaces, use invalidate", ErrInvalidInput)
	}

	chunks, err := s.repo.MemoryChunks(ctx, teamID, id)
	if err != nil {
		return SupersedeResult{}, fmt.Errorf("look up the memory this drawer belongs to: %w", err)
	}
	if len(chunks) == 0 {
		return SupersedeResult{}, ErrNotFound
	}
	head := chunks[0]
	if head.ValidTo != "" {
		return SupersedeResult{}, fmt.Errorf("%w: drawer %s was already ended on %s (%q); correct the "+
			"record that replaced it, not the one it replaced", ErrInvalidInput, short12(id), head.ValidTo, head.EndedReason)
	}

	// ⚠ THE ORDER HERE WAS DELIBERATE AND IS NOW REJECTED. This path used to write
	// the successor first, "so a failure leaves the old memory current rather than
	// leaving the team with nothing" — an ended record with no replacement being
	// the worse half-state, because recall goes quiet on a subject the palace still
	// knows about.
	//
	// ADR-044 §Decision rejects that trade explicitly. The state it protects is
	// predecessor-current AND successor-current, which is precisely the
	// two-competing-records state that produced four framings of one finding on one
	// page. The half of the old rationale that SURVIVES is its premise: an ending
	// with no successor is genuinely bad. What changed is that we no longer have to
	// choose — both halves commit together or neither does, so the failure leaves
	// the predecessor current and nothing else, which is the pre-correction state
	// rather than a fork.
	//
	// Embedding happens HERE, before the transaction opens, because it is a network
	// call: KGSupersede records what holding one inside SQLite's single write
	// transaction costs — "a slow embedder becomes a locked database".
	prepared, err := s.prepareWrite(ctx, teamID, AddInput{
		Wing:        wing,
		Room:        room,
		SourceFile:  head.SourceFile,
		Content:     content,
		ContentDate: head.ContentDate,
	})
	if err != nil {
		return SupersedeResult{}, fmt.Errorf("file the correcting record: %w", err)
	}
	if len(prepared.drawers) == 0 {
		return SupersedeResult{}, fmt.Errorf("the correcting record produced no rows")
	}

	// ⚠ A CORRECTION MINTS. It may never reuse an id belonging to the record it is
	// ending, and this is ADR-038's contract rather than an implementation detail:
	// the id CHANGES, and the old text stays readable by its own id, because the
	// version that was replaced is the thing nothing else can recover.
	//
	// prepareWrite reuses the id of any CURRENT row already holding a chunk's
	// content key — deliberately, so re-filing unchanged text keeps every anchor and
	// pointer pinned to it. But it resolves that OUTSIDE this transaction, and when
	// a correction leaves chunk 0 byte-identical (fixing the conclusion of a long
	// note and leaving the opening alone — the commonest correction there is) the
	// lookup hands back the PREDECESSOR's own id. The swap below then ends that row
	// and the insert collides with it:
	//
	//	save drawers: constraint failed: UNIQUE constraint failed: drawers.team_id, drawers.id
	//
	// Shipped 2026-08-29 in T7 and fixed here. The pre-T7 order was not correct
	// either: writing the successor first made that reused id UPSERT the
	// predecessor's still-current chunk 0, overwriting the row ADR-038 exists to
	// preserve and only then ending it — silent destruction where this was a loud
	// failure. Neither is right; minting is.
	predecessorIDs := make(map[string]bool, len(chunks))
	for _, c := range chunks {
		predecessorIDs[c.ID] = true
	}
	remint := map[string]string{} // old id -> freshly minted id
	for i := range prepared.drawers {
		if predecessorIDs[prepared.drawers[i].ID] {
			fresh := opaqueDrawerID()
			remint[prepared.drawers[i].ID] = fresh
			prepared.drawers[i].ID = fresh
		}
	}
	// Parentage is assigned from chunk 0's id, so a reminted root has to be
	// followed through its children or they point at the record being ended.
	for i := range prepared.drawers {
		if fresh, ok := remint[prepared.drawers[i].ParentID]; ok {
			prepared.drawers[i].ParentID = fresh
		}
	}
	newID := prepared.drawers[0].ID

	// Vectors before the transaction, for the reason persistRows documents: the
	// vector store shares the service's database handle, so writing through it
	// inside the transaction is a second connection to a locked file. A vector
	// whose row never lands is harmless — search skips ids it cannot resolve.
	if prepared.vectors != nil {
		if err := s.upsertDrawerVectors(ctx, teamID, prepared.drawers, prepared.vectors); err != nil {
			return SupersedeResult{}, fmt.Errorf("file the correcting record: %w", err)
		}
	}

	// The chunks observed OPEN, which is what the compare-and-swap is against.
	// Counting these rather than len(chunks) is the difference between "somebody
	// raced me" and "one of these was already ended before I looked", which the
	// loop below used to silently skip.
	open := make([]string, 0, len(chunks))
	for _, c := range chunks {
		if c.ValidTo == "" {
			open = append(open, c.ID)
		}
	}
	if len(open) == 0 {
		return SupersedeResult{}, fmt.Errorf("%w: every chunk of drawer %s is already ended",
			ErrInvalidInput, short12(id))
	}

	endedAt := time.Now().UTC().Format(time.RFC3339)
	err = s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// ENDINGS FIRST, INSIDE THE TRANSACTION, and this order matters for a
		// reason that is not obvious: persistRows re-files under the predecessor's
		// SOURCE, and a re-file ends every current row of that source whose content
		// key left it — which is every chunk of the predecessor. Running it before
		// the swap would end them with the generic re-file reason, and the swap
		// would then find nothing current and report a race that never happened.
		res := tx.Model(&drawerRow{}).
			Where("team_id = ? AND id IN ? AND valid_to = ''", teamID, open).
			Updates(map[string]any{
				"valid_to":      endedAt,
				"ended_at":      endedAt,
				"ended_reason":  reason,
				"superseded_by": newID,
			})
		if res.Error != nil {
			return fmt.Errorf("end the superseded memory: %w", res.Error)
		}
		// ⚠ AND END THE DERIVED EDGES THAT POINT AT THOSE ROWS, in this same
		// transaction, because the author cannot do it and nothing else will. A
		// derived edge is minted by the server from the room; no call lets an
		// author end one, so advice about repointing your edges has no action
		// behind it. See endDerivedEdgesFor for the full reasoning and the other
		// three doors that end rows.
		if err := endDerivedEdgesFor(tx, teamID, open, endedAt,
			"the drawer this derived edge points at was superseded"); err != nil {
			return fmt.Errorf("end the superseded memory's derived edges: %w", err)
		}
		// THE COMPARE-AND-SWAP. RowsAffected is the answer, not a diagnostic — the
		// same discard that made am_kg_invalidate report success for a fact it never
		// touched (#73). A short count means someone ended at least one of these
		// chunks between our read and this write, so a second correction is already
		// in flight; applying ours too is how one subject ends up with two current
		// successors.
		if res.RowsAffected != int64(len(open)) {
			return fmt.Errorf("%w: %d of %d chunks of drawer %s were still current when this "+
				"correction was written; another correction of the same record is in flight. "+
				"Nothing was changed — re-read the memory and correct the record that replaced it",
				ErrConcurrentCorrection, res.RowsAffected, len(open), short12(id))
		}
		return s.persistRows(ctx, repoOn(tx), teamID, prepared)
	})
	if err != nil {
		return SupersedeResult{}, err
	}

	// AFTER the commit, both of them, and both FAIL OPEN — because by this point
	// the correction is durable and returning an error would report a write that
	// succeeded as one that failed.
	//
	// The first draft called both "repairable follow-ups" and then made one of them
	// fatal, which is the comment describing the code the author meant to write. A
	// caller that read that error and did the obvious thing — retry — landed on the
	// already-ended refusal, recovering from something that had already worked.
	//
	// This is the choice am_get_drawer's MemorySize lookup already makes ("fails
	// OPEN and says so in the trace"), and it applies with more force here: there
	// the read was still in flight, here the write is committed. Anchors are scarce
	// enough to be worth a loud trace and not worth a false failure.
	if err := s.carryAnchors(ctx, teamID, head.ID, newID); err != nil {
		telemetry.Annotate(ctx, attribute.Bool("am.anchors_not_carried", true))
		slog.Warn("correction committed but its anchors were not carried forward",
			"error", err, "superseded", short12(head.ID), "successor", short12(newID))
	}
	s.attachDerivedEdgeTo(ctx, teamID, prepared.drawers)

	return SupersedeResult{ID: newID, Supersedes: head.ID, Reason: reason, EndedAt: endedAt}, nil
}

// InvalidateDrawer retracts a memory that nothing replaces.
//
// It exists beside Supersede because plenty of retractions replace nothing: "we
// are not doing this after all" has no successor record, and forcing one would
// make an agent invent a placeholder memory to express an absence.
//
// The whole memory goes, every chunk, for the reason Delete already gave: a
// memory is the unit, and ending one chunk leaves the others current and still
// answering with the claim that was just retracted.
func (s *Service) InvalidateDrawer(ctx context.Context, teamID, id, reason string) error {
	chunks, err := s.repo.MemoryChunks(ctx, teamID, id)
	if err != nil {
		return fmt.Errorf("look up the memory this drawer belongs to: %w", err)
	}
	if len(chunks) == 0 {
		return ErrNotFound
	}
	for _, c := range chunks {
		if c.ValidTo != "" {
			continue
		}
		if err := s.EndDrawer(ctx, teamID, c.ID, reason); err != nil {
			return err
		}
	}
	return nil
}

// carryAnchors copies a superseded record's anchors onto its successor with
// status reset to unchecked.
//
// anchorID folds the drawer id, so the copies mint new ids for free and cannot
// collide with the originals. The predecessor KEEPS its own: it keeps its text,
// so its pin is still true of it.
//
// ⚠ NAMED COST: Stale() is true only for drifted/missing, so a carried anchor
// does not cry wolf before anyone looks at it — and there is therefore a window
// where it reads as fine and has not been checked. That is the right trade, a
// false stale marker being worse than an unchecked one, but it is a window rather
// than nothing.
func (s *Service) carryAnchors(ctx context.Context, teamID, fromID, toID string) error {
	existing, err := s.AnchorsForDrawers(ctx, teamID, []string{fromID})
	if err != nil {
		return err
	}
	src := existing[fromID]
	if len(src) == 0 {
		return nil
	}
	in := make([]AnchorInput, 0, len(src))
	for _, a := range src {
		in = append(in, AnchorInput{Repo: a.Repo, Path: a.Path, Snippet: a.Snippet})
	}
	_, err = s.AddAnchors(ctx, teamID, toID, in)
	return err
}

// KGSupersede replaces a fact atomically: it ends the old one and adds the new
// one in a SINGLE transaction, so no caller can observe the graph holding both or
// neither.
//
// Upstream's protocol names the defect this removes: "do NOT hand-roll invalidate
// + add, which leaves the old and new values overlapping at the boundary."
// agentsmemory had no such verb, and kg.go's frozen no-auto-supersede rule
// MANDATED the hand-rolled path. There was also no Transaction( anywhere in
// kg.go, so a session dying between the two calls left zero current values or two,
// permanently.
//
// ⚠ IT STAMPS AN INSTANT, NEVER A DATE, and that is what removes the day-scale
// overlap without changing what any existing row means. temporalEndKey stretches a
// date-only valid_to to T23:59:59Z, so a hand-rolled same-day replacement leaves
// BOTH values in effect for 86,400 seconds — reproduced in issue #74. Both
// endpoints get the same RFC3339 datetime here, so no stretch applies.
//
// What it does NOT fix: the boundary instant itself. inEffectAt is inclusive on
// both ends, so a shared endpoint is in effect for both rows — 86,400 seconds
// becomes 1. That is the closed-versus-half-open interval question, which belongs
// to issue #74 because answering it re-reads every already-ended fact.
func (s *Service) KGSupersede(ctx context.Context, teamID, subject, predicate, oldObject, newObject, reason string) (boundary string, err error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", fmt.Errorf("%w: a reason is required to replace a fact — an invalidation that "+
			"records only THAT a fact ended keeps the cheapest half and drops the expensive one", ErrInvalidInput)
	}
	subj, err := sanitizeKGValue(subject, "subject")
	if err != nil {
		return "", err
	}
	pred, err := SanitizeName(predicate, "predicate")
	if err != nil {
		return "", err
	}
	oldObj, err := sanitizeKGValue(oldObject, "old object")
	if err != nil {
		return "", err
	}
	newObj, err := sanitizeKGValue(newObject, "new object")
	if err != nil {
		return "", err
	}

	// An INSTANT, shared by both endpoints. Never a date.
	boundary = time.Now().UTC().Format(time.RFC3339)
	subID, oldID, p := normalizeEntityID(subj), normalizeEntityID(oldObj), normalizePredicate(pred)

	err = s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A transaction-bound view of the repo. Every KG write goes through r.db,
		// so handing the shared add path this copy is what puts the end and the
		// add under one commit without a second, drifting copy of the sequence.
		txRepo := repoOn(tx)

		res := tx.Model(&kgTripleRow{}).
			Where("team_id = ? AND subject = ? AND predicate = ? AND object = ? AND valid_to = ''",
				teamID, subID, p, oldID).
			Updates(map[string]any{"valid_to": boundary, "ended_reason": reason})
		if res.Error != nil {
			return res.Error
		}
		// RowsAffected is the answer, not a diagnostic — the same discard that made
		// am_kg_invalidate report success for a fact it never touched (#73).
		if res.RowsAffected == 0 {
			return fmt.Errorf("%w: no CURRENT fact matches %s → %s → %s, so there is nothing to "+
				"replace. Either it was never filed, or it is already ended. Nothing was changed",
				ErrFactNotFound, subID, p, oldID)
		}
		if _, err := kgAddOn(ctx, txRepo, teamID, subj, pred, newObj, boundary, "", "", "", ""); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	// Indexed AFTER the commit, deliberately. This calls the embedder, and holding
	// SQLite's single write transaction open across a network call is how a slow
	// embedder becomes a locked database. It is already non-fatal on the add path:
	// the fact is stored either way, and the label index is only how it is found.
	s.indexFactLabels(ctx, teamID, subj, newObj)
	return boundary, nil
}
