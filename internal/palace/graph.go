package palace

import (
	"context"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This file holds the relational persistence for the navigable graph — the
// hallways and tunnels tables (migration 00009) and their repo methods — plus the
// drawer aggregation that build-graph reads. The derivation algorithms and the
// graph tools live in hallway.go / tunnel.go / graphquery.go.

// defaultStrength / defaultStability are the L7 initial values a freshly derived
// or created connection starts at (frozen DEFAULT_STRENGTH / DEFAULT_STABILITY).
const (
	defaultStrength  = 1.0
	defaultStability = 1.0
)

// initDynamics returns the starting dynamics for a new connection: full strength
// and stability, activated now, never accessed. Used when a hallway is first
// derived or a tunnel first created.
func initDynamics(now string) Dynamics {
	return Dynamics{Strength: defaultStrength, Stability: defaultStability, LastActivated: now, AccessCount: 0}
}

// --- hallways -------------------------------------------------------------

// hallwayRow is the gorm view of the hallways table.
type hallwayRow struct {
	TeamID        string  `gorm:"column:team_id;primaryKey"`
	ID            string  `gorm:"column:id;primaryKey"`
	Wing          string  `gorm:"column:wing"`
	EntityA       string  `gorm:"column:entity_a"`
	EntityB       string  `gorm:"column:entity_b"`
	CoOccurrence  int     `gorm:"column:co_occurrence"`
	Rooms         string  `gorm:"column:rooms"` // semicolon-joined
	Label         string  `gorm:"column:label"`
	CreatedAt     string  `gorm:"column:created_at"`
	CreatedBy     string  `gorm:"column:created_by"`
	Strength      float64 `gorm:"column:strength"`
	Stability     float64 `gorm:"column:stability"`
	LastActivated string  `gorm:"column:last_activated"`
	AccessCount   int     `gorm:"column:access_count"`
}

func (hallwayRow) TableName() string { return "hallways" }

func toHallwayRow(h Hallway) hallwayRow {
	return hallwayRow{
		TeamID: h.TeamID, ID: h.ID, Wing: h.Wing, EntityA: h.EntityA, EntityB: h.EntityB,
		CoOccurrence: h.CoOccurrence, Rooms: strings.Join(h.Rooms, ";"), Label: h.Label,
		CreatedAt: h.CreatedAt, CreatedBy: h.CreatedBy,
		Strength: h.Strength, Stability: h.Stability, LastActivated: h.LastActivated, AccessCount: h.AccessCount,
	}
}

func fromHallwayRow(r hallwayRow) Hallway {
	return Hallway{
		ID: r.ID, TeamID: r.TeamID, Wing: r.Wing, EntityA: r.EntityA, EntityB: r.EntityB,
		CoOccurrence: r.CoOccurrence, Rooms: splitEntities(r.Rooms), Label: r.Label,
		CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy,
		Dynamics: Dynamics{Strength: r.Strength, Stability: r.Stability, LastActivated: r.LastActivated, AccessCount: r.AccessCount},
	}
}

// ReplaceWingHallways atomically swaps a wing's hallways: it deletes the wing's
// existing rows and inserts the freshly derived set in one transaction, so a
// recompute replaces rather than accumulates. An empty set just clears the wing.
func (r *Repo) ReplaceWingHallways(ctx context.Context, teamID, wing string, halls []Hallway) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("team_id = ? AND wing = ?", teamID, wing).Delete(&hallwayRow{}).Error; err != nil {
			return err
		}
		if len(halls) == 0 {
			return nil
		}
		rows := make([]hallwayRow, len(halls))
		for i, h := range halls {
			rows[i] = toHallwayRow(h)
		}
		return tx.Create(&rows).Error
	})
}

