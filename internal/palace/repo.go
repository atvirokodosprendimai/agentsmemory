package palace

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// drawerRow is the gorm view of one row in the `drawers` table (migration
// 00006). It is the persistence shape; callers work with the domain Drawer and
// the repo translates between them. The composite primary key (team_id, id) is
// what makes Save replace-by-identity, giving add_drawer its idempotency.
type drawerRow struct {
	TeamID      string `gorm:"column:team_id;primaryKey"`
	ID          string `gorm:"column:id;primaryKey"`
	Wing        string `gorm:"column:wing"`
	Room        string `gorm:"column:room"`
	SourceFile  string `gorm:"column:source_file"`
	ChunkIndex  int    `gorm:"column:chunk_index"`
	Content     string `gorm:"column:content"`
	Entities    string `gorm:"column:entities"` // semicolon-joined on disk
	ParentID    string `gorm:"column:parent_id"`
	FiledAt     string `gorm:"column:filed_at"`
	ContentDate string `gorm:"column:content_date"`
	Agent       string `gorm:"column:agent"` // diary: whose journal (lowercased); "" for normal drawers
	Topic       string `gorm:"column:topic"` // diary: free grouping tag; "" for normal drawers
	// The validity window (migration 00030). Empty ValidTo means CURRENT, which
	// is what makes the migration backfill-free: every pre-existing row is
	// already correct. '' rather than NULL matches kg_triples, so one concept
	// does not need two sentinels across two tables.
	ContentKey   string `gorm:"column:content_key"`
	ValidTo      string `gorm:"column:valid_to"`
	SupersededBy string `gorm:"column:superseded_by"`
	EndedReason  string `gorm:"column:ended_reason"`
	EndedAt      string `gorm:"column:ended_at"`
	// EmbeddedAt is RFC3339 when the vector was built, or NULL while the row is
	// awaiting background embedding (migration 00013). A pointer so "" and NULL
	// are distinct: the sync filing paths stamp it now; absorb leaves it nil.
	EmbeddedAt *string `gorm:"column:embedded_at"`
}

// TableName pins the table so gorm does not pluralise to "drawer_rows".
func (drawerRow) TableName() string { return "drawers" }

// WingStat is one row of the list_wings aggregation: a wing with how many
// drawers and distinct rooms it holds. The json tags keep the MCP wire shape
// snake_case, matching the drawer views (the struct is returned to agents as-is).
type WingStat struct {
	Wing string `gorm:"column:wing" json:"wing"`
	// Drawers counts CURRENT rows — chunks. A retracted drawer is not something
	// a session can read and am_list_drawers already excludes it, so counting it
	// here made the two surfaces disagree about the same room in the same minute.
	Drawers int `gorm:"column:drawers" json:"drawers"`
	// Memories counts what a reader would call an item: one per memory, however
	// many chunks it was stored as. am_search reports a memory-level unit and
	// this surface was the last one still speaking in rows, so a four-chunk
	// handoff outranked two short ones purely by length.
	Memories int `gorm:"column:memories" json:"memories"`
	Rooms    int `gorm:"column:rooms" json:"rooms"`
}

// RoomStat is one row of the list_rooms aggregation: a room (within its wing)
// and its drawer count.
type RoomStat struct {
	Wing string `gorm:"column:wing" json:"wing"`
	Room string `gorm:"column:room" json:"room"`
	// Drawers counts CURRENT rows (chunks); Memories counts items. See WingStat.
	Drawers  int `gorm:"column:drawers" json:"drawers"`
	Memories int `gorm:"column:memories" json:"memories"`
}

// Repo is the gorm-backed persistence for drawer metadata. It owns only the
// `drawers` table; the embeddings live behind the store seam, joined by id. gorm
// is the query layer — goose owns the schema, so AutoMigrate is never called.
type Repo struct {
	// db is the WRITER: the single-connection handle every write, and every
	// Transaction, goes through. reader is the read model's handle — query_only
	// at the driver, pooled wide — and every method that only reads uses it.
	//
	// ADR-052 T5: a method that reads AND writes belongs wholly on db, because a
	// read on the reader followed by a write on db is two connections and no
	// longer one transaction. Readers that run inside a Transaction closure use
	// the tx it was given, never this field. A caller may pass one handle as
	// both — a test is allowed to be less strict than production — but the
	// package's own fixture passes a query_only twin, so any read that writes
	// fails there rather than in a served palace.
	db     *gorm.DB
	reader *gorm.DB
}

// NewRepo wraps a reader and a writer over one migrated database. The
// signature is the interface: nothing constructs a Repo without deciding which
// handle is which, which is what makes the split a property of the code rather
// than a convention (ADR-052 T5).
func NewRepo(reader, writer *gorm.DB) *Repo { return &Repo{db: writer, reader: reader} }

// repoOn wraps a transaction as a Repo whose reader IS the transaction.
//
// ADR-052 T5: a read that runs inside a Transaction closure must see the
// closure's own uncommitted writes and must hold the writer's connection —
// routing it onto the pooled reader would be two connections and no
// transaction, the split the record's invariant forbids. Building the tx Repo
// with only db set left reader nil, and the ADR-045 relocation's read inside
// Repo.Update was the first thing to dereference it.
func repoOn(tx *gorm.DB) *Repo { return &Repo{db: tx, reader: tx} }

