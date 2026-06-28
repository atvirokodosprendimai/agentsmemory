package palace

import (
	"context"
	"fmt"
	"time"
)

// This file is the read side of the async-embedding queue that AbsorbDrawers /
// AbsorbClosets feed: a migration import writes rows with embedded_at NULL, and
// the background worker (internal/embedworker) calls EmbedPendingForTeam to build
// their vectors off the request path. Keeping embedding here — behind the same
// Service that owns the synchronous filing tail — means absorbed rows are embedded
// by exactly the same model and store seam as native writes.

// embedBatch is the default number of pending rows embedded in one ollama call.
// Sized like the importer's old per-batch flush: big enough to amortise the call,
// small enough that one tenant's step stays short so round-robin stays fair.
const embedBatch = 64

// PendingCount reports how many of a team's drawers + closets still await
// background embedding — the "indexing N" signal the importer returns on finalize.
func (s *Service) PendingCount(ctx context.Context, teamID string) (int, error) {
	d, err := s.repo.PendingDrawerCount(ctx, teamID)
	if err != nil {
		return 0, err
	}
	c, err := s.repo.PendingClosetCount(ctx, teamID)
	if err != nil {
		return 0, err
	}
	return int(d + c), nil
}

// TeamsWithPending lists tenants holding any drawer OR closet awaiting embedding,
// deduped, so the worker can round-robin them rather than draining one giant
// migration first. limit bounds each underlying scan (0 = unbounded).
func (s *Service) TeamsWithPending(ctx context.Context, limit int) ([]string, error) {
	dt, err := s.repo.TeamsWithPendingDrawers(ctx, limit)
	if err != nil {
		return nil, err
	}
	ct, err := s.repo.TeamsWithPendingClosets(ctx, limit)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(dt)+len(ct))
	out := make([]string, 0, len(dt)+len(ct))
	for _, t := range dt {
		if _, dup := seen[t]; !dup {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	for _, t := range ct {
		if _, dup := seen[t]; !dup {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out, nil
}

// EmbedPendingForTeam indexes up to `batch` pending drawers AND up to `batch`
// pending closets for one tenant, building their vectors and stamping embedded_at.
// It returns how many rows it indexed this call (0 = the tenant is caught up); the
// worker calls it repeatedly until it drains.
//
// Ordering is deliberate: vectors are upserted BEFORE embedded_at is stamped, so a
// crash in between leaves the row pending and the next call re-embeds it
// idempotently — there is never a stamped row without its vector. A concurrent
// re-absorb of identical content is a harmless no-op (DoNothing on conflict).
func (s *Service) EmbedPendingForTeam(ctx context.Context, teamID string, batch int) (int, error) {
	if batch <= 0 {
		batch = embedBatch
	}
	nd, err := s.embedPendingDrawers(ctx, teamID, batch)
	if err != nil {
		return nd, err
	}
	nc, err := s.embedPendingClosets(ctx, teamID, batch)
	return nd + nc, err
}

// embedPendingDrawers embeds one batch of a tenant's un-embedded drawers.
func (s *Service) embedPendingDrawers(ctx context.Context, teamID string, batch int) (int, error) {
	pending, err := s.repo.PendingDrawers(ctx, teamID, batch)
	if err != nil {
		return 0, fmt.Errorf("load pending drawers: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}
	texts := make([]string, len(pending))
	for i, d := range pending {
		texts[i] = d.Content
	}
	vectors, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("embed pending drawers: %w", err)
	}
	// Vectors first (durable, joinable), THEN clear the pending flag.
	if err := s.upsertDrawerVectors(ctx, teamID, pending, vectors); err != nil {
		return 0, err
	}
	ids := make([]string, len(pending))
	for i, d := range pending {
		ids[i] = d.ID
	}
	at := time.Now().UTC().Format(time.RFC3339)
	if err := s.repo.MarkDrawersEmbedded(ctx, teamID, ids, at); err != nil {
		return 0, fmt.Errorf("mark drawers embedded: %w", err)
	}
	return len(pending), nil
}

// embedPendingClosets embeds one batch of a tenant's un-embedded closets.
func (s *Service) embedPendingClosets(ctx context.Context, teamID string, batch int) (int, error) {
	pending, err := s.repo.PendingClosets(ctx, teamID, batch)
	if err != nil {
		return 0, fmt.Errorf("load pending closets: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}
	texts := make([]string, len(pending))
	for i, c := range pending {
		texts[i] = c.Document
	}
	vectors, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("embed pending closets: %w", err)
	}
	if err := s.upsertClosetVectors(ctx, teamID, pending, vectors); err != nil {
		return 0, err
	}
	ids := make([]string, len(pending))
	for i, c := range pending {
		ids[i] = c.ID
	}
	at := time.Now().UTC().Format(time.RFC3339)
	if err := s.repo.MarkClosetsEmbedded(ctx, teamID, ids, at); err != nil {
		return 0, fmt.Errorf("mark closets embedded: %w", err)
	}
	return len(pending), nil
}
