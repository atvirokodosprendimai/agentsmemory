package palace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// This file holds the palace's bulk migration path: filing drawers, closets and
// (via the existing KG/tunnel methods) the rest of a foreign palace VERBATIM
// under the target tenant. It exists for the mempalace → agentsmemory SaaS
// migration: a user exports their local palace and the server re-files it.
//
// Migration differs from Add/Mine in one cardinal way: the records are already
// chunked, dated and provenance-stamped by the SOURCE palace, so import must
// PRESERVE those fields rather than derive new ones. Re-chunking, re-dating, or
// re-extracting would make the migration lossy, which violates the drawer's
// never-summarised rule. The only field recomputed is the id (with the target
// team's recipe) so re-running an import upserts rather than duplicates.

// ImportDrawer is one verbatim memory to import from another palace. A diary
// entry is just a drawer with Room "diary" and Agent/Topic set, so it rides this
// same path rather than a parallel store.
type ImportDrawer struct {
	Wing        string
	Room        string
	SourceFile  string
	ChunkIndex  int
	Content     string   // verbatim, stored exactly as exported
	Entities    []string // proper nouns the source palace already extracted
	FiledAt     string   // source ingestion time (RFC3339); defaults to now if absent
	ContentDate string   // the date the memory is about (optional)
	Agent       string   // diary only: whose journal (lowercased upstream)
	Topic       string   // diary only: grouping tag
}

// ImportDrawers files a batch of verbatim drawers from another palace under the
// target tenant. It mirrors Add's persistence tail — embed every content in one
// batch, then storeDrawers (vectors before rows) — but deliberately skips Add's
// chunking and self-dating: an import record is already one chunk carrying its
// own provenance, so re-deriving those would corrupt the migration. IDs are
// recomputed with the target team's DrawerID recipe, so the same record imported
// twice upserts one drawer (idempotent re-runs) instead of duplicating.
//
// Records with an empty wing, room, or content are skipped rather than rejected:
// one unaddressable row must not abort a 30k-drawer migration. The returned count
// is how many were actually filed, so the caller can report skips as the
// difference from len(in).
func (s *Service) ImportDrawers(ctx context.Context, teamID string, in []ImportDrawer) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	drawers := make([]Drawer, 0, len(in))
	texts := make([]string, 0, len(in))
	for _, r := range in {
		wing := strings.TrimSpace(r.Wing)
		room := strings.TrimSpace(r.Room)
		// Validate emptiness on a trimmed copy, but store the content VERBATIM:
		// the source palace preserved exact bytes and so must the migration.
		if wing == "" || room == "" || strings.TrimSpace(r.Content) == "" {
			continue
		}
		filedAt := strings.TrimSpace(r.FiledAt)
		if filedAt == "" {
			filedAt = now
		}
		drawers = append(drawers, Drawer{
			ID:          DrawerID(teamID, wing, room, r.SourceFile, r.ChunkIndex, r.Content),
			TeamID:      teamID,
			Wing:        wing,
			Room:        room,
			SourceFile:  r.SourceFile,
			ChunkIndex:  r.ChunkIndex,
			Content:     r.Content,
			Entities:    r.Entities,
			FiledAt:     filedAt,
			ContentDate: strings.TrimSpace(r.ContentDate),
			Agent:       strings.TrimSpace(r.Agent),
			Topic:       strings.TrimSpace(r.Topic),
		})
		texts = append(texts, r.Content)
	}
	if len(drawers) == 0 {
		return 0, nil
	}
	// Re-embed with THIS server's model (the migration carries text, not vectors),
	// so every imported drawer is searchable by the same embedder as native writes.
	vectors, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("embed import batch: %w", err)
	}
	if err := s.storeDrawers(ctx, teamID, drawers, vectors); err != nil {
		return 0, err
	}
	return len(drawers), nil
}

// ImportCloset is one packed closet pointer-index document from another palace.
// Closets are derived state (the miner rebuilds them), but the migration carries
// them verbatim so closet-boost search works the instant after import, before any
// re-mine — the source palace already did the topic/quote extraction.
type ImportCloset struct {
	Wing       string
	Room       string
	SourceFile string
	Document   string   // the packed pointer lines, embedded for closet-boost search
	Entities   []string // the closet's top entities
	FiledAt    string
}

// importClosetID content-addresses an imported closet: a hash of its tenant,
// location, source AND document. Unlike the miner's closetID (which keys on a
// per-source sequence number the migration does not know), hashing the document
// makes re-import idempotent regardless of stream order — the same closet always
// maps to the same id, so re-running a migration upserts rather than duplicates.
// Parts are NUL-separated so distinct tuples cannot collide by concatenation.
func importClosetID(teamID, wing, room, source, document string) string {
	h := sha256.New()
	for _, part := range []string{teamID, wing, room, source, document} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ImportClosets files a batch of verbatim closets under the target tenant. It
// embeds each closet document with this server's model and reuses storeClosets
// (vectors into the per-team closet namespace, then rows) — the same persistence
// tail the miner ends in. Blank documents/locations are skipped; the count is how
// many were filed.
func (s *Service) ImportClosets(ctx context.Context, teamID string, in []ImportCloset) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	closets := make([]Closet, 0, len(in))
	texts := make([]string, 0, len(in))
	for _, r := range in {
		doc := strings.TrimSpace(r.Document)
		wing := strings.TrimSpace(r.Wing)
		room := strings.TrimSpace(r.Room)
		if doc == "" || wing == "" || room == "" {
			continue
		}
		filedAt := strings.TrimSpace(r.FiledAt)
		if filedAt == "" {
			filedAt = now
		}
		closets = append(closets, Closet{
			ID:         importClosetID(teamID, wing, room, r.SourceFile, r.Document),
			TeamID:     teamID,
			Wing:       wing,
			Room:       room,
			SourceFile: r.SourceFile,
			Document:   r.Document,
			Entities:   r.Entities,
			FiledAt:    filedAt,
		})
		texts = append(texts, r.Document)
	}
	if len(closets) == 0 {
		return 0, nil
	}
	vectors, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("embed closet import batch: %w", err)
	}
	if err := s.storeClosets(ctx, teamID, closets, vectors); err != nil {
		return 0, err
	}
	return len(closets), nil
}