// Save upserts drawers by (team_id, id). Re-saving the same id replaces the row,
// which is exactly what idempotent re-mining needs. An empty slice is a no-op.
//
// Every caller of Save is a SYNCHRONOUS filing path (add_drawer, diary_write,
// mine) that embedded the drawer before calling, so the row is stamped
// embedded_at=now — it never enters the background queue. Absorb (the migration
// import) uses SaveUnembedded instead.
func (r *Repo) Save(ctx context.Context, drawers []Drawer) error {
	if len(drawers) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rows := make([]drawerRow, 0, len(drawers))
	for _, d := range drawers {
		row := toRow(d)
		row.EmbeddedAt = &now
		rows = append(rows, row)
	}
	// NOT UpdateAll. Re-filing the exact text of an ENDED drawer mints the same id
	// (the recipe is unchanged at this task), so UpdateAll would reset valid_to,
	// ended_at and ended_reason to their zero values and silently RESURRECT a
	// retracted memory — undoing a decision somebody took, with no trace. The
	// validity columns are therefore owned by EndDrawer alone and are never
	// written by a filing path.
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			// The conflict target must repeat the PARTIAL index's predicate: a target
			// that names only the columns does not match a partial index, so SQLite
			// would reject the statement rather than upsert. Both conjuncts, exactly
			// as 00031 declares them.
			Columns:     []clause.Column{{Name: "team_id"}, {Name: "content_key"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "content_key != '' AND valid_to = ''"}}},
			// The id is NOT in this list, and that is the point: on a conflict the
			// EXISTING row keeps its name while its content is refreshed, so every
			// anchor, tunnel and provenance pointer at it survives a re-file.
			DoUpdates: clause.AssignmentColumns([]string{
				"wing", "room", "source_file", "chunk_index", "content",
				"entities", "parent_id", "filed_at", "content_date", "agent", "topic",
				"embedded_at",
			}),
		}).
		Create(&rows).Error
}

// SaveUnembedded inserts drawer rows WITHOUT a vector and WITHOUT marking them
// embedded (embedded_at stays NULL on insert) — the absorb half of a migration
// import. The background embed worker builds each vector later and stamps it.
//
// On conflict it updates the mutable columns but DELIBERATELY leaves embedded_at
// untouched, which is the crux of idempotent re-runs: a re-absorb refreshes
// metadata (entities, dates, agent/topic the source may have re-derived) yet
// preserves an already-indexed drawer's embedded_at — so an identical re-run never
// needlessly re-queues a valid vector, and never resets pending→embedded or back.
// It conflicts on the CONTENT KEY, not the id — AbsorbDrawers calls this method
// exclusively (import.go never calls Save) and Add falls to it whenever the
// embedder is down, so leaving it keyed on the id would make every import re-run
// duplicate a whole palace once ids stopped being derived from content.
func (r *Repo) SaveUnembedded(ctx context.Context, drawers []Drawer) error {
	if len(drawers) == 0 {
		return nil
	}
	rows := make([]drawerRow, 0, len(drawers))
	for _, d := range drawers {
		rows = append(rows, toRow(d)) // EmbeddedAt nil -> NULL -> pending on insert
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			// The conflict target must repeat the PARTIAL index's predicate: a target
			// that names only the columns does not match a partial index, so SQLite
			// would reject the statement rather than upsert. Both conjuncts, exactly
			// as 00031 declares them.
			Columns:     []clause.Column{{Name: "team_id"}, {Name: "content_key"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "content_key != '' AND valid_to = ''"}}},
			DoUpdates: clause.AssignmentColumns([]string{
				"wing", "room", "source_file", "chunk_index", "content",
				"entities", "parent_id", "filed_at", "content_date", "agent", "topic",
			}),
		}).
		Create(&rows).Error
}

// PendingDrawers returns up to limit drawers for a team whose vector has not been
// built yet (embedded_at IS NULL), oldest first so absorb order is preserved. The
// background worker embeds these, then calls MarkDrawersEmbedded.
func (r *Repo) PendingDrawers(ctx context.Context, teamID string, limit int) ([]Drawer, error) {
	if limit <= 0 {
		limit = 64
	}
	var rows []drawerRow
	if err := r.reader.WithContext(ctx).
		Where("team_id = ? AND embedded_at IS NULL", teamID).
		Order("filed_at ASC, id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Drawer, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromRow(row))
	}
	return out, nil
}

// MarkDrawersEmbedded stamps embedded_at on the given ids within a team, removing
// them from the pending queue. It is called only AFTER their vectors are durably
// upserted, so a crash between upsert and mark merely re-embeds (idempotently)
// next cycle rather than losing data. An empty id slice is a no-op.
func (r *Repo) MarkDrawersEmbedded(ctx context.Context, teamID string, ids []string, at string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Model(&drawerRow{}).
		Where("team_id = ? AND id IN ?", teamID, ids).
		Update("embedded_at", at).Error
}

