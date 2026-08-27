// Package palace holds the core memory-palace domain, ported faithfully from
// the frozen Python mempalace. The metaphor is the data model: a Wing is a
// project, a Room is an aspect inside it, a Drawer is one verbatim memory, and
// Hallways/Tunnels are the links that make the palace navigable.
//
// This file defines the domain types and invariants only. Mining (text ->
// drawers) and hybrid search (vector + BM25 + closet boost) are deliberately
// not implemented in the skeleton — they are the next phase — but the types are
// pinned now so every later package depends on a stable vocabulary, and every
// type carries a tenant (TeamID) because storage is tenant-isolated.
package palace

import "strings"

// Drawer is the atomic memory unit: a single VERBATIM text chunk plus locating
// metadata. The cardinal rule from the Python tool carries over — a drawer is
// never a summary; the exact source text is preserved so recall is lossless.
type Drawer struct {
	// ID is the drawer's OPAQUE name. It is minted once and never recomputed,
	// never compared to a hash, and never used to infer anything about the row's
	// content — it exists so that anchors, tunnels, kg_triples.source_drawer_id,
	// parent_id, search_events and the vector store have something stable to point
	// at (ADR-038).
	//
	// It previously read "a deterministic hash of (team, wing, room, source,
	// chunkIndex)", which was wrong twice over: the recipe also hashed CONTENT,
	// and three shipped paths mutate those fields in place while keeping the id.
	// What that sentence described is now ContentKey.
	ID string

	// TeamID is the owning tenant; it selects the Qdrant collection.
	TeamID string

	Wing       string // project namespace
	Room       string // aspect within the wing
	SourceFile string // provenance of the chunk
	ChunkIndex int    // position within the source file
	Content    string // verbatim text — the memory itself

	// Entities are the proper nouns extracted from Content; their co-occurrence
	// within a wing is what materialises Hallways.
	Entities []string

	// FiledAt is the RFC3339 ingestion time; ContentDate is the date the memory
	// is *about*, extracted from filename/frontmatter/body/mtime.
	FiledAt     string
	ContentDate string

	// ParentID links the chunks of one oversized add_drawer back to the first
	// chunk, so a multi-chunk write can be recognised as a single logical memory.
	// Empty for single-chunk drawers.
	ParentID string

	// ContentKey is the hash dedup matches on: DrawerID over this row's own
	// fields (ADR-038, migration 00031). It is what ID used to be, moved to a
	// column of its own so the id can stay put while the content changes.
	//
	// EMPTY for diary rows, because a journal is append-only and two identical
	// reflections are two entries — the unique index's `content_key != ''`
	// conjunct is what keeps them out of dedup.
	ContentKey string

	// ValidTo, SupersededBy, EndedReason and EndedAt are the validity window
	// (ADR-038, migration 00030). A drawer is CURRENT while ValidTo is empty,
	// exactly as a knowledge-graph fact already is — ending a memory never
	// deletes it, so the record of why a decision changed survives the change.
	//
	// EndedReason is required at every ending and is the point of the window: an
	// invalidation that records only THAT something ended destroys the only thing
	// worth keeping about it. SupersededBy names the successor when a correction
	// replaces this record, and is empty for a retraction that replaces nothing.
	ValidTo      string
	SupersededBy string
	EndedReason  string
	EndedAt      string

	// Agent and Topic carry the two extra fields a diary entry needs and a normal
	// drawer leaves empty (migration 00007). Agent is whose journal the entry
	// belongs to — stored lowercased so diary_read is case-insensitive, matching
	// the frozen Python contract (#1243) — and is what diary_read scopes by; Topic
	// is a free tag grouping entries (defaulting to "general"). Keeping them as
	// columns on the same drawer keeps diary on the identical chunk/embed/store
	// machinery as add_drawer rather than forking a parallel store.
	Agent string
	Topic string
	// HasEdge and EdgeDerived report whether this drawer is reachable by
	// traversal and, if so, whether the server inferred the edge or a writer
	// authored it. They are not persisted: they describe what the filing just
	// did, so a caller learns it without a second query.
	HasEdge     bool `json:"has_edge,omitempty"`
	EdgeDerived bool `json:"edge_derived,omitempty"`

	// Supersedes and SupersededReason name the record THIS one replaced, and why.
	// They are not persisted either — the predecessor row holds the truth in its
	// SupersededBy and EndedReason, and these are resolved onto the live record
	// when it is read (ADR-038 T5).
	//
	// They ride the DEFAULT path deliberately. ADR-010 first put history behind a
	// flag and then corrected itself: hiding it AND expecting retractions to stop
	// re-litigation cannot both hold, because a session about to redo a rejected
	// thing does not know to ask for history. So the current record carries what
	// it replaced, and SupersededReason is capped at maxCarriedReasonRunes — the
	// full text stays on the predecessor, reachable by the history route.
	Supersedes       string `json:"supersedes,omitempty"`
	SupersededReason string `json:"superseded_reason,omitempty"`
}

