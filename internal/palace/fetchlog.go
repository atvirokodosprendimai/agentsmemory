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
// It is deliberately two raw counts and not a ratio, and it stays that way now
// that FetchRatesByProfile exists: these two are TEAM-WIDE totals, useful for
// answering "is the fetch join recording anything at all", and a rate derived
// from them would be the population error ADR-007 exists to stop — it would
// average across every ranking profile that ran in the window. The rate lives
// next door, grouped by profile, with its denominator attached.
func (s *Service) CountFetches(ctx context.Context, teamID string, since time.Duration) (fetches, recallsFetched int, err error) {
	if since <= 0 {
		since = 24 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-since).Format(time.RFC3339)
	q := s.repo.reader.WithContext(ctx).Model(&drawerFetchRow{}).
		Where("team_id = ? AND created_at >= ?", teamID, cutoff)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return 0, 0, err
	}
	var distinct int64
	if err := s.repo.reader.WithContext(ctx).Model(&drawerFetchRow{}).
		Where("team_id = ? AND created_at >= ?", teamID, cutoff).
		Distinct("search_id").Count(&distinct).Error; err != nil {
		return 0, 0, err
	}
	return int(total), int(distinct), nil
}

// FetchRate is the fetch ratio for ONE ranking profile, carrying the denominator
// it was computed over.
//
// The three fields travel together on purpose. ADR-007's rule is that no number
// may be quoted past its population, and this is the shape that makes obeying it
// the default: anything rendering this struct renders the profile and the
// denominator beside the rate, and a caller who wants the bare number has to go
// and take it out.
type FetchRate struct {
	// ProfileID is the ranking that produced these recalls.
	ProfileID string
	// RecallsLogged is the denominator: recalls this palace RECORDED in the window
	// under this profile. Not recalls that HAPPENED — SkipTelemetry means the
	// eval's thousands of synthetic queries write no row at all, and counting them
	// would move the rate with sweeps nobody was measuring.
	RecallsLogged int
	// RecallsFetched is the numerator: how many of those recalls a caller went on
	// to read something from. Recalls, never fetches — two reads off one page are
	// one useful recall, and counting fetches would let the rate exceed 1 on
	// exactly the usage this signal exists to reward.
	RecallsFetched int
}

// Rate returns the fraction of logged recalls under this profile that a caller
// fetched from, in [0,1].
//
// The zero denominator is unreachable from FetchRatesByProfile — a group exists
// only where a row does — but it is guarded rather than assumed, because the
// failure is not a wrong number: float division yields NaN, encoding/json REFUSES
// to marshal NaN, and one hand-built value would then fail the whole
// am_recall_stats response instead of one field of it.
func (r FetchRate) Rate() float64 {
	if r.RecallsLogged == 0 {
		return 0
	}
	return float64(r.RecallsFetched) / float64(r.RecallsLogged)
}

// FetchRatesByProfile reports, per ranking profile, what fraction of the recalls
// this palace LOGGED in the window a caller went on to read something from.
//
// This is ADR-028's deferral discharged. T3 published raw counts and stopped on
// purpose: "38% of recalls were followed by a fetch" is uninterpretable without
// the ranking that produced them, because a change to the blend moves the number
// and the average across such a change describes no configuration anyone ran. So
// the group is the profile, and the report is one row per profile rather than one
// number.
//
// ⚠ Recalls whose profile_id is NULL — every row written before migration 00038 —
// are EXCLUDED rather than grouped. They are real recalls, but nothing records
// which ranking took them, so they are evidence about no profile at all; folding
// them together would publish a rate for a configuration that never existed. On
// a corpus predating the column this correctly returns nothing, and narrowing the
// window to post-deploy rows is what fills it.
//
// A window holding no logged recall returns NO rows, never a zero rate: "nothing
// was fetched" and "nothing was measured" must not render alike.
func (s *Service) FetchRatesByProfile(ctx context.Context, teamID string, since time.Duration) ([]FetchRate, error) {
	if since <= 0 {
		since = 24 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-since).Format(time.RFC3339)
	var rates []FetchRate
	// DISTINCT inside the join rather than COUNT(DISTINCT …) outside it: the
	// numerator counts recalls that were fetched, so a page read twice must join
	// once. The other spelling lets the rate exceed 1 on the heaviest readers.
	err := s.repo.reader.WithContext(ctx).Model(&searchEventRow{}).
		Select("search_events.profile_id AS profile_id, COUNT(*) AS recalls_logged, "+
			"COUNT(f.search_id) AS recalls_fetched").
		Joins("LEFT JOIN (SELECT DISTINCT team_id, search_id FROM drawer_fetches) f "+
			"ON f.search_id = search_events.id AND f.team_id = search_events.team_id").
		Where("search_events.team_id = ? AND search_events.created_at >= ? AND search_events.profile_id IS NOT NULL",
			teamID, cutoff).
		Group("search_events.profile_id").
		Order("search_events.profile_id").
		Scan(&rates).Error
	if err != nil {
		return nil, err
	}
	return rates, nil
}