// TeamsWithPendingDrawers lists distinct teams holding at least one un-embedded
// drawer, so the worker can round-robin tenants instead of draining one giant
// migration before touching another's. limit bounds the slice (0 = unbounded).
func (r *Repo) TeamsWithPendingDrawers(ctx context.Context, limit int) ([]string, error) {
	q := r.reader.WithContext(ctx).
		Model(&drawerRow{}).
		Distinct("team_id").
		Where("embedded_at IS NULL")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var teams []string
	if err := q.Pluck("team_id", &teams).Error; err != nil {
		return nil, err
	}
	return teams, nil
}

// PendingDrawerCount is how many of a team's drawers still await embedding — the
// "indexing N in background" signal the importer returns on finalize.
func (r *Repo) PendingDrawerCount(ctx context.Context, teamID string) (int64, error) {
	var n int64
	if err := r.reader.WithContext(ctx).
		Model(&drawerRow{}).
		Where("team_id = ? AND embedded_at IS NULL", teamID).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// Get loads a single drawer by id within a team. A missing drawer returns
// gorm.ErrRecordNotFound, which the caller translates into a tool-level error.
func (r *Repo) Get(ctx context.Context, teamID, id string) (Drawer, error) {
	var row drawerRow
	if err := r.reader.WithContext(ctx).
		Where("team_id = ? AND id = ?", teamID, id).
		First(&row).Error; err != nil {
		return Drawer{}, err
	}
	return fromRow(row), nil
}

// DrawerExists reports whether a row with this id exists in this team, INCLUDING
// one that has been ended.
//
// ⚠ HISTORY-INCLUSIVE ON PURPOSE, and that is the whole subtlety. A fact citing a
// drawer that was later corrected is the system working: provenance is a record of
// what was believed then, and a supersede does not retract the fact somebody
// derived from the row it ended. Scoping this to current rows would make every
// correction break its own citations — the opposite of the defect it exists to
// catch, which is a citation that resolved to nothing on the day it was written.
//
// It selects one id rather than the row: the caller wants existence, and a drawer
// can hold 100,000 runes of content nobody asked for.
func (r *Repo) DrawerExists(ctx context.Context, teamID, id string) (bool, error) {
	var found []string
	err := r.reader.WithContext(ctx).Model(&drawerRow{}).
		Where("team_id = ? AND id = ?", teamID, id).
		Limit(1).Pluck("id", &found).Error
	if err != nil {
		return false, err
	}
	return len(found) == 1, nil
}

// IDsBySource returns the ids of every drawer filed from one source within a
// (team, wing, room). add_drawer uses it to purge a named source's prior chunks
// before re-filing it, so re-adding shorter content cannot leave stale
// higher-index chunks behind. Order is unspecified.
func (r *Repo) IDsBySource(ctx context.Context, teamID, wing, room, source string) ([]string, error) {
	var ids []string
	if err := r.reader.WithContext(ctx).
		Model(&drawerRow{}).
		Where("team_id = ? AND wing = ? AND room = ? AND source_file = ?", teamID, wing, room, source).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// DeleteBySource removes every drawer row filed from one source within a
// (team, wing, room) in a single statement — the row half of an add_drawer purge
// (the caller drops the matching vectors via the ids from IDsBySource).
func (r *Repo) DeleteBySource(ctx context.Context, teamID, wing, room, source string) error {
	// Anchors first, while the drawers that name them are still queryable.
	var ids []string
	if err := r.db.WithContext(ctx).Model(&drawerRow{}).
		Where("team_id = ? AND wing = ? AND room = ? AND source_file = ?", teamID, wing, room, source).
		Pluck("id", &ids).Error; err != nil {
		return err
	}
	if err := r.deleteAnchors(ctx, teamID, ids); err != nil {
		return err
	}
	return r.db.WithContext(ctx).
		Where("team_id = ? AND wing = ? AND room = ? AND source_file = ?", teamID, wing, room, source).
		Delete(&drawerRow{}).Error
}

// GetMany loads drawers by id within a team, returned as an id->Drawer map so
// the caller can look survivors up in score order. Ids with no row (e.g. a
// vector whose metadata row was deleted) are simply absent from the map — search
// treats that as "skip", tolerating a transiently orphaned vector. An empty id
// slice returns an empty map.
func (r *Repo) GetMany(ctx context.Context, teamID string, ids []string) (map[string]Drawer, error) {
	out := make(map[string]Drawer, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []drawerRow
	if err := r.reader.WithContext(ctx).
		Where("team_id = ? AND id IN ?", teamID, ids).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = fromRow(row)
	}
	return out, nil
}

// MemoryChunksByRoots loads every stored chunk for the requested logical memory
// roots in one query, keyed by root id and ordered by chunk index. Missing roots
// are absent from the map.
func (r *Repo) MemoryChunksByRoots(ctx context.Context, teamID string, roots []string) (map[string][]Drawer, error) {
	out := make(map[string][]Drawer, len(roots))
	if len(roots) == 0 {
		return out, nil
	}
	var rows []drawerRow
	if err := r.memoryChunkQuery(ctx, teamID, roots, allDrawerColumns).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		d := fromRow(row)
		root := memoryOf(d)
		out[root] = append(out[root], d)
	}
	return out, nil
}

// MemoryChunkIDsByRoots is MemoryChunksByRoots reduced to identity. Anchor
// resolution needs only which chunk ids belong to which memory, and loading
// whole memories to build a list of ids moves every chunk's content across the
// wire for nothing — on a page of long memories that is the largest read in the
// request.
func (r *Repo) MemoryChunkIDsByRoots(ctx context.Context, teamID string, roots []string) (map[string][]string, error) {
	out := make(map[string][]string, len(roots))
	if len(roots) == 0 {
		return out, nil
	}
	// chunk_index is selected because a compound SELECT can only order by a
	// column it returns. It costs an int; content and entities — the columns
	// this projection exists to avoid — stay on the server.
	var rows []struct {
		ID         string
		ParentID   string
		ChunkIndex int
	}
	if err := r.memoryChunkQuery(ctx, teamID, roots, chunkIdentityColumns).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		root := row.ParentID
		if root == "" {
			root = row.ID
		}
		out[root] = append(out[root], row.ID)
	}
	return out, nil
}

