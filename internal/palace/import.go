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

// AbsorbDrawers files a batch of verbatim drawers from another palace under the
// target tenant as ROWS ONLY — no embedding. The drawer's text and provenance are
// written immediately with embedded_at NULL, and the background embed worker
// builds each vector afterwards. This is the migration's "absorb fast, index
// later" path: it turns a per-batch ollama round-trip (the slow part that tripped
// the CDN timeout) into a plain DB write, so a huge palace upload finishes in
// seconds. IDs are recomputed with the target team's DrawerID recipe, so the same
// record imported twice resolves to one row (idempotent re-runs); SaveUnembedded
// preserves an already-indexed row's embedded_at on conflict.
//
// Records with an empty wing, room, or content are skipped rather than rejected:
// one unaddressable row must not abort a 30k-drawer migration. The returned count
// is how many rows were absorbed, so the caller can report skips as the difference
// from len(in). It deliberately skips Add's chunking and self-dating — an import
// record is already one chunk carrying its own provenance.
func (s *Service) AbsorbDrawers(ctx context.Context, teamID string, in []ImportDrawer) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	drawers := make([]Drawer, 0, len(in))
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
	}
	if len(drawers) == 0 {
		return 0, nil
	}
	if err := s.repo.SaveUnembedded(ctx, drawers); err != nil {
		return 0, fmt.Errorf("absorb drawers: %w", err)
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
// AbsorbClosets is the closet twin of AbsorbDrawers: it writes closet rows only
// (embedded_at NULL) for the background worker to embed later. Same idempotency
// and skip rules.
func (s *Service) AbsorbClosets(ctx context.Context, teamID string, in []ImportCloset) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	closets := make([]Closet, 0, len(in))
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
	}
	if len(closets) == 0 {
		return 0, nil
	}
	if err := s.repo.SaveClosetsUnembedded(ctx, closets); err != nil {
		return 0, fmt.Errorf("absorb closets: %w", err)
	}
	return len(closets), nil
}
