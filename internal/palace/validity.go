package palace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// validityWindowMigrationVersion is the goose version that adds the window
// (00030_drawers_validity_window.sql). It is named here rather than typed into a
// test, because a test that migrates "to 29" is a test that silently stops
// testing the boundary the day the number moves.
//
// ⚠ ADR-038 requires the number to be ALLOCATED AT MERGE, not at authoring: a
// renumber at merge re-runs the migration on any database that already applied
// it under the old number, which is a crash loop the README documents a repair
// for. If this file is rebased past another migration, this constant and the
// filename move together.
const validityWindowMigrationVersion int64 = 30

// currentScope narrows a drawer query to records that have not been ended.
//
// The comparison is exact against the empty string — this schema's "not yet
// ended" sentinel, chosen in 00010 for kg_triples so a Go string column never
// has to scan NULL. Exactness is also why idx_drawers_current can be a partial
// index on the same predicate: it never compares two temporal values, so the
// mixed date-only and datetime formats a caller may write cannot affect which
// rows match.
func currentScope(dbq *gorm.DB) *gorm.DB { return dbq.Where("valid_to = ''") }

// CurrentDrawers returns a team's drawers that are still current, optionally
// narrowed to one wing.
//
// The recall path does NOT go through here — T5 composes currentScope into
// Repo.ListCurrent and the ended branch of survivorsFrom instead, because each of
// those needs the predicate inside its own query rather than a second whole-wing
// read. This stays as the wing-wide enumeration the corpus checks use.
func (r *Repo) CurrentDrawers(ctx context.Context, teamID, wing string) ([]Drawer, error) {
	q := r.db.WithContext(ctx).Model(&drawerRow{}).Where("team_id = ?", teamID)
	if strings.TrimSpace(wing) != "" {
		q = q.Where("wing = ?", wing)
	}
	var rows []drawerRow
	if err := currentScope(q).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Drawer, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromRow(row))
	}
	return out, nil
}

// EndDrawer marks one drawer as no longer current, with the reason it stopped
// applying. It is the SINGLE place a row becomes historical: T4 adds callers
// (the supersede path and am_invalidate_drawer) and never a second ending path,
// because two of them diverge on the case nobody tested.
//
// Ending is not deleting. The row, its content, its vector and its anchors all
// survive; only its membership in "current" changes.
//
// A reason is REQUIRED and an already-ended drawer is REFUSED. The first is
// because an ending with no why leaves a later session exactly where finding
// nothing would — re-deriving, and re-litigating a decision already taken. The
// second is because the FIRST ending is the true one: silently re-ending would
// overwrite the reason that explained the change with the reason for a mistake.
//
// ⚠ NAMED EndDrawer, NOT End, and the suffix is load-bearing. ADR-038 T1 wrote
// it as End(id, reason); a bare End in this package poisons
// TestMutatingCallListIsComplete, whose analysis is deliberately name-keyed and
// receiver-blind ("small enough to read in one glance"). Every traced function
// here calls span.End() in its telemetry defer — 15 call sites across three
// files — so a mutating Service.End makes the fixed point classify Get,
// GetMemory, KGQuery, Traverse, Bootstrap, EntryPoint and FollowTunnels as
// mutating. Proven two-sided: the gate is green without this method and red with
// it named End. The gate is right and the name was wrong.
func (s *Service) EndDrawer(ctx context.Context, teamID, id, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("%w: a reason is required to end a memory — an ending with no why "+
			"records that something stopped applying and destroys the only thing worth keeping about it", ErrInvalidInput)
	}
	// GetAnyVersion, not Get: the refusal below is ABOUT an ended record, and the
	// current-only route answers "not found" for one — turning a precise "already
	// ended on X, and here is the reason" into a bare miss for a row that exists.
	current, err := s.GetAnyVersion(ctx, teamID, id) // also maps an unknown id to ErrNotFound
	if err != nil {
		return err
	}
	if current.ValidTo != "" {
		return fmt.Errorf("%w: drawer %s was already ended on %s (%q). The first ending is the one that "+
			"is true; re-ending would overwrite the reason that explained the change",
			ErrInvalidInput, short12(id), current.ValidTo, current.EndedReason)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// Both writes in ONE transaction: a retraction that ended the row and then
	// failed to end its derived edges would leave the front door pointing at a
	// record the same call had just withdrawn.
	return s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Model(&drawerRow{}).
			Where("team_id = ? AND id = ?", teamID, id).
			Updates(map[string]any{"valid_to": now, "ended_at": now, "ended_reason": reason}).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// The server's derived edges go with it; an authored pointer survives,
		// because retracting a record does not retract someone's reference to it.
		return endDerivedEdgesFor(tx, teamID, []string{id}, now,
			"the drawer this derived edge points at was retracted")
	})
}
