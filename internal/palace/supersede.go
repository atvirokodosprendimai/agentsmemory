package palace

import (
	"context"
	"fmt"
	"strings"
	"time"

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

	// The successor is written FIRST, so a failure leaves the old memory current
	// rather than leaving the team with nothing. An ended record with no
	// replacement is the worse half-state: recall goes quiet on a subject the
	// palace still knows about.
	added, err := s.Add(ctx, teamID, AddInput{
		Wing:        wing,
		Room:        room,
		SourceFile:  head.SourceFile,
		Content:     content,
		ContentDate: head.ContentDate,
	})
	if err != nil {
		return SupersedeResult{}, fmt.Errorf("file the correcting record: %w", err)
	}
	if len(added.Drawers) == 0 {
		return SupersedeResult{}, fmt.Errorf("the correcting record produced no rows")
	}
	newID := added.Drawers[0].ID

	if err := s.carryAnchors(ctx, teamID, head.ID, newID); err != nil {
		return SupersedeResult{}, fmt.Errorf("carry anchors to the correcting record: %w", err)
	}

	endedAt := time.Now().UTC().Format(time.RFC3339)
	for _, c := range chunks {
		if c.ValidTo != "" {
			continue
		}
		err := s.repo.db.WithContext(ctx).Model(&drawerRow{}).
			Where("team_id = ? AND id = ?", teamID, c.ID).
			Updates(map[string]any{
				"valid_to":      endedAt,
				"ended_at":      endedAt,
				"ended_reason":  reason,
				"superseded_by": newID,
			}).Error
		if err != nil {
			return SupersedeResult{}, fmt.Errorf("end chunk %s of the superseded memory: %w", short12(c.ID), err)
		}
	}
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
		txRepo := &Repo{db: tx}

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
