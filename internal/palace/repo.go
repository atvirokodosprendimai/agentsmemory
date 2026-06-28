package palace

import (
	"context"
	"strings"

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
	Entities    string `gorm:"column:entities"`     // semicolon-joined on disk
	ParentID    string `gorm:"column:parent_id"`
	FiledAt     string `gorm:"column:filed_at"`
	ContentDate string `gorm:"column:content_date"`
	Agent       string `gorm:"column:agent"` // diary: whose journal (lowercased); "" for normal drawers
	Topic       string `gorm:"column:topic"` // diary: free grouping tag; "" for normal drawers
}

// TableName pins the table so gorm does not pluralise to "drawer_rows".
func (drawerRow) TableName() string { return "drawers" }

// WingStat is one row of the list_wings aggregation: a wing with how many
// drawers and distinct rooms it holds. The json tags keep the MCP wire shape
// snake_case, matching the drawer views (the struct is returned to agents as-is).
type WingStat struct {
	Wing    string `gorm:"column:wing" json:"wing"`
	Drawers int    `gorm:"column:drawers" json:"drawers"`
	Rooms   int    `gorm:"column:rooms" json:"rooms"`
}

// RoomStat is one row of the list_rooms aggregation: a room (within its wing)
// and its drawer count.
type RoomStat struct {
	Wing    string `gorm:"column:wing" json:"wing"`
	Room    string `gorm:"column:room" json:"room"`
	Drawers int    `gorm:"column:drawers" json:"drawers"`
}

// Repo is the gorm-backed persistence for drawer metadata. It owns only the
// `drawers` table; the embeddings live behind the store seam, joined by id. gorm
// is the query layer — goose owns the schema, so AutoMigrate is never called.
type Repo struct {
	db *gorm.DB
}

// NewRepo wraps an open gorm DB whose schema has been migrated by goose.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Save upserts drawers by (team_id, id). Re-saving the same id replaces the row,
// which is exactly what idempotent re-mining needs. An empty slice is a no-op.
func (r *Repo) Save(ctx context.Context, drawers []Drawer) error {
	if len(drawers) == 0 {
		return nil
	}
	rows := make([]drawerRow, 0, len(drawers))
	for _, d := range drawers {
		rows = append(rows, toRow(d))
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&rows).Error
}

// Get loads a single drawer by id within a team. A missing drawer returns
// gorm.ErrRecordNotFound, which the caller translates into a tool-level error.
func (r *Repo) Get(ctx context.Context, teamID, id string) (Drawer, error) {
	var row drawerRow
	if err := r.db.WithContext(ctx).
		Where("team_id = ? AND id = ?", teamID, id).
		First(&row).Error; err != nil {
		return Drawer{}, err
	}
	return fromRow(row), nil
}

// IDsBySource returns the ids of every drawer filed from one source within a
// (team, wing, room). add_drawer uses it to purge a named source's prior chunks
// before re-filing it, so re-adding shorter content cannot leave stale
// higher-index chunks behind. Order is unspecified.
func (r *Repo) IDsBySource(ctx context.Context, teamID, wing, room, source string) ([]string, error) {
	var ids []string
	if err := r.db.WithContext(ctx).
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
	if err := r.db.WithContext(ctx).
		Where("team_id = ? AND id IN ?", teamID, ids).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = fromRow(row)
	}
	return out, nil
}

// DrawerPatch carries the optional fields update_drawer may change. A nil field
// means "leave unchanged", distinguishing "set to empty" from "not provided".
type DrawerPatch struct {
	Content *string
	Wing    *string
	Room    *string
}