// memoryChunkColumns names a projection memoryChunkQuery may select. It is a
// closed type rather than a string because the value is interpolated into
// Select() — GORM cannot parameterise a column list — so a `string` parameter is
// an SQL-injection sink one careless call site away from being reachable. Every
// call site today passes a literal and nothing external reaches it; making the
// type closed means nothing external CAN, without the compiler objecting.
type memoryChunkColumns int

const (
	// allDrawerColumns loads whole chunks, for reassembling a memory's text.
	allDrawerColumns memoryChunkColumns = iota
	// chunkIdentityColumns loads only identity. chunk_index is included because a
	// compound SELECT can only order by a column it returns; content and entities
	// — the columns this projection exists to avoid — stay on the server.
	chunkIdentityColumns
)

// sql renders the projection. An unknown value falls back to the widest
// projection, which is the safe direction to be wrong in: too much data, never
// a malformed statement.
func (c memoryChunkColumns) sql() string {
	if c == chunkIdentityColumns {
		return "id, parent_id, chunk_index"
	}
	return "*"
}

// memoryChunkQuery selects a memory's chunks as a UNION of two single-column
// lookups rather than `id IN (...) OR parent_id IN (...)`.
//
// The OR spelling is the readable one and it is why this was a full scan: no
// planner can seek both sides of a disjunction in one index pass, so it
// degrades to examining every row of the tenant however the table is indexed —
// adding the parent_id index alone leaves the plan unchanged. Split into a
// union, each branch seeks its own index (the primary key for roots,
// idx_drawers_team_parent from migration 00024 for children).
//
// UNION ALL rather than UNION, and that is load-bearing rather than a
// micro-optimisation. Deduplicating forces the compound to produce sorted
// inputs, and at least one SQLite build answers that by merging on the primary
// key — which puts the child branch back on a tenant-wide scan and quietly
// undoes the fix. The branches cannot overlap anyway: a root is stored with an
// empty parent_id, so `id IN roots` matches only roots and `parent_id IN roots`
// only their children.
// ⚠BOTH branches carry `team_id = ?`, and both are load-bearing. The child
// branch matches on parent_id, which is caller-influenced data: a row in ANOTHER
// tenant whose parent_id happens to name this tenant's root would be returned by
// an unscoped branch, and it flows straight through reassembleMemory onto the
// wire. TestMemoryChunkQueriesRefuseToCrossTenants is the gate; delete either
// predicate and watch it go red.
func (r *Repo) memoryChunkQuery(ctx context.Context, teamID string, roots []string, columns memoryChunkColumns) *gorm.DB {
	db := r.reader.WithContext(ctx)
	byID := db.Select(columns.sql()).Table("drawers").Where("team_id = ? AND id IN ?", teamID, roots)
	byParent := db.Select(columns.sql()).Table("drawers").Where("team_id = ? AND parent_id IN ?", teamID, roots)
	// Ordering belongs on the compound result: sorting inside a branch is not
	// guaranteed to survive the union. SQLite rejects parenthesised operands
	// around UNION ALL, so the branches are spliced bare and only the compound
	// is wrapped.
	return db.Table("(? UNION ALL ?) AS memory_chunks", byID, byParent).Order("chunk_index ASC")
}

// DrawerPatch carries the optional fields update_drawer may change. A nil field
// means "leave unchanged", distinguishing "set to empty" from "not provided".
type DrawerPatch struct {
	Content *string
	Wing    *string
	Room    *string
	// Reason is WHY the memory changed, and it is required whenever Content is
	// set: a content change supersedes the record, and a correction that keeps
	// only THAT something changed destroys the one thing worth keeping about the
	// change. It is unused for a wing/room move, which corrects nothing.
	//
	// Not a pointer, unlike the fields above, because there is no difference
	// between "reason omitted" and "reason set empty" — both are refused.
	Reason string
}

