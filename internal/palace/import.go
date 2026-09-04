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
	keys := make([]string, 0, len(in))
	for _, r := range in {
		wing, room := strings.TrimSpace(r.Wing), strings.TrimSpace(r.Room)
		keys = append(keys, contentKeyOf(teamID, wing, room, r.SourceFile, r.ChunkIndex, r.Content))
	}
	// import.go:21's contract — "re-running an import upserts rather than
	// duplicates" — now rests on the content key rather than on a derived id, so
	// the ids must be reused for the same reason Add reuses them.
	existing, err := s.writer.IDsByContentKeys(ctx, teamID, keys)
	if err != nil {
		return 0, fmt.Errorf("look up rows already holding these content keys: %w", err)
	}
	for i, r := range in {
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
		// The source's entities are replayed verbatim when it has them — this is a
		// migration, and re-deriving would overwrite another palace's extraction
		// with this build's. When it has NONE, derive: an export from a palace
		// predating ADR-016 carries no entities at all, and absorbing it filed
		// every memory permanently outside the derived graph, because
		// RecomputeGraph reads this column and never re-extracts. Deriving only
		// into the gap keeps the verbatim contract for every record that has one.
		entities := r.Entities
		if len(entities) == 0 {
			entities = extractEntities(r.Content)
		}
		drawers = append(drawers, Drawer{
			ID:          mintOrReuse(existing, keys[i]),
			ContentKey:  keys[i],
			TeamID:      teamID,
			Wing:        wing,
			Room:        room,
			SourceFile:  r.SourceFile,
			ChunkIndex:  r.ChunkIndex,
			Content:     r.Content,
			Entities:    entities,
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

	// Imported drawers get the same containment edge every other write path
	// attaches. Without this an entire imported dataset is filed and unreachable
	// by traversal — the exact orphan state ADR-036 T6 exists to end, arriving
	// through a path T6 never looked at because ADR-035 landed after it was
	// written.
	//
	// One edge per SOURCE, not per row: an import is many drawers from one file,
	// and edging each row would put thousands of derived triples in the graph to
	// express one fact about where the dataset lives.
	s.attachDerivedEdgeTo(ctx, teamID, drawers)
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