// Update applies a patch to an existing drawer in place, keyed by its id (the id
// is stable — it is not recomputed from the new wing/room, matching the Python
// contract where update_drawer edits a drawer without re-chunking it). It
// returns the updated drawer, or gorm.ErrRecordNotFound if the id is unknown.
func (r *Repo) Update(ctx context.Context, teamID, id string, patch DrawerPatch) (Drawer, error) {
	updates := map[string]any{}
	if patch.Content != nil {
		updates["content"] = *patch.Content
	}
	if patch.Wing != nil {
		updates["wing"] = *patch.Wing
	}
	if patch.Room != nil {
		updates["room"] = *patch.Room
	}
	if len(updates) > 0 {
		res := r.db.WithContext(ctx).
			Model(&drawerRow{}).
			Where("team_id = ? AND id = ?", teamID, id).
			Updates(updates)
		if res.Error != nil {
			return Drawer{}, res.Error
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
	return r.db.WithContext(ctx).
		Where("team_id = ? AND id = ?", teamID, id).
		Delete(&drawerRow{}).Error
}

// List returns drawers for a team, optionally narrowed to a wing and/or room,
// newest first. limit bounds the page (a non-positive limit defaults to 50 to
// avoid an unbounded scan); offset paginates.
func (r *Repo) List(ctx context.Context, teamID, wing, room string, limit, offset int) ([]Drawer, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q := r.db.WithContext(ctx).Where("team_id = ?", teamID)
	if wing != "" {
		q = q.Where("wing = ?", wing)
	}
	if room != "" {
		q = q.Where("room = ?", room)
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

// Wings aggregates a team's drawers by wing — the list_wings backend. The
// GROUP BY rides idx_drawers_team_wing, keeping it cheap as the palace grows.
func (r *Repo) Wings(ctx context.Context, teamID string) ([]WingStat, error) {
	var stats []WingStat
	if err := r.db.WithContext(ctx).
		Model(&drawerRow{}).
		Select("wing, COUNT(*) AS drawers, COUNT(DISTINCT room) AS rooms").
		Where("team_id = ?", teamID).
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
	q := r.db.WithContext(ctx).
		Model(&drawerRow{}).
		Select("wing, room, COUNT(*) AS drawers").
		Where("team_id = ?", teamID)
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
	return q
}

// Diary returns an agent's most recent diary entries (newest first), scoped via
// diaryScope. limit bounds the page; a non-positive limit is treated as the
// default by the caller, so this method trusts the value it is given. Ordering is
// filed_at DESC, id ASC for a stable total order even when two entries share a
// timestamp — mirroring the frozen tool's reverse-chronological read.
func (r *Repo) Diary(ctx context.Context, teamID, agent, wing string, limit int) ([]Drawer, error) {
	var rows []drawerRow
	if err := diaryScope(r.db.WithContext(ctx), teamID, agent, wing).
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
	if err := diaryScope(r.db.WithContext(ctx), teamID, agent, wing).
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
		TeamID:      d.TeamID,
		ID:          d.ID,
		Wing:        d.Wing,
		Room:        d.Room,
		SourceFile:  d.SourceFile,
		ChunkIndex:  d.ChunkIndex,
		Content:     d.Content,
		Entities:    strings.Join(d.Entities, ";"),
		ParentID:    d.ParentID,
		FiledAt:     d.FiledAt,
		ContentDate: d.ContentDate,
		Agent:       d.Agent,
		Topic:       d.Topic,
	}
}

// fromRow rebuilds a domain Drawer from a row, splitting the semicolon-joined
// entities back into a slice (empty string -> nil, not a one-element [""]).
func fromRow(row drawerRow) Drawer {
	return Drawer{
		ID:          row.ID,
		TeamID:      row.TeamID,
		Wing:        row.Wing,
		Room:        row.Room,
		SourceFile:  row.SourceFile,
		ChunkIndex:  row.ChunkIndex,
		Content:     row.Content,
		Entities:    splitEntities(row.Entities),
		FiledAt:     row.FiledAt,
		ContentDate: row.ContentDate,
		ParentID:    row.ParentID,
		Agent:       row.Agent,
		Topic:       row.Topic,
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
