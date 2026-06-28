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

// Drawer is the atomic memory unit: a single VERBATIM text chunk plus locating
// metadata. The cardinal rule from the Python tool carries over — a drawer is
// never a summary; the exact source text is preserved so recall is lossless.
type Drawer struct {
	// ID is a deterministic hash of (team, wing, room, source, chunkIndex) so
	// re-mining the same source is idempotent rather than duplicative.
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

	// Agent and Topic carry the two extra fields a diary entry needs and a normal
	// drawer leaves empty (migration 00007). Agent is whose journal the entry
	// belongs to — stored lowercased so diary_read is case-insensitive, matching
	// the frozen Python contract (#1243) — and is what diary_read scopes by; Topic
	// is a free tag grouping entries (defaulting to "general"). Keeping them as
	// columns on the same drawer keeps diary on the identical chunk/embed/store
	// machinery as add_drawer rather than forking a parallel store.
	Agent string
	Topic string
}

// Hallway is a within-wing link between two entities that co-occur in drawers.
// It is derived (recomputed from drawers), never authored, and unordered: A↔B
// and B↔A are the same hallway, so endpoints are stored sorted for a stable id.
type Hallway struct {
	ID              string
	TeamID          string
	Wing            string
	EntityA         string
	EntityB         string
	CoOccurrence    int      // how many drawers mention both
	Rooms           []string // rooms where they met
	Label           string
}

// TunnelKind distinguishes a human-authored cross-wing link from one the miner
// generated automatically from a shared topic.
type TunnelKind string

const (
	// TunnelExplicit is a user-created link between two wings/rooms.
	TunnelExplicit TunnelKind = "explicit"
	// TunnelTopic is auto-generated when two wings share a topic label.
	TunnelTopic TunnelKind = "topic"
)

// Endpoint is one side of a Tunnel: a location in the palace, optionally pinned
// to a specific drawer.
type Endpoint struct {
	Wing     string
	Room     string
	DrawerID string // optional
}

// Tunnel links two locations across wings. Explicit tunnels are validated
// against existing rooms; topic tunnels are synthesised at mine time.
type Tunnel struct {
	ID     string
	TeamID string
	Source Endpoint
	Target Endpoint
	Label  string
	Kind   TunnelKind
}

// SearchHit is one ranked result from hybrid search. Score is the fused rank — a
// convex blend of vector similarity and lexical BM25, as the Python searcher did
// (closet boost joins once mining builds closets). BM25 is the raw lexical score
// that fed the blend, surfaced for transparency; Distance is the raw cosine
// distance from the query.
type SearchHit struct {
	Drawer      Drawer
	Score       float64 // fused rank score, higher is better
	BM25        float64 // raw Okapi-BM25 lexical score (pre-normalization)
	ClosetBoost float64 // closet rank boost folded into Score (0 when none)
	Distance    float64 // raw cosine distance, lower is closer
}