// ListHallways returns a team's hallways, optionally narrowed to one wing,
// ordered by descending co-occurrence so the strongest links lead.
func (r *Repo) ListHallways(ctx context.Context, teamID, wing string) ([]Hallway, error) {
	q := r.db.WithContext(ctx).Where("team_id = ?", teamID)
	if wing != "" {
		q = q.Where("wing = ?", wing)
	}
	var rows []hallwayRow
	if err := q.Order("co_occurrence DESC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Hallway, len(rows))
	for i, row := range rows {
		out[i] = fromHallwayRow(row)
	}
	return out, nil
}

// DeleteHallway removes a hallway by id, reporting whether a row was deleted.
func (r *Repo) DeleteHallway(ctx context.Context, teamID, id string) (bool, error) {
	res := r.db.WithContext(ctx).Where("team_id = ? AND id = ?", teamID, id).Delete(&hallwayRow{})
	return res.RowsAffected > 0, res.Error
}

// --- tunnels --------------------------------------------------------------

// tunnelRow is the gorm view of the tunnels table.
type tunnelRow struct {
	TeamID         string  `gorm:"column:team_id;primaryKey"`
	ID             string  `gorm:"column:id;primaryKey"`
	SourceWing     string  `gorm:"column:source_wing"`
	SourceRoom     string  `gorm:"column:source_room"`
	SourceDrawerID string  `gorm:"column:source_drawer_id"`
	TargetWing     string  `gorm:"column:target_wing"`
	TargetRoom     string  `gorm:"column:target_room"`
	TargetDrawerID string  `gorm:"column:target_drawer_id"`
	Label          string  `gorm:"column:label"`
	Kind           string  `gorm:"column:kind"`
	CreatedAt      string  `gorm:"column:created_at"`
	UpdatedAt      string  `gorm:"column:updated_at"`
	Strength       float64 `gorm:"column:strength"`
	Stability      float64 `gorm:"column:stability"`
	LastActivated  string  `gorm:"column:last_activated"`
	AccessCount    int     `gorm:"column:access_count"`
}

func (tunnelRow) TableName() string { return "tunnels" }

func toTunnelRow(t Tunnel) tunnelRow {
	return tunnelRow{
		TeamID: t.TeamID, ID: t.ID,
		SourceWing: t.Source.Wing, SourceRoom: t.Source.Room, SourceDrawerID: t.Source.DrawerID,
		TargetWing: t.Target.Wing, TargetRoom: t.Target.Room, TargetDrawerID: t.Target.DrawerID,
		Label: t.Label, Kind: string(t.Kind), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		Strength: t.Strength, Stability: t.Stability, LastActivated: t.LastActivated, AccessCount: t.AccessCount,
	}
}

func fromTunnelRow(r tunnelRow) Tunnel {
	return Tunnel{
		ID: r.ID, TeamID: r.TeamID,
		Source: Endpoint{Wing: r.SourceWing, Room: r.SourceRoom, DrawerID: r.SourceDrawerID},
		Target: Endpoint{Wing: r.TargetWing, Room: r.TargetRoom, DrawerID: r.TargetDrawerID},
		Label: r.Label, Kind: TunnelKind(r.Kind), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		Dynamics: Dynamics{Strength: r.Strength, Stability: r.Stability, LastActivated: r.LastActivated, AccessCount: r.AccessCount},
	}
}

// SaveTunnel upserts a tunnel by (team_id, id). Re-saving the canonical id of an
// existing tunnel replaces it, which is what create_tunnel's upsert needs.
func (r *Repo) SaveTunnel(ctx context.Context, t Tunnel) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&[]tunnelRow{toTunnelRow(t)}).Error
}

// SaveTunnels upserts many tunnels at once (the recompute path). Empty is a no-op.
func (r *Repo) SaveTunnels(ctx context.Context, tunnels []Tunnel) error {
	if len(tunnels) == 0 {
		return nil
	}
	rows := make([]tunnelRow, len(tunnels))
	for i, t := range tunnels {
		rows[i] = toTunnelRow(t)
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&rows).Error
}

// GetTunnel loads one tunnel by id, or gorm.ErrRecordNotFound if absent.
func (r *Repo) GetTunnel(ctx context.Context, teamID, id string) (Tunnel, error) {
	var row tunnelRow
	if err := r.db.WithContext(ctx).Where("team_id = ? AND id = ?", teamID, id).First(&row).Error; err != nil {
		return Tunnel{}, err
	}
	return fromTunnelRow(row), nil
}