// Dynamics are the L7 "living connection" fields every hallway and tunnel carries:
// a Hebbian Strength that would grow on co-access, a Stability that resists decay,
// and the bookkeeping for that evolution. They are stored for wire-shape parity
// with the frozen tools; the potentiation/decay that would move them off their
// defaults is a later phase, so for now they stay at their initialized values.
type Dynamics struct {
	Strength      float64 `json:"strength"`
	Stability     float64 `json:"stability"`
	LastActivated string  `json:"last_activated"`
	AccessCount   int     `json:"access_count"`
}

// Hallway is a within-wing link between two entities that co-occur in drawers.
// It is derived (recomputed from drawers), never authored, and unordered: A↔B
// and B↔A are the same hallway, so endpoints are stored sorted for a stable id.
type Hallway struct {
	ID           string
	TeamID       string
	Wing         string
	EntityA      string
	EntityB      string
	CoOccurrence int      // how many drawers mention both
	Rooms        []string // rooms where they met
	Label        string
	CreatedAt    string
	CreatedBy    string // "auto" for derived hallways
	Dynamics
}

// TunnelKind distinguishes a human-authored cross-wing link from one the miner
// generated automatically from a shared topic.
type TunnelKind string

const (
	// TunnelExplicit is a user-created link between two wings/rooms.
	TunnelExplicit TunnelKind = "explicit"
	// TunnelEntity is auto-generated when an entity has hallways in two wings.
	TunnelEntity TunnelKind = "entity"
)

// Endpoint is one side of a Tunnel: a location in the palace, optionally pinned
// to a specific drawer.
type Endpoint struct {
	Wing     string
	Room     string
	DrawerID string // optional
}

// Tunnel links two locations across wings. Explicit tunnels are validated
// against existing rooms; entity tunnels are synthesised from hallways. A tunnel
// is symmetric — its id is a hash of its sorted endpoints — so creating A↔B and
// B↔A resolves to one record.
type Tunnel struct {
	ID        string
	TeamID    string
	Source    Endpoint
	Target    Endpoint
	Label     string
	Kind      TunnelKind
	CreatedAt string
	UpdatedAt string
	Dynamics
}

// SearchResult is one page of recall together with the identity it was recorded
// under.
//
// SearchID is the same value `search_events` holds as that row's primary key,
// so a page and its durable record join on it with no extra state anywhere. It
// is present even when Hits is empty: a recall that found nothing still ran,
// still wrote a row, and is the page an operator most often wants to trace.
type SearchResult struct {
	SearchID string
	Hits     []SearchHit
	// Facts is the fact block ADR-036 adds BESIDE the hits — in-wing facts, the
	// sibling wings holding matches this recall did not return, and a count of
	// the matches it could not place at all. It never reorders Hits: F-9 pins
	// that, so this cannot be confused with a retrieval change.
	Facts FactBlock
}

