package palace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// contentKeyMigrationVersion is the goose version that adds the column and its
// partial unique index (00031_drawers_content_key.sql). Named here for the same
// reason validityWindowMigrationVersion is: a test that hardcodes a number stops
// testing the boundary the day the number moves.
const contentKeyMigrationVersion int64 = 31

// ErrContentKeyCollision reports two CURRENT rows in one team whose fields hash
// to the same content key. It is a distinct sentinel because the caller has to
// tell it apart from an ordinary write failure: a collision is a corpus fact
// somebody must look at, not a transient error to retry.
var ErrContentKeyCollision = errors.New("content key collision")

// opaqueDrawerID mints a NEW drawer's name. It is random, never derived, and
// never compared to anything — its whole job is to be a stable handle that
// anchors, tunnels, kg_triples.source_drawer_id and parent_id can point at while
// the row's content changes underneath it.
//
// ⚠ 32 random bytes, so it is the SAME SHAPE as the old content hash — 64 lowercase
// hex — and that is a deliberate reversal of the ADR's own preference. ADR-038
// says an id indistinguishable from a hash "invites the next reader to re-derive
// it", which argued for a visibly different shape. But repohygiene's privacy gate
// finds palace identifiers in tracked fixtures by matching \b[0-9a-f]{64}\b
// (adr036_fixtures_test.go:17), so a shorter or prefixed id would slip past the
// check that stops real drawer ids being committed. A readability preference lost
// to a privacy gate; the doc comments carry the "never re-derive this" rule
// instead, and T6's gate enforces it.
func opaqueDrawerID() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Falling back to the content hash would reintroduce exactly the coupling
		// this decision removes, so the failure is returned as an unusable id the
		// caller's insert will reject rather than as a silently derived one.
		return ""
	}
	return hex.EncodeToString(b[:])
}

// contentKeyFor computes the key for a row as it currently stands. Diary rows
// get an empty key — a journal is append-only, so two identical reflections are
// two entries, and the index's `content_key != ”` conjunct is what keeps them
// out of dedup.
func contentKeyFor(d Drawer) string {
	return contentKeyOf(d.TeamID, d.Wing, d.Room, d.SourceFile, d.ChunkIndex, d.Content)
}

// contentKeyOf is contentKeyFor for a caller that has the fields but not yet a
// Drawer — which is every mint path, because the key is needed to decide the id
// the Drawer will carry.
//
// ⚠ THE DIARY BRANCH LIVES HERE AND NOWHERE ELSE, and that is the whole point of
// this function existing. It was written as one branch inside contentKeyFor, and
// four of the five mint paths — Add, Mine, AbsorbDrawers and CopyWing — called
// DrawerID directly and never saw it. Reported and reproduced on #76: two
// identical journal entries went into AbsorbDrawers, ONE row came out, and the
// call reported 2. A journal is append-only and two identical reflections are two
// entries; deduping them is silent data loss.
//
// The T2 test that was supposed to prevent exactly this drove WriteDiary, the one
// path that was already right — the repository's signature defect in its
// documented form: the test exercised the component rather than the selection.
// TestEveryMintPathHonoursTheDiaryExemption is the replacement, and it is written
// against the paths rather than the function.
func contentKeyOf(teamID, wing, room, sourceFile string, chunkIndex int, content string) string {
	if room == DiaryRoom {
		return ""
	}
	return DrawerID(teamID, wing, room, sourceFile, chunkIndex, content)
}

// namedCollision turns a bare UNIQUE-constraint violation into an error that says
// WHICH drawer already holds the content.
//
// This exists because the bare form is unactionable. "UNIQUE constraint failed:
// drawers.team_id, drawers.content_key" tells an operator that something
// collided and nothing about what — and the two ways to get here are a merge into
// a wing that already holds the same memory, and an in-place edit that makes one
// row's content identical to another's. Both are things a human resolves by
// looking at the two rows.
func (r *Repo) namedCollision(ctx context.Context, teamID, key, movingID string, cause error) error {
	if cause == nil || !isUniqueViolation(cause) {
		return cause
	}
	var other []string
	_ = r.db.WithContext(ctx).Model(&drawerRow{}).
		Where("team_id = ? AND content_key = ? AND valid_to = '' AND id <> ?", teamID, key, movingID).
		Limit(2).Pluck("id", &other).Error
	return fmt.Errorf("%w: drawer %s would share content with %s, which is already current in this team. "+
		"Nothing was changed — resolve the duplicate first (end one of them, or edit its content)",
		ErrContentKeyCollision, short12(movingID), strings.Join(shortAll(other), ", "))
}

// isUniqueViolation recognises the driver's constraint error by message rather
// than by type: glebarez/sqlite wraps the underlying error and the concrete type
// is not part of its API, so matching on it would break on a driver bump without
// any test noticing.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED")
}

// shortAll abbreviates a list of ids for an error message.
func shortAll(ids []string) []string {
	if len(ids) == 0 {
		return []string{"another row"}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, short12(id))
	}
	return out
}

// RecomputeContentKeys refreshes the key on rows whose hashed fields just moved
// — today that is MergeWing, which relabels the wing and must carry the key with
// it. A wing move is the path easiest to forget, because the row's content never
// changes and only one of the six hashed fields does.
//
// A collision here is REFUSED and named. Under the old model a merge into a wing
// already holding the same memory silently produced two rows with different ids;
// now the index catches it. That converts a silent duplicate into a loud refusal,
// which is the better direction — and ADR-015 already fails the whole merge on
// any failure rather than leaving it half-done.
func (r *Repo) RecomputeContentKeys(ctx context.Context, teamID string, ids []string) error {
	for _, batch := range chunkIDs(ids) {
		var rows []drawerRow
		if err := r.db.WithContext(ctx).Where("team_id = ? AND id IN ?", teamID, batch).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			key := contentKeyFor(fromRow(row))
			if key == row.ContentKey {
				continue
			}
			err := r.db.WithContext(ctx).Model(&drawerRow{}).
				Where("team_id = ? AND id = ?", teamID, row.ID).
				Update("content_key", key).Error
			if err != nil {
				return r.namedCollision(ctx, teamID, key, row.ID, err)
			}
		}
	}
	return nil
}

