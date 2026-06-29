package palace

import (
	"context"
	"fmt"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// This file is the cross-tenant "share a wing" path: snapshot one wing's drawers
// and closets from a source tenant into a destination tenant (e.g. a solo palace
// into a new team/enterprise workspace), reusing the stored vectors so nothing is
// re-embedded. Every record is re-keyed with the destination team's id recipe, so
// the copy is isolated and idempotent — re-running refreshes rather than duplicates.

// pointReader is the slice of the vector store CopyWing needs beyond writing:
// reading stored vectors by id. The source of truth (SQLite) supports it and the
// configured store is always SQLite-backed (directly or behind the Hybrid), so the
// runtime assertion in CopyWing holds; a pure search index would not implement it.
type pointReader interface {
	PointsByIDs(ctx context.Context, namespace string, ids []string) ([]store.Point, error)
}

// CopyResult reports what a wing copy moved.
type CopyResult struct {
	Drawers int // drawers copied (with their vectors)
	Closets int // closets copied
	Skipped int // records whose vector was not yet built in the source, so left behind
}

// copyBatch bounds how many records are read+written per round. It also caps the
// PointsByIDs IN-list so the underlying SQL stays within SQLite's parameter limit.
const copyBatch = 500

// CopyWing snapshots one wing from fromTeam into toTeam, reusing the stored
// vectors (no re-embedding). Drawers and closets are copied; KG facts (team-global,
// not wing-scoped) and tunnels (cross-wing) are NOT, and the destination's derived
// graph (hallways + entity tunnels) is rebuilt at the end. The wing keeps its name.
//
// Idempotent: ids are re-keyed with toTeam's recipe, so re-running upserts rather
// than duplicates. A drawer/closet whose vector is not yet built in the source
// (still pending background embedding) is skipped and counted — re-run once the
// source finishes indexing.
func (s *Service) CopyWing(ctx context.Context, fromTeam, toTeam, wing string) (CopyResult, error) {
	var res CopyResult
	if fromTeam == "" || toTeam == "" || wing == "" {
		return res, fmt.Errorf("%w: fromTeam, toTeam and wing are required", ErrInvalidInput)
	}
	if fromTeam == toTeam {
		return res, fmt.Errorf("%w: source and destination teams must differ", ErrInvalidInput)
	}
	reader, ok := s.vectors.(pointReader)
	if !ok {
		return res, fmt.Errorf("vector backend does not support copy (no PointsByIDs)")
	}

	// --- drawers: page the source wing, copy vectors+rows re-keyed for the dest ---
	for offset := 0; ; offset += copyBatch {
		src, err := s.repo.List(ctx, fromTeam, wing, "", copyBatch, offset)
		if err != nil {
			return res, fmt.Errorf("list source drawers: %w", err)
		}
		if len(src) == 0 {
			break
		}
		n, skipped, err := s.copyDrawerBatch(ctx, reader, fromTeam, toTeam, src)
		if err != nil {
			return res, err
		}
		res.Drawers += n
		res.Skipped += skipped
		if len(src) < copyBatch {
			break
		}
	}

	// --- closets: copy the wing's pointer index, chunked to bound the IN-list ---
	closets, err := s.repo.ClosetsByWing(ctx, fromTeam, wing)
	if err != nil {
		return res, fmt.Errorf("list source closets: %w", err)
	}
	for start := 0; start < len(closets); start += copyBatch {
		end := start + copyBatch
		if end > len(closets) {
			end = len(closets)
		}
		n, skipped, err := s.copyClosetBatch(ctx, reader, fromTeam, toTeam, closets[start:end])
		if err != nil {
			return res, err
		}
		res.Closets += n
		res.Skipped += skipped
	}

	// Rebuild the destination's derived graph from its now-larger drawer set.
	if _, err := s.RecomputeGraph(ctx, toTeam, "", false); err != nil {
		return res, fmt.Errorf("recompute dest graph: %w", err)
	}
	return res, nil
}

// copyDrawerBatch re-keys one batch of source drawers for the destination, pairs
// each with its source vector, and writes both. Returns (copied, skipped).
func (s *Service) copyDrawerBatch(ctx context.Context, reader pointReader, fromTeam, toTeam string, src []Drawer) (int, int, error) {
	ids := make([]string, len(src))
	for i, d := range src {
		ids[i] = d.ID
	}
	vecByID, err := vectorsByID(ctx, reader, fromTeam, ids)
	if err != nil {
		return 0, 0, fmt.Errorf("read source vectors: %w", err)
	}
	dstDrawers := make([]Drawer, 0, len(src))
	dstVectors := make([][]float32, 0, len(src))
	skipped := 0
	for _, d := range src {
		vec := vecByID[d.ID]
		if len(vec) == 0 {
			skipped++ // vector not built in the source yet — nothing to copy
			continue
		}
		dstDrawers = append(dstDrawers, Drawer{
			ID:          DrawerID(toTeam, d.Wing, d.Room, d.SourceFile, d.ChunkIndex, d.Content),
			TeamID:      toTeam,
			Wing:        d.Wing,
			Room:        d.Room,
			SourceFile:  d.SourceFile,
			ChunkIndex:  d.ChunkIndex,
			Content:     d.Content,
			Entities:    d.Entities,
			FiledAt:     d.FiledAt,
			ContentDate: d.ContentDate,
			Agent:       d.Agent,
			Topic:       d.Topic,
			// ParentID is intentionally dropped: it points at a source drawer id that
			// is re-keyed in the destination, so the linkage would dangle. The chunks
			// are still copied as independent, searchable drawers.
		})
		dstVectors = append(dstVectors, vec)
	}
	if len(dstDrawers) == 0 {
		return 0, skipped, nil
	}
	if err := s.storeDrawers(ctx, toTeam, dstDrawers, dstVectors); err != nil {
		return 0, 0, fmt.Errorf("write dest drawers: %w", err)
	}
	return len(dstDrawers), skipped, nil
}

// copyClosetBatch is the closet twin of copyDrawerBatch.
func (s *Service) copyClosetBatch(ctx context.Context, reader pointReader, fromTeam, toTeam string, src []Closet) (int, int, error) {
	ids := make([]string, len(src))
	for i, c := range src {
		ids[i] = c.ID
	}
	vecByID, err := vectorsByID(ctx, reader, closetNamespace(fromTeam), ids)
	if err != nil {
		return 0, 0, fmt.Errorf("read source closet vectors: %w", err)
	}
	dstClosets := make([]Closet, 0, len(src))
	dstVectors := make([][]float32, 0, len(src))
	skipped := 0
	for _, c := range src {
		vec := vecByID[c.ID]
		if len(vec) == 0 {
			skipped++
			continue
		}
		dstClosets = append(dstClosets, Closet{
			ID:         importClosetID(toTeam, c.Wing, c.Room, c.SourceFile, c.Document),
			TeamID:     toTeam,
			Wing:       c.Wing,
			Room:       c.Room,
			SourceFile: c.SourceFile,
			Document:   c.Document,
			Entities:   c.Entities,
			FiledAt:    c.FiledAt,
		})
		dstVectors = append(dstVectors, vec)
	}
	if len(dstClosets) == 0 {
		return 0, skipped, nil
	}
	if err := s.storeClosets(ctx, toTeam, dstClosets, dstVectors); err != nil {
		return 0, 0, fmt.Errorf("write dest closets: %w", err)
	}
	return len(dstClosets), skipped, nil
}

// vectorsByID reads the given ids' vectors from a namespace into an id->vector map.
func vectorsByID(ctx context.Context, reader pointReader, namespace string, ids []string) (map[string][]float32, error) {
	pts, err := reader.PointsByIDs(ctx, namespace, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]float32, len(pts))
	for _, p := range pts {
		out[p.ID] = p.Vector
	}
	return out, nil
}