// DeleteTunnel removes a tunnel by id, reporting whether a row was deleted.
func (r *Repo) DeleteTunnel(ctx context.Context, teamID, id string) (bool, error) {
	res := r.db.WithContext(ctx).Where("team_id = ? AND id = ?", teamID, id).Delete(&tunnelRow{})
	return res.RowsAffected > 0, res.Error
}

// ListTunnels returns a team's tunnels, optionally filtered to those touching a
// wing on either endpoint, newest first.
func (r *Repo) ListTunnels(ctx context.Context, teamID, wing string) ([]Tunnel, error) {
	q := r.db.WithContext(ctx).Where("team_id = ?", teamID)
	if wing != "" {
		q = q.Where("source_wing = ? OR target_wing = ?", wing, wing)
	}
	var rows []tunnelRow
	if err := q.Order("created_at DESC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Tunnel, len(rows))
	for i, row := range rows {
		out[i] = fromTunnelRow(row)
	}
	return out, nil
}

// DeleteTunnelsByKind drops every auto-generated tunnel of a kind for a team, so
// recompute can rebuild the derived (entity) tunnels from scratch without
// touching explicit, user-authored ones.
func (r *Repo) DeleteTunnelsByKind(ctx context.Context, teamID string, kind TunnelKind) error {
	return r.db.WithContext(ctx).Where("team_id = ? AND kind = ?", teamID, string(kind)).Delete(&tunnelRow{}).Error
}

// --- graph aggregation ----------------------------------------------------

// RoomWing is one (room, wing) pairing with how many drawers back it and the most
// recent content date seen — the raw rows build-graph folds into the room->wings
// view. The "general" room and blank wings are excluded as in the frozen graph.
type RoomWing struct {
	Room  string `gorm:"column:room"`
	Wing  string `gorm:"column:wing"`
	Count int    `gorm:"column:count"`
	Recent string `gorm:"column:recent"`
}

// GraphRoomWings returns every (room, wing) pairing for a team with its drawer
// count and most recent content date. The graph tools aggregate these in Go into
// rooms and their spanning wings; doing the grouping in SQL keeps it cheap as the
// palace grows. Rooms named "general" or with no wing are skipped (they are not
// navigable ideas), matching the frozen build_graph filter.
func (r *Repo) GraphRoomWings(ctx context.Context, teamID string) ([]RoomWing, error) {
	var rows []RoomWing
	if err := r.db.WithContext(ctx).
		Model(&drawerRow{}).
		Select("room, wing, COUNT(*) AS count, MAX(content_date) AS recent").
		Where("team_id = ? AND wing != '' AND room != '' AND room != ?", teamID, "general").
		Group("room, wing").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// WingsWithDrawers returns the distinct wings a team has any drawer in — the set
// recompute_graph iterates and prunes orphan graph records against.
func (r *Repo) WingsWithDrawers(ctx context.Context, teamID string) ([]string, error) {
	var wings []string
	if err := r.db.WithContext(ctx).
		Model(&drawerRow{}).
		Where("team_id = ? AND wing != ''", teamID).
		Distinct().
		Pluck("wing", &wings).Error; err != nil {
		return nil, err
	}
	return wings, nil
}

// DrawersForHallways loads the (room, entities) of a wing's drawers, the minimal
// projection hallway derivation needs (it counts entity pairs per drawer and the
// rooms they met in). Sentinel-free: every drawer row is real content.
func (r *Repo) DrawersForHallways(ctx context.Context, teamID, wing string) ([]Drawer, error) {
	var rows []drawerRow
	if err := r.db.WithContext(ctx).
		Select("team_id, id, wing, room, content, entities").
		Where("team_id = ? AND wing = ?", teamID, wing).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Drawer, len(rows))
	for i, row := range rows {
		out[i] = fromRow(row)
	}
	return out, nil
}