// BackfillContentKeys stamps the key on every row that has none.
//
// ⚠ IT IS GATED ON WORK REMAINING, NOT ON THE GOOSE VERSION, and that is the
// whole design. goose records a migration's version the first time its SQL runs
// and never runs it again, so a backfill expressed as "runs once" would never
// resume if it aborted halfway — the corpus would sit permanently half-keyed with
// nothing reporting it. Gating on rows-still-empty means an aborted run is
// retried on the next boot and a completed one costs one cheap COUNT.
//
// It ABORTS on the first collision rather than skipping the row. A silent partial
// backfill is the failure shape this repository keeps catching: a failed
// migration is recoverable and visible, a half-done one is neither.
//
// SQLite cannot compute SHA-256, which is why this is a Go pass rather than an
// UPDATE inside the migration.
func (r *Repo) BackfillContentKeys(ctx context.Context) error {
	const batch = 500
	for {
		var rows []drawerRow
		err := r.db.WithContext(ctx).
			Where("content_key = '' AND room <> ?", DiaryRoom).
			Limit(batch).Find(&rows).Error
		if err != nil {
			return fmt.Errorf("read rows awaiting a content key: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		// A batch that stamps nothing would be re-selected forever, because the
		// loop's exit condition is "no rows left to key" and a row that cannot be
		// keyed never leaves the set. Found by a mutant on 2026-08-27: removing
		// contentKeyFor's diary exemption made this spin instead of fail, and a
		// hang and a pass are indistinguishable from a timed-out gate. Progress is
		// therefore checked rather than assumed.
		stamped := 0
		for _, row := range rows {
			key := contentKeyFor(fromRow(row))
			if key == "" {
				continue
			}
			err := r.db.WithContext(ctx).Model(&drawerRow{}).
				Where("team_id = ? AND id = ?", row.TeamID, row.ID).
				Update("content_key", key).Error
			if err != nil {
				if isUniqueViolation(err) {
					continue
				}
				return fmt.Errorf("stamp content key on %s: %w", short12(row.ID), err)
			}
			stamped++
		}
		if stamped == 0 {
			return fmt.Errorf("%d row(s) still have no content key and none could be stamped — "+
				"the backfill would loop forever rather than finish. First is %s in %s/%s",
				len(rows), short12(rows[0].ID), rows[0].Wing, rows[0].Room)
		}
	}
}

// chunkIDs splits an id list into batches bounded like every other id list in
// this package, so a merge of a large wing does not build one enormous IN clause.
func chunkIDs(ids []string) [][]string {
	const max = 500
	var out [][]string
	for len(ids) > max {
		out = append(out, ids[:max])
		ids = ids[max:]
	}
	if len(ids) > 0 {
		out = append(out, ids)
	}
	return out
}

var _ = gorm.ErrRecordNotFound

// CurrentBySource returns the CURRENT rows filed from one source within a
// (team, wing, room). It is what a re-file diffs against: the rows whose content
// key is absent from the new set are the ones the source dropped.
//
// Scoped to current rows on purpose. An already-ended row is history; a re-file
// neither revives it nor ends it twice, and including it would make the second
// ending overwrite the first one's reason.
func (r *Repo) CurrentBySource(ctx context.Context, teamID, wing, room, source string) ([]Drawer, error) {
	var rows []drawerRow
	err := r.db.WithContext(ctx).
		Where("team_id = ? AND wing = ? AND room = ? AND source_file = ? AND valid_to = ''",
			teamID, wing, room, source).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]Drawer, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromRow(row))
	}
	return out, nil
}

// IDsByContentKeys maps content keys to the id of the CURRENT row already
// holding each, for the keys that exist.
//
// It is what keeps a re-file from renaming a memory. A mint path computes its
// keys first and reuses the id of any row that already holds one, so the upsert
// updates that row in place rather than inserting beside it — and, just as
// importantly, so the ids the caller is TOLD about are the ids the database ends
// up with. Minting blindly and letting the conflict clause sort it out leaves the
// row correct and the response wrong, which is worse: an agent that anchors to a
// returned id would pin to a row that does not exist.
func (r *Repo) IDsByContentKeys(ctx context.Context, teamID string, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	// An empty key is the diary exemption, not a key. Left in, `content_key IN
	// ('')` matches every exempt row in the team and the lookup answers with an
	// unrelated id — harmless today only because mintOrReuse guards on the same
	// emptiness, which is one guard too few to rely on.
	lookup := make([]string, 0, len(keys))
	for _, k := range keys {
		if k != "" {
			lookup = append(lookup, k)
		}
	}
	if len(lookup) == 0 {
		return out, nil
	}
	for _, batch := range chunkIDs(lookup) {
		var rows []drawerRow
		err := r.db.WithContext(ctx).
			Select("id", "content_key").
			Where("team_id = ? AND valid_to = '' AND content_key IN ?", teamID, batch).
			Find(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[row.ContentKey] = row.ID
		}
	}
	return out, nil
}

// mintOrReuse returns the id a drawer with this content key must carry: the one
// the current row already has, or a fresh opaque name.
func mintOrReuse(existing map[string]string, key string) string {
	if key != "" {
		if id, ok := existing[key]; ok && id != "" {
			return id
		}
	}
	return opaqueDrawerID()
}