// SearchHit is one ranked result from hybrid search. Score is the fused rank — a
// convex blend of vector similarity and lexical BM25, as the Python searcher did
// (closet boost joins once mining builds closets). BM25 is the raw lexical score
// that fed the blend, surfaced for transparency; Distance is the raw cosine
// distance from the query.
type SearchHit struct {
	Drawer Drawer
	// MemoryID is the stable identity shared by the root and every child chunk.
	// Drawer.ID remains the best matching passage for compatibility; callers that
	// reason about, fetch, or annotate the whole memory use this field instead.
	MemoryID string
	// MemoryContent is the whole logical memory, reconstructed from its stored
	// chunks. Ranking uses it only in the memory-level A/B arm, while the MCP wire
	// uses it in both arms so snippets, regions, identity, and staleness all describe
	// the same unit.
	MemoryContent string
	// ChunksMatched is how many chunks of this memory were in the ranked pool: 1
	// for a memory that was never split, N when N of its chunks matched. It exists
	// because collapsing a page to one hit per memory would otherwise destroy the
	// signal — a memory that matched in four places is stronger evidence than one
	// that matched in one, and a silent collapse throws that away.
	ChunksMatched int
	// StaleIndex says the index that served this recall was behind its source of
	// truth (ADR-033): the hits come from the SoT's own vector path, not the
	// search index, and an async rebuild is in flight. It rides on every hit of
	// a degraded recall, so an agent that reads a sentence from a stale recall
	// can tell it happened instead of mistaking it for fresh knowledge.
	StaleIndex bool
	// Corrections are the retracts/supersedes/qualifies edges pointing AT this
	// record, resolved server-side. Marked, never hidden and never demoted: a
	// retraction can itself be wrong, so this is a signal for the reader rather
	// than a gate on what the reader may see.
	Corrections []Correction `json:"corrections,omitempty"`
	Score       float64      // fused rank score, higher is better
	BM25        float64      // raw Okapi-BM25 lexical score (pre-normalization)
	ClosetBoost float64      // closet rank boost folded into Score (0 when none)
	Distance    float64      // raw cosine distance, lower is closer
	// RerankScore is the cross-encoder's relevance for this hit, or 0 when no
	// reranker is configured or it did not score this one. It is reported
	// alongside Score rather than replacing it: the two are not on the same scale
	// (a cross-encoder logit against a fused [0,1] score), and the final order is
	// a blend of both, so an agent reading results can see which signal moved a
	// hit and by how much.
	RerankScore float64
	// Reranked says whether a cross-encoder actually scored this hit. It exists
	// because RerankScore's zero is ambiguous: TEI is asked for sigmoid scores
	// in (0,1), but llama.cpp's server returns bare logits, where zero is a
	// perfectly ordinary value. Anything deciding whether a score is PRESENT —
	// an abstention gate, or the eval calibrating one — must read this.
	Reranked bool
	// Blended is the value the page was ORDERED by: BlendRerank's weighted
	// combination of the pool-normalised fused and rerank scores. It is not a
	// third opinion to weigh against the other two — it is the one the sort used.
	//
	// Pool-relative by construction, so it compares hits WITHIN a page and means
	// nothing across pages. Zero when this hit was not reranked, for the same
	// reason RerankScore is: a hit outside the scored pool was ordered by the
	// fused score alone and has no blend.
	Blended float64
}

// memoryOf returns the id of the MEMORY a drawer belongs to: its parent when it
// is a chunk of a larger one, otherwise itself.
//
// One definition, because there were two. The eval folded hits onto ParentID in
// two places before scoring, so it measured memories, while Search returned
// chunks — and a page of ten could hold as few as six distinct memories while the
// eval reported the gold at rank 1. An eval cannot report a regression it does
// not measure the unit of, and the unit was written down twice in the harness and
// nowhere in the pipeline.
func memoryOf(d Drawer) string {
	if d.ParentID != "" {
		return d.ParentID
	}
	return d.ID
}

// ValidSearchID reports whether sid has the shape randomID mints: lowercase hex,
// or the clock fallback "t" followed by digits. It is a shape check, not a
// lookup — an id for a search that never happened is a client bug worth seeing
// on a span, whereas an arbitrary string is a leak worth refusing.
//
// It lives HERE, beside the minter, rather than at the consumer that validates
// incoming ids. When the two were apart, nothing tied them together: a review on
// 2026-08-26 mutated randomID to emit UPPERCASE hex, and every package stayed
// green while every freshly minted id would have been rejected — and since a
// rejected id is not counted as adoption, ADR-028's deferral trigger would have
// read "no client ever sent one" at the exact moment every client was sending
// one. TestEveryMintedSearchIDIsAcceptedByItsOwnValidator now fails on that.
//
// The hex length is a RANGE rather than the 24 randomID currently emits, because
// the two ways to be wrong are not symmetric: too loose lets an odd id through,
// too tight silently rejects every real one the moment that length changes.
func ValidSearchID(sid string) bool {
	if rest, ok := strings.CutPrefix(sid, "t"); ok && rest != "" && isDigits(rest) {
		return len(sid) <= 32
	}
	if len(sid) < 16 || len(sid) > 32 {
		return false
	}
	for _, r := range sid {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// isDigits reports whether s is non-empty and all ASCII digits.
func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