// Update applies a patch to an existing drawer in place, keyed by its id (the id
// is stable — it is not recomputed from the new wing/room, matching the Python
// contract where update_drawer edits a drawer without re-chunking it). It
// returns the updated drawer, or gorm.ErrRecordNotFound if the id is unknown.
func (r *Repo) Update(ctx context.Context, teamID, id string, patch DrawerPatch) (Drawer, error) {
	updates := map[string]any{}
	if patch.Content != nil {
		updates["content"] = *patch.Content
		// Entities are DERIVED from content, so they are refreshed in the same
		// statement that replaces it. Written here rather than by the caller so
		// the two columns cannot diverge: a future call site that forgets is not
		// a path this function has.
		//
		// Before this, Update replaced the content and left the previous
		// content's entities on the row, and the derived graph went on asserting
		// an edge the text no longer supported. That is worse than the missing
		// entities ADR-016 fixed on the Add and WriteDiary paths: an empty graph
		// sends an agent to go and look, a wrong one tells it not to.
		//
		// Per-chunk is per-memory here. A content change reaches this function
		// only through supersedeInto, which files the replacement through the
		// chunking path, so the row being written holds one chunk's content and
		// extracting from it matches what Add stores per chunk. (Service.Update
		// used to REFUSE a multi-chunk content edit and this comment said so;
		// ADR-038 T4 moved corrections onto supersede and ADR-045 removed the
		// refusal's remaining half, so the guard named here no longer exists.)
		updates["entities"] = strings.Join(extractEntities(*patch.Content), ";")
	}
	if patch.Wing != nil {
		updates["wing"] = *patch.Wing
	}
	if patch.Room != nil {
		updates["room"] = *patch.Room
	}
	// The content key hashes wing, room and content, so any patch touching one
	// must carry it. Computed from the POST-patch state — the row as it will be —
	// rather than from the patch alone, so a partial patch cannot produce a key
	// describing neither the old row nor the new one.
	if patch.Content != nil || patch.Wing != nil || patch.Room != nil {
		cur, err := r.Get(ctx, teamID, id)
		if err != nil {
			return Drawer{}, err
		}
		if patch.Content != nil {
			cur.Content = *patch.Content
		}
		if patch.Wing != nil {
			cur.Wing = *patch.Wing
		}
		if patch.Room != nil {
			cur.Room = *patch.Room
		}
		updates["content_key"] = contentKeyFor(cur)
	}
	if len(updates) > 0 {
		res := r.db.WithContext(ctx).
			Model(&drawerRow{}).
			Where("team_id = ? AND id = ?", teamID, id).
			Updates(updates)
		if res.Error != nil {
			// Named, not raw. A move into a wing that already holds identical text
			// is an ordinary curation action — "this wing already has that memory"
			// is precisely when someone relocates one — and the bare driver error
			// ("UNIQUE constraint failed: drawers.team_id, drawers.content_key")
			// says something collided and nothing about what. Reported on #76 after
			// T4 moved the content path out of here and left the move as the way in;
			// RecomputeContentKeys has said this properly since T2 and this was the
			// second caller that did not.
			key, _ := updates["content_key"].(string)
			return Drawer{}, r.namedCollision(ctx, teamID, key, id, res.Error)
		}
		if res.RowsAffected == 0 {
			return Drawer{}, gorm.ErrRecordNotFound
		}
	}
	return r.Get(ctx, teamID, id)
}

// Delete removes a drawer by id within a team. Deleting an absent id is a no-op
// (RowsAffected is not checked) so the caller can pair it with a vector delete
// without racing on which store dropped the point first.
func (r *Repo) Delete(ctx context.Context, teamID, id string) error {
	if err := r.db.WithContext(ctx).
		Where("team_id = ? AND id = ?", teamID, id).
		Delete(&drawerRow{}).Error; err != nil {
		return err
	}
	return r.deleteAnchors(ctx, teamID, []string{id})
}

// deleteAnchors removes the code anchors of deleted drawers. Without this an
// anchor outlives the memory it pinned, and `verify` reports drift on a sentence
// nobody can read any more — a warning about nothing, which is the fastest way to
// teach people to ignore warnings.
func (r *Repo) deleteAnchors(ctx context.Context, teamID string, drawerIDs []string) error {
	if len(drawerIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("team_id = ? AND drawer_id IN ?", teamID, drawerIDs).
		Delete(&anchorRow{}).Error
}

// List returns drawers for a team, optionally narrowed to a wing and/or room,
// newest first, INCLUDING records that have been ended. limit bounds the page (a
// non-positive limit defaults to 50 to avoid an unbounded scan); offset paginates.
//
// History-inclusive is right for its callers — a wing bundle, a cross-workspace
// copy, a sync — because each of them moves the whole record and an export that
// silently dropped what a team retracted would lose the only account of why. The
// recall path uses ListCurrent instead.
func (r *Repo) List(ctx context.Context, teamID, wing, room string, limit, offset int) ([]Drawer, error) {
	return r.list(ctx, teamID, wing, room, limit, offset, false)
}

// ListCurrent is List narrowed to records that have not been ended.
//
// The predicate is pushed into SQL rather than applied to the returned page,
// because limit/offset are applied by the database: filtering afterwards would
// return SHORT pages and, worse, a page that skips records as the offset walks
// past ended rows the caller never saw.
func (r *Repo) ListCurrent(ctx context.Context, teamID, wing, room string, limit, offset int) ([]Drawer, error) {
	return r.list(ctx, teamID, wing, room, limit, offset, true)
}

func (r *Repo) list(ctx context.Context, teamID, wing, room string, limit, offset int, currentOnly bool) ([]Drawer, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q := r.reader.WithContext(ctx).Where("team_id = ?", teamID)
	if wing != "" {
		q = q.Where("wing = ?", wing)
	}
	if room != "" {
		q = q.Where("room = ?", room)
	}
	if currentOnly {
		q = currentScope(q)
	}
	var rows []drawerRow
	// filed_at DESC, id ASC is a stable total order so paging never skips or
	// repeats a drawer even when two share an ingestion timestamp.
	if err := q.Order("filed_at DESC, id ASC").
		Limit(limit).Offset(offset).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Drawer, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromRow(row))
	}
	return out, nil
}

