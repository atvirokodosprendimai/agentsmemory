package palace

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Admin operations over a team's palace: merge_wing (relabel a wing) and
// memories_filed_away (a recent-activity summary). The frozen sync (prune drawers
// whose on-disk source files vanished) and hook_settings (local Claude Code hook
// config) are intentionally absent — both are single-user-local concepts with no
// meaning on a multi-tenant server that has neither the user's filesystem nor
// per-team hooks, the same reason mine takes text rather than a server path.

// MergeWingResult reports a wing merge: the sources folded in, the target, and how
// many drawers and closets were relabeled.
type MergeWingResult struct {
	Sources []string `json:"sources"`
	Target  string   `json:"target"`
	Drawers int64    `json:"drawers_relabeled"`
	Closets int64    `json:"closets_relabeled"`
}

// MergeWing folds one or more source wings into a target, relabeling the `wing` of
// every drawer and closet in place (ids unchanged), the frozen merge_wing. The
// derived graph (hallways/tunnels) is NOT rebuilt here — call recompute_graph
// afterwards, as the frozen tool instructs. Vectors are not re-written: their
// payload wing is advisory (search filters on the drawer row's wing), so a merge
// needs no re-embedding. Idempotent: merging an already-merged wing is a no-op.
func (s *Service) MergeWing(ctx context.Context, teamID string, sources []string, target string) (MergeWingResult, error) {
	tgt, err := SanitizeName(target, "target")
	if err != nil {
		return MergeWingResult{}, err
	}
	clean := make([]string, 0, len(sources))
	for _, src := range sources {
		c, err := SanitizeName(src, "source")
		if err != nil {
			return MergeWingResult{}, err
		}
		if c != tgt { // merging a wing into itself is a no-op; drop it
			clean = append(clean, c)
		}
	}
	if len(clean) == 0 {
		return MergeWingResult{Sources: clean, Target: tgt}, nil
	}

	drawers, err := s.repo.RelabelDrawerWing(ctx, teamID, clean, tgt)
	if err != nil {
		return MergeWingResult{}, fmt.Errorf("relabel drawers: %w", err)
	}
	closets, err := s.repo.RelabelClosetWing(ctx, teamID, clean, tgt)
	if err != nil {
		return MergeWingResult{}, fmt.Errorf("relabel closets: %w", err)
	}
	return MergeWingResult{Sources: clean, Target: tgt, Drawers: drawers, Closets: closets}, nil
}

// FiledAwayResult is the memories_filed_away summary: how much a team has filed,
// when it last filed, and the breadth of its palace.
type FiledAwayResult struct {
	Count        int64  `json:"count"`
	Wings        int64  `json:"wings"`
	Rooms        int64  `json:"rooms"`
	LastFiledAt  string `json:"last_filed_at,omitempty"`
	Message      string `json:"message"`
}

// MemoriesFiledAway summarises what a team has stored — total drawers, distinct
// wings and rooms, and the most recent filing. It is the SaaS reading of the
// frozen checkpoint-acknowledge tool: rather than a local hook-state file, it
// reports the team's actual filed memory at a glance.
func (s *Service) MemoriesFiledAway(ctx context.Context, teamID string) (FiledAwayResult, error) {
	count, lastFiledAt, wings, rooms, err := s.repo.FiledAwaySummary(ctx, teamID)
	if err != nil {
		return FiledAwayResult{}, err
	}
	msg := fmt.Sprintf("%d memories filed across %d wings and %d rooms", count, wings, rooms)
	if count == 0 {
		msg = "No memories filed yet"
	}
	return FiledAwayResult{Count: count, Wings: wings, Rooms: rooms, LastFiledAt: lastFiledAt, Message: msg}, nil
}

// RelabelDrawerWing moves every drawer in any of the source wings to the target
// wing for a team, returning how many rows changed. Ids are unchanged.
func (r *Repo) RelabelDrawerWing(ctx context.Context, teamID string, sources []string, target string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&drawerRow{}).
		Where("team_id = ? AND wing IN ?", teamID, sources).
		Update("wing", target)
	return res.RowsAffected, res.Error
}

// RelabelClosetWing is the closet half of a wing merge.
func (r *Repo) RelabelClosetWing(ctx context.Context, teamID string, sources []string, target string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&closetRow{}).
		Where("team_id = ? AND wing IN ?", teamID, sources).
		Update("wing", target)
	return res.RowsAffected, res.Error
}

// FiledAwaySummary returns a team's drawer count, most recent filing time, and the
// number of distinct wings and rooms — the numbers behind memories_filed_away.
func (r *Repo) FiledAwaySummary(ctx context.Context, teamID string) (count int64, lastFiledAt string, wings, rooms int64, err error) {
	base := func() *gorm.DB { return r.db.WithContext(ctx).Model(&drawerRow{}).Where("team_id = ?", teamID) }
	if err = base().Count(&count).Error; err != nil {
		return
	}
	if err = base().Select("COALESCE(MAX(filed_at), '')").Scan(&lastFiledAt).Error; err != nil {
		return
	}
	if err = base().Distinct("wing").Count(&wings).Error; err != nil {
		return
	}
	err = base().Distinct("room").Count(&rooms).Error
	return
}
