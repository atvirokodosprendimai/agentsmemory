package palace

import (
	"context"
	"time"
)

// ADR-028 T3. The consuming half of implicit relevance feedback.
//
// `search_events` records that a recall happened and how many hits it returned.
// It records no drawer identity, so until this file the palace could not answer
// the one question that says whether a page was any good: did the caller go on to
// READ anything from it. A fetch that names a search_id is the closest thing to a
// relevance click this system can observe, and unlike a labelled eval set it grows
// with usage instead of with someone's labelling budget.
//
// Recording is best-effort for the same reason `recordSearch` is: a statistics
// write must never be able to fail a read. A lost row costs one observation.

// drawerFetchRow is the gorm view of one recorded fetch.
type drawerFetchRow struct {
	ID       string `gorm:"column:id;primaryKey"`
	TeamID   string `gorm:"column:team_id"`
	SearchID string `gorm:"column:search_id"`
	// DrawerID is the drawer that was RETURNED, never the id that was requested.
	// A request for an id that does not resolve is not a click, and counting one
	// would put misses in the numerator of every ratio derived from this table.
	DrawerID  string `gorm:"column:drawer_id"`
	Whole     int    `gorm:"column:whole"`
	CreatedAt string `gorm:"column:created_at"`
}

// TableName pins the table name so gorm does not pluralise the struct name.
func (drawerFetchRow) TableName() string { return "drawer_fetches" }

// RecordFetch records that a caller fetched drawerID while naming searchID as the
// recall that sent it there.
//
// It is a no-op when either id is empty or when searchID is not the shape Search
// mints — the caller decides nothing about validity, so a malformed id from a
// confused client cannot pollute the join. That mirrors the span annotator, which
// refuses the same values rather than reporting them.
//
// Exported because the caller is `internal/mcpserver`, which is where the tool
// boundary is and therefore the only place that knows a fetch SUCCEEDED.
func (s *Service) RecordFetch(ctx context.Context, teamID, searchID, drawerID string, whole bool) {
	if teamID == "" || drawerID == "" || !ValidSearchID(searchID) {
		return
	}
	w := 0
	if whole {
		w = 1
	}
	s.repo.recordFetch(ctx, drawerFetchRow{
		TeamID: teamID, SearchID: searchID, DrawerID: drawerID, Whole: w,
	})
}

// recordFetch writes one fetch row, best-effort.
func (r *Repo) recordFetch(ctx context.Context, f drawerFetchRow) {
	if f.ID == "" {
		f.ID = randomID()
	}
	if f.CreatedAt == "" {
		f.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_ = r.db.WithContext(ctx).Create(&f).Error
}

// CountFetches returns how many fetches this team recorded in the window, and how
// many DISTINCT recalls those fetches name.
//
// It is deliberately two raw counts and not a ratio. ADR-028's deferral puts the
// ratio behind `profile_id` on the durable row, because "38% of recalls were
// followed by a fetch" is uninterpretable without knowing which ranking profile
// produced them — and the denominator is recalls THAT WERE LOGGED, since
// SkipTelemetry means some recalls write no search_events row at all. Publishing
// a raw count now is what makes the write observable through a served surface;
// publishing a rate now would be the population error this corpus keeps retracting.
func (s *Service) CountFetches(ctx context.Context, teamID string, since time.Duration) (fetches, recallsFetched int, err error) {
	if since <= 0 {
		since = 24 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-since).Format(time.RFC3339)
	q := s.repo.db.WithContext(ctx).Model(&drawerFetchRow{}).
		Where("team_id = ? AND created_at >= ?", teamID, cutoff)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return 0, 0, err
	}
	var distinct int64
	if err := s.repo.db.WithContext(ctx).Model(&drawerFetchRow{}).
		Where("team_id = ? AND created_at >= ?", teamID, cutoff).
		Distinct("search_id").Count(&distinct).Error; err != nil {
		return 0, 0, err
	}
	return int(total), int(distinct), nil
}