// DatedDrawers returns a random sample of drawers carrying a non-empty
// content_date, optionally narrowed to a wing, at most one per source file. It
// exists for the temporal eval: "the newer version of a corrected fact" is only
// well-defined for drawers whose own chronology is known, and filtering in SQL
// beats paging the whole corpus through the client to discard the undated
// majority. Random rather than newest-first for the same reason ListRandom is
// (the questions must not all be about last week), one-per-source for the same
// reason too (parts of one mined session are correlated observations);
// reproducibility comes from the saved case file, not a seed.
func (r *Repo) DatedDrawers(ctx context.Context, teamID, wing string, limit int) ([]Drawer, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.reader.WithContext(ctx).
		Where("team_id = ? AND content_date IS NOT NULL AND content_date <> ''", teamID)
	if wing != "" {
		q = q.Where("wing = ?", wing)
	}
	var rows []drawerRow
	if err := q.Order("RANDOM()").Limit(limit * 5).Find(&rows).Error; err != nil {
		return nil, err
	}
	seen := make(map[string]bool, limit)
	out := make([]Drawer, 0, limit)
	for _, row := range rows {
		if row.SourceFile != "" {
			if seen[row.SourceFile] {
				continue
			}
			seen[row.SourceFile] = true
		}
		out = append(out, fromRow(row))
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// memoryKeyExpr collapses a chunk to the memory it belongs to: a root chunk
// carries an empty parent_id and stands for itself, every other chunk names its
// root. Counting DISTINCT over it is the difference between "how many rows" and
// "how many things a reader would call items".
const memoryKeyExpr = "CASE WHEN parent_id = '' THEN id ELSE parent_id END"

// Wings aggregates a team's CURRENT drawers by wing — the list_wings backend. The
// GROUP BY rides idx_drawers_team_wing, keeping it cheap as the palace grows.
func (r *Repo) Wings(ctx context.Context, teamID string) ([]WingStat, error) {
	var stats []WingStat
	if err := r.reader.WithContext(ctx).
		Model(&drawerRow{}).
		Select("wing, COUNT(*) AS drawers, COUNT(DISTINCT "+memoryKeyExpr+") AS memories, COUNT(DISTINCT room) AS rooms").
		Where("team_id = ? AND valid_to = ''", teamID).
		Group("wing").
		Order("wing").
		Scan(&stats).Error; err != nil {
		return nil, err
	}
	return stats, nil
}

// Rooms aggregates a team's drawers by room — the list_rooms backend. An empty
// wing returns every room across the team; a non-empty wing narrows to it.
func (r *Repo) Rooms(ctx context.Context, teamID, wing string) ([]RoomStat, error) {
	q := r.reader.WithContext(ctx).
		Model(&drawerRow{}).
		Select("wing, room, COUNT(*) AS drawers, COUNT(DISTINCT "+memoryKeyExpr+") AS memories").
		Where("team_id = ? AND valid_to = ''", teamID)
	if wing != "" {
		q = q.Where("wing = ?", wing)
	}
	var stats []RoomStat
	if err := q.Group("wing, room").Order("wing, room").Scan(&stats).Error; err != nil {
		return nil, err
	}
	return stats, nil
}

// diaryScope builds the shared WHERE for an agent's diary: always (team, room
// 'diary', agent), and — only when wing is non-empty — that wing too. An empty
// wing deliberately matches every wing the agent has journaled in, because hook
// writes land in project-derived wings (wing_<project>); requiring a wing on
// read would silo those from an agent-initiated read. The (team_id, room, agent)
// index from migration 00007 is what makes this scan cheap.
func diaryScope(db *gorm.DB, teamID, agent, wing string) *gorm.DB {
	q := db.Where("team_id = ? AND room = ? AND agent = ?", teamID, DiaryRoom, agent)
	if wing != "" {
		q = q.Where("wing = ?", wing)
	}
	// A journal is append-only, so a diary entry is rarely ended — but it CAN be:
	// am_invalidate_drawer takes any drawer id, and nothing stops an agent
	// retracting a reflection it decides was wrong. Both the page and the count go
	// through here, so the filter is applied once and neither can drift from the
	// other — a total that includes retracted entries while the page excludes them
	// tells an agent its journal is larger than what it can read.
	return currentScope(q)
}

// Diary returns an agent's most recent diary entries (newest first), scoped via
// diaryScope. limit bounds the page; a non-positive limit is treated as the
// default by the caller, so this method trusts the value it is given. Ordering is
// filed_at DESC, id ASC for a stable total order even when two entries share a
// timestamp — mirroring the frozen tool's reverse-chronological read.
func (r *Repo) Diary(ctx context.Context, teamID, agent, wing string, limit int) ([]Drawer, error) {
	var rows []drawerRow
	if err := diaryScope(r.reader.WithContext(ctx), teamID, agent, wing).
		Order("filed_at DESC, id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Drawer, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromRow(row))
	}
	return out, nil
}

// DiaryCount is the total number of diary entries an agent has in scope, before
// the last_n page limit — it feeds diary_read's "total" so an agent can tell its
// journal is larger than the window it is reading (the frozen tool reports the
// same total/showing split).
func (r *Repo) DiaryCount(ctx context.Context, teamID, agent, wing string) (int64, error) {
	var n int64
	if err := diaryScope(r.reader.WithContext(ctx), teamID, agent, wing).
		Model(&drawerRow{}).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// --- domain <-> row translation -------------------------------------------

// toRow flattens a domain Drawer into its storage shape, joining entities with
// semicolons (the frozen palace's on-disk encoding).
func toRow(d Drawer) drawerRow {
	return drawerRow{
		TeamID:       d.TeamID,
		ID:           d.ID,
		Wing:         d.Wing,
		Room:         d.Room,
		SourceFile:   d.SourceFile,
		ChunkIndex:   d.ChunkIndex,
		Content:      d.Content,
		Entities:     strings.Join(d.Entities, ";"),
		ParentID:     d.ParentID,
		FiledAt:      d.FiledAt,
		ContentDate:  d.ContentDate,
		Agent:        d.Agent,
		Topic:        d.Topic,
		ContentKey:   d.ContentKey,
		ValidTo:      d.ValidTo,
		SupersededBy: d.SupersededBy,
		EndedReason:  d.EndedReason,
		EndedAt:      d.EndedAt,
	}
}

// fromRow rebuilds a domain Drawer from a row, splitting the semicolon-joined
// entities back into a slice (empty string -> nil, not a one-element [""]).
func fromRow(row drawerRow) Drawer {
	return Drawer{
		ID:           row.ID,
		TeamID:       row.TeamID,
		Wing:         row.Wing,
		Room:         row.Room,
		SourceFile:   row.SourceFile,
		ChunkIndex:   row.ChunkIndex,
		Content:      row.Content,
		Entities:     splitEntities(row.Entities),
		FiledAt:      row.FiledAt,
		ContentDate:  row.ContentDate,
		ParentID:     row.ParentID,
		Agent:        row.Agent,
		Topic:        row.Topic,
		ContentKey:   row.ContentKey,
		ValidTo:      row.ValidTo,
		SupersededBy: row.SupersededBy,
		EndedReason:  row.EndedReason,
		EndedAt:      row.EndedAt,
	}
}

// splitEntities reverses the semicolon join, dropping empty fields so a blank
// column yields nil rather than a slice of empty strings.
func splitEntities(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ListRandom returns a random sample of a team's drawers.
//
// The eval samples the corpus to build questions, and taking the newest N would
// mean sampling one week of a palace that holds years — the questions would all
// be about whatever the team happened to be doing lately, and the score would
// describe recall on recent memory only. SQLite's RANDOM() over an indexed team
// scan is enough here: this runs once per eval, not per query.
func (r *Repo) ListRandom(ctx context.Context, teamID, wing string, limit int) ([]Drawer, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.reader.WithContext(ctx).Where("team_id = ?", teamID)
	if wing != "" {
		q = q.Where("wing = ?", wing)
	}
	// Over-fetch, then keep at most one drawer per source file. A mined session
	// arrives as many parts sharing one source, and two eval cases seeded from
	// the same session are not independent observations — the bootstrap treats
	// them as if they were, which narrows every interval it prints. Drawers with
	// no source (hand-filed) are each their own cluster.
	var rows []drawerRow
	if err := q.Order("RANDOM()").Limit(limit * 5).Find(&rows).Error; err != nil {
		return nil, err
	}
	seen := make(map[string]bool, limit)
	out := make([]Drawer, 0, limit)
	for _, row := range rows {
		if row.SourceFile != "" {
			if seen[row.SourceFile] {
				continue
			}
			seen[row.SourceFile] = true
		}
		out = append(out, fromRow(row))
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// MemoryChunks returns every drawer belonging to one memory — the parent and its
// children — given any drawer id in it, ordered by chunk index.
//
// A memory over ChunkSize is stored as several rows sharing a parent, and any
// operation that treats one of those rows as the whole memory leaves the rest
// live and contradicting it. That is not hypothetical: an update rewrote chunk 0
// and left chunks 1 and 2 returning the retracted claim, above the correction, in
// search.
func (r *Repo) MemoryChunks(ctx context.Context, teamID, id string) ([]Drawer, error) {
	var self drawerRow
	if err := r.reader.WithContext(ctx).Where("team_id = ? AND id = ?", teamID, id).First(&self).Error; err != nil {
		return nil, err
	}
	root := self.ID
	if self.ParentID != "" {
		root = self.ParentID
	}
	var rows []drawerRow
	if err := r.reader.WithContext(ctx).
		Where("team_id = ? AND (id = ? OR parent_id = ?)", teamID, root, root).
		Order("chunk_index asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Drawer, len(rows))
	for i, row := range rows {
		out[i] = fromRow(row)
	}
	return out, nil
}

// WingIsEmpty reports whether a wing holds no drawers at all — the question
// behind "am I creating this wing right now?".
//
// It exists separately from Wings() because the caller is on the write path and
// needs one boolean, not the whole taxonomy: LIMIT 1 on the same
// idx_drawers_team_wing index stops at the first row rather than counting every
// drawer in a wing that may hold thousands.
func (r *Repo) WingIsEmpty(ctx context.Context, teamID, wing string) (bool, error) {
	var id string
	err := r.reader.WithContext(ctx).
		Model(&drawerRow{}).
		Select("id").
		Where("team_id = ? AND wing = ?", teamID, wing).
		Limit(1).
		Scan(&id).Error
	if err != nil {
		return false, err
	}
	return id == "", nil
}

// DrawerWings maps every EMBEDDED drawer id to the wing it is filed in, and
// separately lists the ids still awaiting a first embedding.
//
// Two columns of every row, which is the whole point: the drift check compares
// this against what each vector store believes, and loading whole drawers to do
// it would pull every memory's text into memory to read one field.
func (r *Repo) DrawerWings(ctx context.Context, teamID string) (embedded map[string]string, pending []string, err error) {
	var rows []struct {
		ID         string
		Wing       string
		EmbeddedAt *string
	}
	if err := r.reader.WithContext(ctx).
		Model(&drawerRow{}).
		Select("id", "wing", "embedded_at").
		Where("team_id = ?", teamID).
		Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	embedded = make(map[string]string, len(rows))
	for _, row := range rows {
		// A drawer awaiting its first embedding has no point yet, and that is a
		// queue rather than a fault. Separating them here is what lets the drift
		// check treat a MISSING point as a defect without a busy palace looking
		// broken.
		if row.EmbeddedAt == nil {
			pending = append(pending, row.ID)
			continue
		}
		embedded[row.ID] = row.Wing
	}
	return embedded, pending, nil
}

// ClosetWings maps every EMBEDDED closet id to the wing it is filed in — the
// closet half of DrawerWings, and for the same reason: closets keep a second
// copy of the wing in their stored payload, and nothing compared them. The
// embedded_at filter mirrors DrawerWings' pending split: a closet awaiting its
// first embedding is a queue, not a fault, so it is excluded here and counted
// separately via PendingClosetCount.
func (r *Repo) ClosetWings(ctx context.Context, teamID string) (map[string]string, error) {
	var rows []struct {
		ID   string
		Wing string
	}
	if err := r.reader.WithContext(ctx).
		Model(&closetRow{}).
		Select("id", "wing").
		Where("team_id = ? AND embedded_at IS NOT NULL", teamID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.ID] = row.Wing
	}
	return out, nil
}

// WingNames lists the wings a team has written to, for an error message that
// has to show the caller what exists. Wings() carries counts nobody needs here.
func (r *Repo) WingNames(ctx context.Context, teamID string) ([]string, error) {
	var names []string
	if err := r.reader.WithContext(ctx).
		Model(&drawerRow{}).
		Distinct("wing").
		Where("team_id = ?", teamID).
		Order("wing").
		Pluck("wing", &names).Error; err != nil {
		return nil, err
	}
	return names, nil
}

// InboxCount counts a wing's inbox drawers — findings handed over by another
// project's session, which are only read if something makes the reader look.
func (r *Repo) InboxCount(ctx context.Context, teamID, wing, room string) (int, error) {
	var n int64
	// LIVE MEMORIES, not rows. Both halves of that were wrong and each was wrong
	// on its own: retracted drawers were counted, so closing an inbox item never
	// moved the number that greets the next session; and chunks were counted while
	// the hint called them "memories", so the figure scaled with how long the
	// sender wrote. One room reported eight for two live memories.
	//
	// Counting ROOT chunks is exact rather than approximate: a retraction ends
	// every chunk of a memory (InvalidateDrawer and Supersede both do the whole
	// memory), so a live root implies live siblings and there is no half-ended
	// memory to miscount.
	if err := r.reader.WithContext(ctx).
		Model(&drawerRow{}).
		Where("team_id = ? AND wing = ? AND room = ? AND valid_to = '' AND parent_id = ''", teamID, wing, room).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}

// DrawersByIDs loads drawers by id, preserving the caller's order.
//
// Order is preserved deliberately: the bootstrap's eager tier is what the entry
// point points at, IN THE ORDER it points at it, and a map-ordered result would
// make the same wing bootstrap differently on each call. A session comparing two
// bootstraps would read that as the palace changing.
func (r *Repo) DrawersByIDs(ctx context.Context, teamID string, ids []string) ([]Drawer, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []drawerRow
	// CURRENT only. This hydrates am_bootstrap's inline records, which is a default
	// read route and the FIRST one a waking session takes — an entry edge is
	// written when a drawer is written and outlives an ending, so a retracted
	// record reached this way is a withdrawn claim presented as the thing to read
	// before doing anything else.
	if err := currentScope(r.reader.WithContext(ctx).
		Where("team_id = ? AND id IN ?", teamID, ids)).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]Drawer, len(rows))
	for _, row := range rows {
		byID[row.ID] = fromRow(row)
	}
	out := make([]Drawer, 0, len(ids))
	for _, id := range ids {
		if d, ok := byID[id]; ok {
			out = append(out, d)
		}
	}
	return out, nil
}
