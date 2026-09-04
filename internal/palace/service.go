package palace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
)

// Sentinel errors the MCP layer maps to tool-level results. Keeping them here
// lets the transport stay ignorant of gorm: the service is the only place that
// knows the persistence library.
var (
	// ErrNotFound is returned when a drawer id does not exist for the team.
	ErrNotFound = errors.New("drawer not found")
	// ErrFactNotFound is the knowledge graph's equivalent: no CURRENT triple
	// matches. A separate sentinel rather than a reuse of ErrNotFound, because that
	// one's text says "drawer" and %w puts it in front — so the KG path rendered
	// "drawer not found: no CURRENT fact matches …", contradicting itself in one
	// line. Callers that only care about the class check both; nothing here does.
	ErrFactNotFound = errors.New("fact not found")
	// ErrConcurrentCorrection is returned when a second correction of the same
	// memory reaches the compare-and-swap after the first has ended its chunks.
	// It is a REFUSAL, not a failure: the loser changed nothing, and the caller's
	// correction should be re-read and reapplied against the record that won.
	ErrConcurrentCorrection = errors.New("concurrent correction")
	// ErrSourceDrawerNotFound reports a fact whose source_drawer_id names no row in
	// this team — provenance that resolves to nothing. Distinct from
	// ErrFactNotFound because the caller's mistake is different: the fact is fine
	// and the citation is wrong, usually a shortened or retyped id.
	ErrSourceDrawerNotFound = errors.New("source drawer not found")
	// ErrInvalidInput is returned when a required argument is missing or empty.
	ErrInvalidInput = errors.New("invalid input")
)

// Defaults and bounds for search/recall, mirroring the frozen Python contract so
// the tool surface behaves identically (search: limit 1-100 def5, max_distance
// 0-2 def1.5; check_duplicate: threshold def0.9).
const (
	DefaultSearchLimit  = 5
	MaxSearchLimit      = 100
	DefaultMaxDistance  = 1.5
	DefaultDupThreshold = 0.9

	// DefaultRerankPool is how many fused candidates a configured cross-encoder
	// scores. Widening the pool is the point of reranking: hybridCandidateMultiplier
	// alone shows the ranker only limit*3 candidates (15 for a default search), so a
	// document the vector pass ranked 40th can never reach the page no matter how
	// well it answers the query. 50 is wide enough to change the answer and small
	// enough to cross-encode within a search's latency budget.
	// Lowered from 50 on 2026-08-21. The cost is linear and measured — ~435ms per
	// document on a CPU cross-encoder, so 50 candidates cost ~22 seconds and made
	// am_search unusable: an independent session's searches timed out 3 times out
	// of 3 while am_status answered instantly. What a larger pool BUYS is still
	// unmeasured at any size, so this is a cost-driven choice and not a quality
	// one; an operator on faster hardware should raise it, and --rerank-pool is how.
	DefaultRerankPool = 10

	// DefaultRerankWeight is how much of the final ordering the cross-encoder
	// decides, with the rest left to the hybrid score it refines.
	//
	// It is a BLEND rather than a handover, and that is a measured choice. Letting
	// the cross-encoder's score decide alone throws away the lexical evidence in
	// the fused score: on a 12-question eval of this palace, ordering purely by
	// cross-encoder scored MRR 0.686 where the fused order scored 1.000, on the
	// queries that carry an identifier or a flag — exactly the searches a
	// developer actually types. A sweep put 0.25 and 0.50 joint-best (0.958).
	DefaultRerankWeight = 0.5
)

// Diary defaults, mirroring the frozen Python diary tools so the journal behaves
// identically: every entry is filed into the "diary" room, an untagged entry gets
// the "general" topic, and diary_read returns the last 10 entries by default and
// at most 100.
const (
	// DiaryRoom is the room every diary entry lives in; diary_read scopes by it
	// together with the agent, cleanly separating journal entries from memories.
	DiaryRoom = "diary"
	// DefaultDiaryTopic tags a diary entry written without an explicit topic.
	DefaultDiaryTopic = "general"
	// DefaultDiaryReadN is diary_read's window when last_n is unset.
	DefaultDiaryReadN = 10
	// MaxDiaryReadN caps diary_read's window so one call cannot scan unbounded.
	MaxDiaryReadN = 100

	// diaryTimeLayout stamps a diary entry's FiledAt with a FIXED-WIDTH, nine-digit
	// nanosecond fraction. diary_read orders by filed_at as a string (SQLite TEXT),
	// so the format must be lexicographically sortable: time.RFC3339Nano trims
	// trailing zeros, making its width vary and a string sort disagree with chrono
	// order. A zero-padded fraction keeps string order == time order, and the
	// nanosecond resolution also makes each entry's id-seed unique.
	diaryTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"
)

// AAAKSpec is the compressed memory dialect agents use for diary and closet
// lines. It is static reference text (the get_aaak_spec tool returns it verbatim)
// so it lives as a constant rather than in storage.
const AAAKSpec = `AAAK is a compressed memory dialect MemPalace uses for efficient, human- and LLM-readable storage.

FORMAT:
  ENTITIES: 3-letter uppercase codes (ALC=Alice, JOR=Jordan).
  EMOTIONS: *markers* before text (*warm*=joy, *fierce*=determined, *raw*=vulnerable, *bloom*=tenderness).
  STRUCTURE: pipe-separated fields. FAM: family | PROJ: projects | ⚠: warnings.
  DATES: ISO (2026-03-31). COUNTS: Nx = N mentions. IMPORTANCE: ★ to ★★★★★.

Read AAAK naturally — expand codes mentally, treat *markers* as emotional context.
When writing AAAK: use entity codes, mark emotions, keep structure tight.`

// Embedder turns text into vectors. It is declared at the consumer (per Go's
// "accept interfaces" guidance) so the service depends on the capability, not on
// the concrete Ollama client — which also makes it trivial to fake in tests.
type Embedder interface {
	// Embed returns one vector per input string, in order.
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	// EmbedOne is the single-string convenience used by search and check_duplicate.
	EmbedOne(ctx context.Context, input string) ([]float32, error)
}

// EmbedDescriber is an OPTIONAL interface an Embedder may implement to name
// itself on a span.
//
// Optional rather than folded into Embedder because a test fake has nothing
// useful to say here and should not be forced to invent it. The served backends
// implement it; anything that does not simply reports no backend, which reads on
// a trace as "unknown" instead of as a lie.
//
// It exists because a distance means nothing without the model that produced it.
// A trace showing am.dim=1024 is satisfied by bge-m3 through Ollama, bge-m3
// through TEI, and any other 1024-dimension model an operator pointed
// OLLAMA_EMBED_MODEL at — three different embedding spaces, one attribute, and
// every cosine distance in the tree silently incomparable across them.
type EmbedDescriber interface {
	// DescribeEmbedder returns the backend name, the model, and the model's input
	// window in TOKENS when the backend can report it (0 when it cannot). A zero
	// window is honest: nothing in this repository could previously state one at
	// all, and a guessed number would be worse than an absent one.
	DescribeEmbedder() (backend, model string, windowTokens int)
}

// VectorDescriber is an OPTIONAL interface a store.VectorStore may implement to
// name the backend serving a recall.
//
// It decides pages in the most direct way there is — it IS the index the query
// runs against — and until now the trace could not say which one answered. That
// matters most exactly when it is hardest to reason about: the source of truth
// and the search index are different stores, and a recall served by a behind
// index looks, in every other attribute, identical to one served by a healthy
// one.
type VectorDescriber interface {
	// DescribeVectorStore names the backend, e.g. "sqlitevec", "qdrant", or
	// "hybrid(sqlitevec->qdrant)" when a source of truth and an index are paired.
	DescribeVectorStore() string
}

// VectorBackendName reports the backend serving recalls, or "" when the store
// cannot name itself.
func (s *Service) VectorBackendName() string {
	d, ok := s.vectors.(VectorDescriber)
	if !ok {
		return ""
	}
	return d.DescribeVectorStore()
}

// rerankBudgetExceeded is the signal a Reranker raises when ITS OWN budget was
// the binding constraint — as opposed to an unreachable endpoint, an inherited
// deadline, or any other failure that happens to consume the same wall clock.
//
// Declared here, at the consumer, like Embedder and Reranker themselves: the
// service depends on the capability rather than on the TEI client that currently
// provides it. It exists because the shape of the error cannot answer the
// question — a DNS stall and a slow cross-encoder produce the same
// timeout-bearing *url.Error, and only the producer knows which one it was.
type rerankBudgetExceeded interface {
	RerankBudgetExceeded() bool
}

// RerankDescriber is an OPTIONAL interface a Reranker may implement to report
// the budget it enforces on itself.
//
// The budget decides pages, which is why the service wants it: when a rerank
// call overruns, applyRerankWith fails open and serves the FUSED order instead
// of the cross-encoder's. The service cannot see that budget otherwise — it
// holds a Reranker, and the duration lives inside the client that was handed
// one at construction. Measured 2026-08-26 on a CPU cross-encoder: 44 of 60
// rerank calls at pool 20 ran longer than the 10s the deployed stack ships,
// so on that hardware the budget is not a safety net, it is the thing deciding
// the ranking.
type RerankDescriber interface {
	// RerankBudget returns the ceiling on a complete rerank call, or 0 when the
	// reranker enforces none. Zero is honest rather than a guess: a reranker that
	// cannot state a budget must not have one invented for it.
	RerankBudget() time.Duration
}

// Reranker scores candidate documents against a query with a cross-encoder,
// returning one score per document IN INPUT ORDER (higher is better). Like
// Embedder it is declared at the consumer, so the service depends on the
// capability rather than on the TEI client that currently provides it.
//
// A cross-encoder reads the query and the document together, which is strictly
// more evidence than the vector+BM25 blend that selects the candidates — but it
// is also far more expensive, which is why it only ever sees a shortlist.
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}

// Service is the core memory loop: it files drawers (chunk -> embed -> store) and
// recalls them (embed query -> nearest-neighbour -> join metadata). It composes
// the metadata Repo, an Embedder, and the vector store seam; everything is
// tenant-scoped by the teamID argument, which is also the vector namespace.
type Service struct {
	repo *Repo
	// writer is the WRITE PATH's view of the repository: the same Repo with the
	// writer handle in both seats, so a lookup taken through it is a read the
	// writer itself made. ADR-052's Decision requires that — "the write path
	// validates against its own reads, never against the read model" — because
	// a lookup on the pooled reader is a different snapshot, and a check taken
	// there is not binding on the write that follows. Review of PR #233 found
	// that shape twice; TestNoWritePathReadsTheReadModel now lists the class.
	// Anything that selects the rows a write will touch, or the values it will
	// write, reads through this field; repo stays the read model's view.
	writer  *Repo
	embed   Embedder
	vectors store.VectorStore
	dim     int // embedding dimension new namespaces are created with (bge-m3 = 1024)
	// rerank, when non-nil, cross-encodes the top rerankPool fused candidates and
	// reorders them before Search pages. nil is the default and means recall stops
	// at the vector+BM25+closet fusion — the behaviour every deployment had before
	// a reranker endpoint was configurable.
	rerank       Reranker
	rerankPool   int
	rerankWeight float64
	// rerankNorm names how a raw cross-encoder score is brought onto a scale
	// comparable with the fused score. Empty resolves to DefaultRerankNorm, which
	// has been SIGMOID since 2026-08-25 — it is not an inert zero value, and
	// reading it as one is not hypothetical: serviceForArm skipped resetting this
	// field on the strength of an earlier version of this comment promising
	// min-max, which silently made the eval's min-max control a second sigmoid
	// arm. Anything wanting min-max must ask for it by name.
	rerankNorm string
	// bm25Auto scales the lexical fusion weight per query by its measured lexical
	// signal; bm25Base is the ceiling. See config.BM25Weight for the evidence.
	bm25Auto bool
	bm25Base float64
	// bm25IDF weights each query term by how much it discriminates instead of
	// counting it once, when bm25Auto is on.
	//
	// It is reachable from configuration rather than eval-only because a measured
	// arm nobody can run is not a finding: four tables across two unrelated
	// corpora put it ahead of the binary count (0.377 vs 0.257, 0.370 vs 0.290,
	// 0.246 vs 0.183, 0.726 vs 0.673), and every one of them measured a code path
	// production could not select. The default stays binary until the maintainer
	// of the second corpus has seen the case for moving it.
	bm25IDF bool
	// fusionRRF makes search fuse vector and lexical evidence by RANK
	// (reciprocal-rank fusion) instead of by weighted score. It exists because a
	// linear blend lets one bad signal drag a good candidate down: on a large,
	// diverse corpus BM25 measured WORSE than vector alone (MRR 0.178 vs 0.335),
	// and the linear fusion carried that damage into the page — and worse, into
	// the cross-encoder's pool, which is taken off the fused head, so the one
	// component that did work never saw the candidates fusion had buried. RRF
	// bounds any single signal's influence to a rank position, and on that same
	// corpus rrf+rerank was the best arm of seventeen.
	//
	// Off by default: the linear blend is best on the corpora we have measured
	// where lexical evidence helps, and a ranking default changes what every
	// existing palace returns. FUSION=rrf turns it on for the corpora where the
	// eval says it should be.
	fusionRRF bool
	// memoryEvidenceSelector chooses the bounded document the optional
	// cross-encoder receives for a reassembled memory. The lexical control uses
	// literal query coverage; the semantic arm embeds windows at query time.
	// Ranking itself is always per logical memory; this selector is the nested
	// A/B that remains after the chunk-ranked unit was retired.
	memoryEvidenceSelector string

	// lexNorm is how the raw BM25 scores are normalised before fusion, and
	// lexNormName is the operator-facing spelling of it.
	//
	// Three transforms were built, tested and compared in the eval — page-max,
	// ceiling and saturating — and for a long time production could select none of
	// them: Search called the page-max wrappers and there was no config key, no
	// flag and no setter. They were reachable from a table and from nothing an
	// operator runs, so an eval could report the best arm and leave no way to
	// deploy it.
	//
	// The DEFAULT is unchanged. Which normaliser should win is an evidence
	// question (ADR-002 T3); being able to choose one is not, and shipping the
	// choice first means the answer is a changed default rather than a build.
	lexNorm     lexNorm
	lexNormName string

	// closetBoostScale scales every closet rank boost: 1 is the full curation
	// prior, 0 turns closets into a pure ranking no-op. It exists because the
	// prior's worth depends on what the palace holds: on a curated palace the
	// boost promotes the memories a human chose to keep; on a corpus dominated
	// by mined transcripts the eval measured the same boost DEMOTING correct
	// answers (~0.10 MRR at n=40) — the closets cover the curated 2% and lift
	// it over the mined gold. The operator knows which palace theirs is.
	closetBoostScale float64

	// recencyBand is the fused-score window inside which a newer content date
	// may swap two adjacent candidates. Zero (the default, and the only value
	// Search's composition root ever sets) leaves the fused order untouched.
	// Eval recency arms set it on a Clone so they rank through rankRetrieved
	// rather than a second reorder beside it. ADR-004 keeps production off.
	recencyBand float64

	// retrieveK is a floor on how many distinct memories Search fetches before
	// ranking, independent of the page Limit. Zero (the default) leaves the
	// fetch at candidateKFor: limit×3, raised to rerankPool only when a
	// cross-encoder will actually run. A positive floor widens; it never
	// shrinks below that formula, and it never changes the page size.
	retrieveK int

	// mineLocks serializes concurrent mines of the same (team, source) within this
	// process, so two re-mines cannot interleave their purge-then-write and leave
	// both content versions behind. It is the in-process analogue of the frozen
	// miner's per-source mine_lock. Note: it does NOT coordinate across horizontally
	// scaled instances — a cross-instance guard would need a DB advisory lock.
	mineLocks *keyedMutex
	// graphLocks serializes a team's recompute_graph the same way: a recompute
	// replaces hallways and delete-and-rebuilds entity tunnels, so two concurrent
	// recomputes of one team could interleave and leave a stale rebuild. Same
	// in-process caveat as mineLocks.
	graphLocks *keyedMutex
}

// Repo exposes the underlying repository. Tool layers hold the Service, and
// seeding or inspection paths (the mcpserver test harness) legitimately need
// the repo; keeping the field private and exposing this accessor keeps that
// one door explicit.
func (s *Service) Repo() *Repo { return s.repo }

// NewService wires the collaborators. dim is the embedding width used to create a
// tenant's vector namespace on first write (the actual width of returned vectors
// is authoritative and used in Add; dim is only the seed/fallback).
func NewService(repo *Repo, embed Embedder, vectors store.VectorStore, dim int) *Service {
	// The writer view is the same Repo with the writer in both seats. A nil repo
	// is legal here — the eval's shape-only default service carries no
	// database at all — and a nil view for it is exactly as usable as its repo.
	var writer *Repo
	if repo != nil {
		writer = repoOn(repo.db)
	}
	return &Service{
		repo: repo, writer: writer, embed: embed, vectors: vectors, dim: dim,
		// Adaptive lexical weighting is the default because it is the only
		// configuration measured best in BOTH query regimes; a zero value here
		// would silently make fusion vector-only, which is a measured regression
		// on identifier queries.
		bm25Auto: true, bm25Base: hybridBM25Weight,
		lexNorm: lexNormPageMax, lexNormName: DefaultLexNorm,
		memoryEvidenceSelector: DefaultMemoryEvidenceSelector,
		// Pointers, not values: the eval's degraded path shallow-copies the
		// service to drop the reranker, and a copied sync.Map is a vet error and
		// a real hazard — the copy must SHARE these locks, it guards the same
		// palace.
		mineLocks: &keyedMutex{}, graphLocks: &keyedMutex{},
		closetBoostScale: 1,
	}
}

// WithReranker attaches a cross-encoder to Search and returns s for chaining.
// pool is how many fused candidates get cross-encoded; values below 1 fall back
// to DefaultRerankPool.
//
// It is a post-construction setter rather than a NewService parameter because
// reranking is optional deployment wiring, not a collaborator the service needs
// to exist — every call site that has no reranker configured simply never calls
// this. It must be called before the service is shared across goroutines: the
// field is read without synchronization on the search path.
func (s *Service) WithReranker(r Reranker, pool int) *Service {
	if pool < 1 {
		pool = DefaultRerankPool
	}
	s.rerank, s.rerankPool = r, pool
	if s.rerankWeight == 0 {
		s.rerankWeight = DefaultRerankWeight
	}
	return s
}

// WithFusion selects how vector and lexical evidence combine: "rrf" for
// reciprocal-rank fusion, anything else for the weighted-score blend. Same
// post-construction-setter contract as WithReranker: call it before the service
// is shared across goroutines.
func (s *Service) WithFusion(mode string) *Service {
	s.fusionRRF = strings.EqualFold(strings.TrimSpace(mode), "rrf")
	return s
}

// DefaultMemoryEvidenceSelector is the control arm: bounded passages selected
// by literal query-term coverage, with no extra embedding call.
const DefaultMemoryEvidenceSelector = "lexical"

const semanticMemoryEvidenceSelector = "semantic"

// WithMemoryEvidenceSelector selects how a reassembled memory is reduced to the
// cross-encoder budget. Unknown values keep the current selector. Call it before
// sharing the service across goroutines, like the other post-construction setters.
func (s *Service) WithMemoryEvidenceSelector(name string) *Service {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", DefaultMemoryEvidenceSelector:
		s.memoryEvidenceSelector = DefaultMemoryEvidenceSelector
	case semanticMemoryEvidenceSelector:
		s.memoryEvidenceSelector = semanticMemoryEvidenceSelector
	}
	return s
}

// WithRerankNorm selects how raw cross-encoder scores are normalised before the
// blend, and returns s for chaining. An unknown or empty name resolves to
// DefaultRerankNorm — sigmoid, not min-max. Passing "" is therefore a request
// for the current default, never a way back to the pre-option behaviour.
//
// Same post-construction-setter contract as WithReranker: call before the
// service is shared.
func (s *Service) WithRerankNorm(name string) *Service {
	switch name {
	case RerankNormMinMax, RerankNormSigmoid, RerankNormRank:
		s.rerankNorm = name
	default:
		s.rerankNorm = DefaultRerankNorm
	}
	return s
}

// RerankNormName reports the resolved normaliser, so am_status and the eval
// table name the same thing the blend actually used.
func (s *Service) RerankNormName() string {
	if s.rerankNorm == "" {
		return DefaultRerankNorm
	}
	return s.rerankNorm
}

// MemoryEvidenceSelectorName reports the resolved operator-facing selector.
func (s *Service) MemoryEvidenceSelectorName() string {
	if s.memoryEvidenceSelector == semanticMemoryEvidenceSelector {
		return semanticMemoryEvidenceSelector
	}
	return DefaultMemoryEvidenceSelector
}

// Clone returns a shallow copy, so a caller can configure one Service several
// ways without the configurations bleeding into each other.
//
// It exists because every With* setter MUTATES and returns the same pointer.
// That is convenient for a composition root, which configures once, and a trap
// for anything that configures repeatedly: a sweep that reused one Service would
// carry each cell's settings into the next, and every knob after the first would
// look inert — the exact conclusion such a sweep exists to draw, reached for the
// wrong reason.
//
// Shallow is correct here. The fields it copies are scalars and interface
// handles; the repo, embedder and vector store are shared on purpose, since a
// sweep varies ranking rather than storage.
func (s *Service) Clone() *Service {
	c := *s
	return &c
}

// WithClosetBoost scales the closet curation prior (1 = full, 0 = off). Same
// post-construction-setter contract as WithReranker: call before the service is
// shared across goroutines.
func (s *Service) WithClosetBoost(scale float64) *Service {
	if scale < 0 {
		scale = 0
	}
	if scale > 1 {
		scale = 1
	}
	s.closetBoostScale = scale
	return s
}

// WithRetrieveK sets a floor on how many distinct memories Search retrieves
// before ranking. k <= 0 turns the floor off and leaves candidateKFor in
// charge. Same post-construction-setter contract as WithReranker: call before
// the service is shared across goroutines.
func (s *Service) WithRetrieveK(k int) *Service {
	if k < 0 {
		k = 0
	}
	s.retrieveK = k
	return s
}

// WithBM25Weight configures the lexical half of fusion: auto scales it per query,
// otherwise base is used as a fixed weight. Out-of-range bases keep the default.
func (s *Service) WithBM25Weight(auto bool, base float64) *Service {
	s.bm25Auto = auto
	if base >= 0 && base <= 1 {
		s.bm25Base = base
	}
	return s
}

// WithLexicalIDF selects the IDF-weighted coverage feature for auto weighting.
// Same post-construction-setter contract as WithReranker: call it before the
// service is shared across goroutines.
func (s *Service) WithLexicalIDF(on bool) *Service {
	s.bm25IDF = on
	return s
}

// WithLexNorm selects the lexical normaliser by its operator-facing name. An
// unrecognised name keeps the default rather than ranking differently in silence
// — the same choice --fusion makes for an unrecognised value.
func (s *Service) WithLexNorm(name string) *Service {
	if n, ok := lexNormByName(name); ok {
		s.lexNorm, s.lexNormName = n, name
	}
	return s
}

// WithRecencyBand sets the fused-score window for a date-preference reorder.
// Zero or negative turns it off. Production never calls this; eval recency
// arms do, so the reorder runs inside rankRetrieved instead of beside it.
func (s *Service) WithRecencyBand(band float64) *Service {
	if band < 0 {
		band = 0
	}
	s.recencyBand = band
	return s
}

// fusionRanker is the ranking function this service fuses candidates with.
//
// Named rather than inlined in Search so that "which ranker does production
// run?" is a question with one answer a test can call. The eval names an arm for
// a served configuration, and the only honest way to check that mapping is to
// run BOTH rankers on the same input and compare the order — a check on the arm
// NAME passes happily while the two functions differ.
func (s *Service) fusionRanker() func(query string, docs []string, dists, boosts []float64) []HybridScore {
	switch {
	case s.fusionRRF:
		// Rank fusion ignores bm25Base entirely — the weight question does not
		// arise when neither signal contributes a magnitude, only a position.
		return rankRRF
	case s.bm25Auto && s.bm25IDF:
		return func(query string, docs []string, dists, boosts []float64) []HybridScore {
			return rankHybridAdaptiveIDFNorm(query, docs, dists, boosts, s.bm25Base, s.lexNorm)
		}
	case s.bm25Auto:
		return func(query string, docs []string, dists, boosts []float64) []HybridScore {
			return rankHybridAdaptiveNorm(query, docs, dists, boosts, s.bm25Base, s.lexNorm)
		}
	default:
		return func(query string, docs []string, dists, boosts []float64) []HybridScore {
			return rankHybridWeightedNorm(query, docs, dists, boosts, s.bm25Base, s.lexNorm)
		}
	}
}

// RankingProfile is the fully resolved ranking configuration in one line: every
// decision that will act on the next query, whether an operator set it or it came
// from a default.
//
// It exists because a deployment could not previously say what it ranks with.
// Startup announced DELTAS — a default configuration printed nothing at all — so
// "no lines" meant both "everything is default" and "the operator set values that
// happen to equal the defaults", and neither an operator reading logs nor an agent
// reading am_status could tell which arm of an eval table their server
// corresponds to. A measurement that cannot be tied to a configuration is a
// number about nothing.
func (s *Service) RankingProfile() string {
	fusion := "linear"
	if s.fusionRRF {
		fusion = "rrf"
	}
	lex := fmt.Sprintf("%.2f", s.bm25Base)
	lexNorm := s.lexNormName
	switch {
	case s.bm25Auto && s.bm25IDF:
		lex = "auto-idf"
	case s.bm25Auto:
		lex = "auto"
	}
	if s.fusionRRF {
		// Rank fusion combines positions, not magnitudes. Printing the unused
		// default ("auto", "page-max") made am_status claim lexical knobs that
		// Search never consulted.
		lex = "n/a"
		lexNorm = "n/a"
	}
	rerank := "off"
	if s.rerank != nil {
		rerank = fmt.Sprintf("on(pool=%d,weight=%.2f,norm=%s)", s.rerankPool, s.rerankWeight, s.RerankNormName())
	}
	profile := fmt.Sprintf("fusion=%s lex-weight=%s lex-norm=%s closet-boost=%.2f rerank=%s unit=memory evidence=%s",
		fusion, lex, lexNorm, s.closetBoostScale, rerank, s.MemoryEvidenceSelectorName())
	if s.retrieveK > 0 {
		profile += fmt.Sprintf(" retrieve-k=%d", s.retrieveK)
	}
	return profile
}

// LexNormName reports the normaliser in force, so startup and am_status can state
// what is actually ranking rather than what was requested.
func (s *Service) LexNormName() string { return s.lexNormName }

// WithRerankWeight sets how much the cross-encoder's opinion counts against the
// hybrid score it refines: 1 hands it the whole decision, 0 ignores it. Values
// outside [0,1] are ignored, leaving DefaultRerankWeight in place.
func (s *Service) WithRerankWeight(w float64) *Service {
	if w >= 0 && w <= 1 {
		s.rerankWeight = w
	}
	return s
}

// AddInput is the add_drawer payload: where the memory goes (wing, room — both
// required), the verbatim text, and optional provenance/date metadata.
type AddInput struct {
	Wing        string
	Room        string
	Content     string
	SourceFile  string
	ContentDate string
}

// AddResult is what a filing returned: the drawers written, and whether their
// vectors are still owed.
//
// PendingEmbedding is not an error and not a detail — it is the difference
// between "this memory is findable" and "this memory exists but nothing will
// recall it yet". The caller is expected to say so out loud, because the failure
// it comes from (an embedder that is down) is invisible from the outside.
type AddResult struct {
	Drawers          []Drawer
	PendingEmbedding bool
}

// Add files a memory: it chunks oversized content, embeds every chunk in one
// batch, writes the vectors, then writes the metadata rows. Vectors are written
// before rows so a row never exists without its embedding — search joins row to
// vector, and the inverse orphan (a vector with no row) is harmless because
// search skips ids it cannot resolve. It returns the drawers created (one per
// chunk), so the tool can report their ids.
func (s *Service) Add(ctx context.Context, teamID string, in AddInput) (result AddResult, err error) {
	_, sp := telemetry.Start(ctx, telemetry.StageAdd)
	defer func() {
		endStage(sp, err, attribute.Int("am.count", len(result.Drawers)), attribute.Bool("am.pending_embed", result.PendingEmbedding))
	}()
	prepared, err := s.prepareWrite(ctx, teamID, in)
	if err != nil {
		return AddResult{}, err
	}
	if err := s.persistWrite(ctx, s.repo, teamID, prepared); err != nil {
		return AddResult{}, err
	}
	// The edge attaches on BOTH branches. An earlier version attached it only
	// after the embedded path, so a memory filed while the embedder was down
	// became a permanent orphan — precisely the memory a later session most needs
	// to find, and the vector index it is waiting for is not what makes it
	// reachable by traversal.
	s.attachDerivedEdgeTo(ctx, teamID, prepared.drawers)
	return AddResult{Drawers: prepared.drawers, PendingEmbedding: prepared.vectors == nil}, nil
}

// preparedWrite is a filing that has been chunked, EMBEDDED and turned into rows,
// with nothing yet written to the database.
//
// The split exists so a correction can put the successor's rows and the
// predecessor's endings under ONE transaction — see supersedeInto. Embedding is
// the reason that cannot simply be done by wrapping Add: it is a network call,
// and KGSupersede already records what happens when one is held inside SQLite's
// single write transaction — "a slow embedder becomes a locked database".
// Preparing first puts the slow, failable part outside the lock and leaves only
// row writes inside it.
type preparedWrite struct {
	drawers []Drawer
	// vectors is nil when the embedder was unavailable and the rows go to the
	// background queue instead (see embedOrDefer). That is a degraded write, not
	// an error, and persistWrite has a branch for it.
	vectors            [][]float32
	wing, room, source string
	// keep is the set of content keys this filing still asserts, so a re-file of
	// a NAMED source can end the chunks that left it.
	keep []string
}

// prepareWrite does everything a filing needs before anything is persisted:
// chunking, embedding, id resolution and row construction. It writes nothing.
func (s *Service) prepareWrite(ctx context.Context, teamID string, in AddInput) (preparedWrite, error) {
	wing := strings.TrimSpace(in.Wing)
	room := strings.TrimSpace(in.Room)
	content := strings.TrimSpace(in.Content)
	if wing == "" || room == "" || content == "" {
		return preparedWrite{}, fmt.Errorf("%w: wing, room and content are required", ErrInvalidInput)
	}
	// ⚠ THE ROOM IS A NAME AND WAS THE ONE NAME NOTHING CHECKED. SanitizeName's own
	// doc calls it "a wing/room/agent/topic name", am_create_tunnel sanitises
	// source_room and target_room, and am_mine sanitises room — but the call every
	// session makes most did not, so a room could carry `/` and `..`.
	//
	// That is not cosmetic, because a room name is ENCODED INTO A GRAPH SUBJECT by
	// an unescaped join: DerivedEdgeSubject builds "room:<wing>/<room>", and
	// BackfillWingRoots recovers the wing from it by stripping affixes, on the
	// stated assumption that "a wing name is sanitised and carries no slash". True
	// of the wing, false of the room. Measured 2026-09-02: one am_add_drawer into
	// room "evil/llm_init" satisfied that function's HasSuffix("/llm_init") check
	// and, on the next boot, minted `wing_acme/evil.root` — a by-name root for a
	// wing that does not exist. That is exactly the failure its own ⚠ comment was
	// written to prevent, arriving through the unvalidated field rather than the
	// wildcard it anticipated.
	//
	// Checked against the live corpus before enforcing: of 46 distinct rooms, the
	// only two this rejects are the two the probe created.
	wing, err := SanitizeName(wing, "wing")
	if err != nil {
		return preparedWrite{}, err
	}
	room, err = SanitizeName(room, "room")
	if err != nil {
		return preparedWrite{}, err
	}

	chunks := ChunkText(content, ChunkSize, ChunkOverlap, ChunkMin)

	// AN ENTRY RECORD THAT CHUNKS IS NO LONGER REFUSED (ADR-046). A guard stood
	// here and was deleted, which is worth recording because the reasoning that
	// justified it was good and still led to the wrong thing.
	//
	// It refused a multi-chunk record in EntryRoom because am_bootstrap's eager
	// tier served ONE chunk: a longer record arrived cut mid-sentence with
	// truncation.omitted:0 and nothing marking it partial. Measured 2026-09-01 on
	// a 3,600-rune record, the front door served 1,600 and reported no omission.
	// The refusal's own error message named that serving bug as its reason — which
	// is what made it a workaround wearing the shape of a rule.
	//
	// ADR-046 T1 fixed the serving: Bootstrap reassembles every chunk, so there is
	// nothing left to protect against. Two further facts settled it rather than
	// merely permitting it:
	//
	//   - It was ALREADY reachable around. This lived in prepareWrite, while
	//     moveMemory patches rows directly and never routes through here, so
	//     ADR-045 made "file it elsewhere and move it in" a two-call bypass. A rule
	//     enforced on one path and open on another is worse than no rule, because
	//     it reads as a guarantee.
	//   - Refusing had been chosen over warning for a good reason — two authors
	//     filed an over-long entry record in the same turn they read the rule, both
	//     saying "I cannot count runes", so a limit an agent cannot measure is a
	//     limit on nothing. That reasoning is sound and is exactly why the fix had
	//     to be removing the limit rather than restating it.
	//
	// What remains is a COST, not a refusal: an entry record is served whole at
	// every wake-up, so length is paid on the one call no session skips. A spine
	// that points at detail still beats one that inlines it; that is now advice,
	// and the byte-bounding option is in the backlog with a trigger.
	// A failed embed does not fail the write — see embedOrDefer. vectors is nil in
	// that case and the rows are absorbed onto the background queue instead.
	vectors := s.embedOrDefer(ctx, chunks)

	filedAt := time.Now().UTC().Format(time.RFC3339)

	// Reuse the id of any CURRENT row already holding one of these content keys,
	// so re-filing unchanged text updates the row in place and every anchor,
	// tunnel and provenance pointer at it survives — and so the ids returned to
	// the caller are the ids the database ends up with.
	keys := make([]string, 0, len(chunks))
	for _, c := range chunks {
		keys = append(keys, contentKeyOf(teamID, wing, room, in.SourceFile, c.Index, c.Content))
	}
	existing, err := s.writer.IDsByContentKeys(ctx, teamID, keys)
	if err != nil {
		return preparedWrite{}, fmt.Errorf("look up rows already holding these content keys: %w", err)
	}

	drawers := make([]Drawer, len(chunks))
	for i, c := range chunks {
		// The first chunk is the parent the rest of a multi-chunk write point
		// back to; the first chunk itself has no parent.
		parentID := ""
		if i > 0 {
			parentID = drawers[0].ID
		}
		// Entities is the field this path was missing (ADR-016). Without it the
		// derived graph — hallways, entity tunnels, the entity half of traverse —
		// is not empty-for-now on a palace agents write to, it is structurally
		// unreachable: RecomputeGraph reads drawers.entities, nothing on this
		// path ever wrote it, and so a recompute reports success and derives
		// nothing however often it runs.
		//
		// Extraction is per CHUNK, exactly as Mine does it, so co-occurrence
		// stays local: a long memory must name two things in the SAME chunk for
		// them to become a hallway, rather than every chunk inheriting the whole
		// memory's entities and manufacturing connections the text never made.
		drawers[i] = Drawer{
			ID:          mintOrReuse(existing, keys[i]),
			ContentKey:  keys[i],
			TeamID:      teamID,
			Wing:        wing,
			Room:        room,
			SourceFile:  in.SourceFile,
			ChunkIndex:  c.Index,
			Content:     c.Content,
			Entities:    extractEntities(c.Content),
			FiledAt:     filedAt,
			ContentDate: strings.TrimSpace(in.ContentDate),
			ParentID:    parentID,
		}
	}

	return preparedWrite{
		drawers: drawers,
		vectors: vectors,
		wing:    wing,
		room:    room,
		source:  in.SourceFile,
		keep:    keys,
	}, nil
}

// persistWrite writes a prepared filing: its vectors, then its rows, through the
// repo it is handed.
//
// ⚠ THE REPO IS A PARAMETER so a caller can pass one bound to a transaction —
// that is what lets supersedeInto commit a successor and its predecessor's
// endings together. Vectors are written FIRST and deliberately OUTSIDE any such
// transaction: it keeps Add's existing invariant that a row never exists without
// its embedding, and the inverse orphan is harmless because search skips ids it
// cannot resolve.
func (s *Service) persistWrite(ctx context.Context, r *Repo, teamID string, p preparedWrite) error {
	if p.vectors != nil {
		if err := s.upsertDrawerVectors(ctx, teamID, p.drawers, p.vectors); err != nil {
			return err
		}
	}
	return s.persistRows(ctx, r, teamID, p)
}

// persistRows is persistWrite's row half, split out so a transactional caller can
// write the VECTORS first and outside the transaction.
//
// ⚠ THAT SPLIT IS NOT TIDINESS. s.vectors was constructed with the service's own
// *gorm.DB, and sqlitevec shares that handle — so calling upsertDrawerVectors
// inside a transaction opens a SECOND connection to the file the transaction
// already holds the write lock on, which is the deadlock KGSupersede's comment
// warns about arriving by a different door.
func (s *Service) persistRows(ctx context.Context, r *Repo, teamID string, p preparedWrite) error {
	// Re-filing a *named* source replaces it wholesale: end the source's prior
	// drawers whose content key LEFT the source, so shrinking the content cannot
	// leave orphaned higher-index chunks behind. A source-less add is a standalone
	// memory (deduped by its CONTENT KEY, not its id), so it is not purged.
	if p.source != "" {
		if err := s.purgeSourceOn(ctx, r, teamID, p.wing, p.room, p.source, p.keep); err != nil {
			return err
		}
	}
	if p.vectors == nil {
		if err := r.SaveUnembedded(ctx, p.drawers); err != nil {
			return fmt.Errorf("save drawers (embedding deferred): %w", err)
		}
		return nil
	}
	if err := r.Save(ctx, p.drawers); err != nil {
		return fmt.Errorf("save drawers: %w", err)
	}
	return nil
}

// attachDerivedEdgeTo makes a freshly written set of chunks reachable by
// traversal. EVERY write path calls it — Add's normal branch, Add's deferred
// branch, and the import path — because a capability reachable on the one path
// somebody tested is this repository's characteristic defect, and T6 shipped
// with exactly that shape before a cross-check found the other two.
//
// The edge goes on the ROOT chunk only. A memory is chunked, and one edge per
// chunk would multiply a single filing into as many graph rows as it happened to
// split into, inflating the very count this is measured by.
//
// Failure is logged, never fatal. The text is the memory; the edge is only how it
// is reached, and losing a filing because the graph refused would be the worse
// trade — the same reasoning the deferred-embedding branch already makes.
func (s *Service) attachDerivedEdgeTo(ctx context.Context, teamID string, drawers []Drawer) {
	// One edge per SOURCE ROOT, not per batch. An import batch can carry records
	// from several independent source files, and attaching only to drawers[0]
	// left every other root unedged — a distinct defect from the missing call
	// that preceded it, and invisible for the same reason: the batch that was
	// tested had one source.
	seen := map[string]bool{}
	rootedWings := map[string]bool{}
	for i := range drawers {
		d := drawers[i]
		// The ROOT chunk of each memory. A chunked memory must not contribute one
		// edge per chunk, which would inflate the count this is measured by.
		if d.ParentID != "" {
			continue
		}
		key := d.Wing + "\x00" + d.Room + "\x00" + d.SourceFile
		if seen[key] {
			continue
		}
		seen[key] = true

		got, err := s.attachDerivedEdge(ctx, teamID, d)
		if err != nil {
			logAttachFailure(ctx, d.ID, err)
			continue
		}
		// A drawer landing in the entry room also gives the wing its by-name root,
		// so `<wing>.root` resolves to the node am_entry_point already reads.
		//
		// ⚠ ONCE PER WING PER BATCH. The loop runs per drawer, and several entry
		// sources in one call would otherwise repeat a check-then-insert whose
		// whole job is idempotence.
		//
		// A failure costs the ADDRESS and not the write, and it is logged as its
		// own thing: the drawer and its room edge are already durable, so reusing
		// the generic attach failure would claim the memory is unreachable when
		// only the chosen name is.
		if d.Room == EntryRoom && !rootedWings[d.Wing] {
			rootedWings[d.Wing] = true
			if err := s.attachWingRootEdge(ctx, teamID, d.Wing); err != nil {
				logRootFailure(ctx, d.Wing, err)
			}
		}
		// Reported from what actually happened. Setting both flags unconditionally
		// made a drawer a writer had deliberately placed come back claiming the
		// server guessed for it.
		drawers[i].HasEdge = true
		drawers[i].EdgeDerived = got != EdgeAuthored
	}
}

// logAttachFailure records a derived-edge attachment that did not happen. It is
// deliberately non-fatal and deliberately not silent: an orphan created because
// the graph write failed looks exactly like one created before this existed.
func logAttachFailure(ctx context.Context, drawerID string, err error) {
	slog.WarnContext(ctx, "derived edge not attached; drawer is filed but unreachable by traversal",
		"drawer_id", drawerID, "err", err)
}

// logRootFailure records a wing root that was not minted.
//
// ⚠ SEPARATE FROM logAttachFailure BECAUSE THE CONSEQUENCE IS DIFFERENT. By the
// time this can fail the drawer and its room edge are durable, so the memory IS
// reachable — through am_entry_point and through search. What was lost is the
// chosen name a session can guess. Reusing the attach warning would tell an
// operator the memory is unreachable, which is false and sends them looking in
// the wrong place.
func logRootFailure(ctx context.Context, wing string, err error) {
	slog.WarnContext(ctx, "wing root not minted; the memory is reachable but <wing>.root is not",
		"wing", wing, "err", err)
}

// embedChunks embeds a batch of chunks, returning one vector per chunk in order.
// It is the shared embed step of every filing path (add_drawer, diary_write), so
// the chunk -> vector contract is single-sourced rather than copied per tool.
func (s *Service) embedChunks(ctx context.Context, chunks []Chunk) ([][]float32, error) {
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	vectors, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed drawer: %w", err)
	}
	return vectors, nil
}

// embedOrDefer embeds chunks, returning nil when the embedder could not do it.
//
// A nil result means "write the rows without vectors and let the background
// worker finish the job" — the durable half of a memory is its text, and losing
// that because an optional-at-this-instant service is down is the worst possible
// trade. The queue this feeds (embedded_at IS NULL) already exists for migration
// imports, so a deferred row is picked up by the same worker and the same model.
//
// EVERY embed failure defers, not just a refused connection: a timeout, a 500, a
// model that was never pulled. Classifying them would mean deciding which
// failures are worth losing a memory over, and none are.
func (s *Service) embedOrDefer(ctx context.Context, chunks []Chunk) [][]float32 {
	vectors, err := s.embedChunks(ctx, chunks)
	if err == nil {
		return vectors
	}
	slog.Warn("embedder unavailable, storing for background embedding", "chunks", len(chunks), "error", err)
	return nil
}

// storeDrawers is the shared persistence tail every filing path ends in: ensure
// the tenant's vector namespace exists, write the embeddings, then write the
// metadata rows. Vectors are written before rows so a row never exists without
// its embedding — search joins row to vector, and the inverse orphan (a vector
// with no row) is harmless because search skips ids it cannot resolve. The vector
// width the model returned is authoritative for namespace creation, so a mis-set
// s.dim can never make EnsureNamespace and Upsert disagree. drawers and vectors
// must be index-aligned and the same length.
func (s *Service) storeDrawers(ctx context.Context, teamID string, drawers []Drawer, vectors [][]float32) error {
	if err := s.upsertDrawerVectors(ctx, teamID, drawers, vectors); err != nil {
		return err
	}
	if err := s.repo.Save(ctx, drawers); err != nil {
		return fmt.Errorf("save drawers: %w", err)
	}
	return nil
}

// upsertDrawerVectors ensures the tenant's vector namespace and writes the
// embeddings only — no metadata rows. It is shared by the synchronous filing tail
// (storeDrawers, which then writes rows) and the background embed worker (which
// backfills vectors for rows absorb already wrote). drawers and vectors must be
// index-aligned and the same length; the returned vector width is authoritative
// for namespace creation so a mis-set s.dim cannot make EnsureNamespace and Upsert
// disagree.
func (s *Service) upsertDrawerVectors(ctx context.Context, teamID string, drawers []Drawer, vectors [][]float32) error {
	dim := s.dim
	if len(vectors) > 0 {
		dim = len(vectors[0])
	}
	if err := s.vectors.EnsureNamespace(ctx, teamID, dim); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}
	points := make([]store.Point, len(drawers))
	for i, d := range drawers {
		// Payload carries only the cheap filter keys; the verbatim content stays
		// single-sourced in the drawers table, joined back by id at search time.
		points[i] = store.Point{
			ID:      d.ID,
			Vector:  vectors[i],
			Payload: map[string]any{"wing": d.Wing, "room": d.Room},
		}
	}
	if err := s.vectors.Upsert(ctx, teamID, points); err != nil {
		return fmt.Errorf("upsert vectors: %w", err)
	}
	return nil
}

// purgeSource deletes every drawer (row + vector) previously filed from a source
// within a (team, wing, room), so a re-add of that source replaces rather than
// accumulates. Vectors are dropped by the ids the rows carry, then the rows.
// purgeSourceOn takes the repo as a parameter for the same reason persistWrite
// does: a correction runs this inside its transaction, so the endings it performs
// commit or roll back with the successor's rows rather than separately.
func (s *Service) purgeSourceOn(ctx context.Context, r *Repo, teamID, wing, room, source string, keep []string) error {
	rows, err := r.CurrentBySource(ctx, teamID, wing, room, source)
	if err != nil {
		return fmt.Errorf("list source drawers: %w", err)
	}
	kept := make(map[string]bool, len(keep))
	for _, k := range keep {
		kept[k] = true
	}
	var ids []string
	for _, row := range rows {
		if !kept[row.ContentKey] {
			ids = append(ids, row.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	// ENDED, not deleted, and the vectors and anchors STAY.
	//
	// This used to delete every row under the triple — with its vectors, its
	// derived edges and, through DeleteBySource, its ANCHORS — and let Add
	// re-insert. That was survivable only because ids were derived from content,
	// so an unchanged chunk came back with the same id. It never saved the
	// anchors: 39 of the 41 anchored drawers in the live palace were one re-file
	// from losing their pin, and nothing reported it.
	//
	// Now only the rows whose content key LEFT the source are touched, and they
	// are ended rather than destroyed: a chunk a re-file dropped is a memory the
	// team stopped asserting, which is a retraction, not an erasure.
	// Ended through the SAME repo, not through s.EndDrawer: that method reads and
	// writes on s.repo.db, so calling it from inside a transaction would use a
	// second connection to the file the transaction already holds a write lock on.
	// The rows here were just read as CURRENT, so EndDrawer's already-ended refusal
	// has nothing to check that this query does not.
	now := time.Now().UTC().Format(time.RFC3339)
	reason := "dropped from " + source + " on re-file"
	for _, id := range ids {
		err := r.db.WithContext(ctx).Model(&drawerRow{}).
			Where("team_id = ? AND id = ? AND valid_to = ''", teamID, id).
			Updates(map[string]any{"valid_to": now, "ended_at": now, "ended_reason": reason}).Error
		if err != nil {
			return fmt.Errorf("end drawer %s dropped from the source: %w", short12(id), err)
		}
	}
	// The server's own derived edges go with them. A re-file that drops a chunk
	// leaves the room's `holds` edge pointing at an ended row otherwise, and the
	// author has no call that would end it — the same defect a correction had.
	if err := endDerivedEdgesFor(r.db.WithContext(ctx), teamID, ids, now, reason); err != nil {
		return fmt.Errorf("end the derived edges of drawers dropped from the source: %w", err)
	}
	return nil
}

// An ended row KEEPS its vector. Nothing is deleted by a re-file: T5 composes
// current() into recall, which is what stops an ended row being returned, and
// destroying the vector would make an ending irreversible in the one store a
// rollback cannot repair.
//
// ⚠ ITS DERIVED EDGES DO NOT SURVIVE, and this comment used to say they did.
// A derived edge naming an ended row is a pointer the server minted and the
// author cannot remove; leaving it current puts dead records at the front of
// am_entry_point. Authored edges are untouched — see endDerivedEdgesFor.

// Get returns one drawer, mapping an unknown id to ErrNotFound.
func (s *Service) Get(ctx context.Context, teamID, id string) (Drawer, error) {
	return s.getOn(ctx, s.repo, teamID, id)
}

// getOn is Get through an explicit view: s.repo for a query, s.writer when the
// result feeds a write — Update reads the successor it just minted through it.
// ADR-052: a write-path lookup on the read model is a different snapshot, so
// the check it makes is not binding on the write that follows.
func (s *Service) getOn(ctx context.Context, r *Repo, teamID, id string) (d Drawer, err error) {
	_, sp := telemetry.Start(ctx, telemetry.StageGet)
	defer func() {
		if errors.Is(err, ErrNotFound) {
			sp.End(telemetry.Ran, attribute.Bool("am.found", false))
			return
		}
		endStage(sp, err, attribute.Bool("am.found", err == nil))
	}()
	d, err = r.Get(ctx, teamID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Drawer{}, ErrNotFound
	}
	if err != nil {
		return Drawer{}, err
	}
	// Ended records are not on the default route (ADR-038 T5), and the refusal
	// NAMES the way in. An agent reaches an ended id by holding the `supersedes`
	// a correction just handed it; a bare "not found" for a row that plainly
	// exists is a dead end at exactly the moment the agent is doing the right
	// thing.
	if d.ValidTo != "" {
		// The successor clause only when there IS one. A retraction replaces
		// nothing, and "or read , which replaced it" is the shape of a bug — it
		// reads as a lost id rather than as an absence that is meant.
		return Drawer{}, endedRefusal(id, d)
	}
	one := []Drawer{d}
	if err := s.attachSupersedes(ctx, teamID, one); err != nil {
		return Drawer{}, err
	}
	return one[0], nil
}

// endedRefusal is the answer for a drawer that exists and has been ended.
//
// ⚠ IT IS SHARED BY BOTH READ PATHS ON PURPOSE. Get had it and GetMemory did not,
// so `am_get_drawer(id)` refused an ended drawer with the date, the reason and the
// successor, while `am_get_drawer(id, whole: true)` on the SAME id answered a bare
// "drawer not found". Reported 2026-08-29 with a same-id, same-second, one-variable
// proof.
//
// That is the worst possible flag to lose it on: the protocol tells readers to
// pass whole:true whenever they mean to READ a memory rather than confirm it
// exists, so the flag we recommend for real reading was the one that hid the
// correction. And the degraded message is not merely less useful, it is a
// DIFFERENT CLAIM — "not found" reads as never existed, not as was corrected, so a
// reader chasing a citation concludes the pointer was bad and moves on.
func endedRefusal(id string, d Drawer) error {
	// The successor clause only when there IS one. A retraction replaces nothing,
	// and "or read , which replaced it" is the shape of a bug — it reads as a lost
	// id rather than as an absence that is meant.
	successor := ""
	if d.SupersededBy != "" {
		successor = fmt.Sprintf(", or read %s, which replaced it", short12(d.SupersededBy))
	}
	return fmt.Errorf("%w: drawer %s was ended on %s (%q). Pass include_history to read it%s",
		ErrNotFound, short12(id), d.ValidTo, truncateReason(d.EndedReason), successor)
}

// GetMemory returns every chunk of the memory the given drawer belongs to, in
// chunk order — the parent and its children, or just the drawer itself when it
// was never split.
//
// It exists because collapsing a search page to one hit per memory is only safe
// if the rest of the memory can be fetched, and until now it could not:
// repo.MemoryChunks was written and tested, and called by Update and Delete
// alone. Both are write paths. No read path could reach a whole chunked memory,
// so am_get_drawer handed back one chunk of a long note and there was no second
// call that would complete it.
//
// The id may be ANY chunk's, not only the first: a caller holding a search hit
// holds whichever chunk matched.
func (s *Service) GetMemory(ctx context.Context, teamID, id string) (chunks []Drawer, err error) {
	_, sp := telemetry.Start(ctx, telemetry.StageGetMemory)
	defer func() {
		if errors.Is(err, ErrNotFound) {
			sp.End(telemetry.Ran, attribute.Bool("am.found", false), attribute.Int("am.count", 0))
			return
		}
		endStage(sp, err, attribute.Bool("am.found", err == nil), attribute.Int("am.count", len(chunks)))
	}()
	chunks, err = s.repo.MemoryChunks(ctx, teamID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Ended chunks are off the default route with the rest (ADR-038 T5). A memory
	// ends whole — T4 ends every chunk in one pass and TestNoMemoryEndsHalfway
	// asserts it — so this filter either keeps all of them or none, and a mixed
	// result would mean the invariant broke rather than that the filter is
	// partial.
	current := chunks[:0]
	for _, c := range chunks {
		if c.ValidTo == "" {
			current = append(current, c)
		}
	}
	if len(current) == 0 {
		// Every chunk is ended, so the memory was corrected or retracted — which is
		// a different answer from "no such id", and the caller is holding an id it
		// got from somewhere. Refuse the way Get does.
		return nil, endedRefusal(id, chunks[0])
	}
	// Lineage on this route too. get_drawer whole=true is how an agent reads a long
	// memory as it was written, and a reader who cannot see that this text replaced
	// something is exactly the reader the carried reason exists for.
	if err := s.attachSupersedes(ctx, teamID, current); err != nil {
		return nil, err
	}
	return current, nil
}

// UpdateResult is what an update produced: the record that now holds the memory,
// and — when the change was a CORRECTION — the record it replaced.
//
// Both halves, because an agent told only "ok" learns neither the id to keep
// working with nor the id it just ended. Supersedes is empty for a move, which is
// how a caller tells a correction from a relocation without asking twice.
type UpdateResult struct {
	Drawer     Drawer `json:"drawer"`
	Supersedes string `json:"supersedes,omitempty"`
	Reason     string `json:"reason,omitempty"`
	EndedAt    string `json:"ended_at,omitempty"`
}

// Update changes an existing memory. What it does depends on WHICH field moves,
// and the split is the point of ADR-038:
//
//   - CONTENT is a correction, and a correction supersedes. A new record is
//     written, the old one is ended with the caller's reason, and the two are
//     linked. The old text survives; only in-place editing destroyed the rejected
//     alternative, which is the one thing irrecoverable at any price.
//   - WING or ROOM alone is a relocation. The memory is the same memory in a
//     different place, so it keeps its id and is edited in place — minting a new
//     record for a move would invalidate every anchor, tunnel and fact pointing at
//     it in exchange for nothing.
//
// A supplied field must be non-empty — update_drawer must not be a back door
// around the non-empty invariant add_drawer enforces (a blank wing/room would
// file the drawer into an unaddressable taxonomy bucket). A no-op patch just
// returns the current drawer.
//
// ⚠ A MOVE COMMITS THE ROWS FIRST AND RELABELS THE INDEX SECOND, AND RE-EMBEDS
// NOTHING (ADR-045). This comment promised the reverse until 2026-09-01 — that a
// move "re-embeds the drawer's final content and re-upserts the vector before the
// row is written" — which was true while a move was ONE row that could not
// partially fail. A move now takes every chunk of a memory in one transaction, so
// the rows must commit before anything describes them: an index written first
// would describe a move that a rollback then undid. And nothing is re-embedded,
// because a move changes no content — only the wing and room a scoped search
// filters on, written with SetPayload, which fails open. See moveMemory.
//
// It went false in the commit that made it false and was caught in review, not by
// a gate; a doc comment on an exported declaration is the one thing that ships to
// a reader who has none of this context, so it is worth the paragraph.
func (s *Service) Update(ctx context.Context, teamID, id string, patch DrawerPatch) (result UpdateResult, err error) {
	_, sp := telemetry.Start(ctx, telemetry.StageUpdate)
	defer func() { endStage(sp, err) }()
	for _, f := range []struct {
		name string
		val  *string
	}{{"content", patch.Content}, {"wing", patch.Wing}, {"room", patch.Room}} {
		if f.val != nil && strings.TrimSpace(*f.val) == "" {
			return UpdateResult{}, fmt.Errorf("%w: %s cannot be set empty", ErrInvalidInput, f.name)
		}
	}

	// GetAnyVersion so an ended record produces the refusals below — the supersede
	// path's "already ended on X" and the move guard — rather than a bare miss.
	current, err := s.getAnyVersionOn(ctx, s.writer, teamID, id) // the writer's own read, not the read model's (ADR-052); also maps unknown id -> ErrNotFound
	if err != nil {
		return UpdateResult{}, err
	}
	// A MOVE of an ended record is refused here; a CORRECTION of one is refused by
	// supersedeInto with the reason and date attached. Neither is allowed, because
	// the first ending is the one that is true and relocating history rewrites
	// where a decision was taken.
	if current.ValidTo != "" && patch.Content == nil {
		return UpdateResult{}, fmt.Errorf(
			"%w: drawer %s was ended on %s (%q) and cannot be moved. Correct the record that "+
				"replaced it, not the one it replaced",
			ErrInvalidInput, short12(id), current.ValidTo, truncateReason(current.EndedReason))
	}

	// Nothing to change.
	if patch.Content == nil && patch.Wing == nil && patch.Room == nil {
		return UpdateResult{Drawer: current}, nil
	}

	// A content change is a CORRECTION, and it leaves this function here.
	//
	// This branch is placed BEFORE the multi-chunk refusal below deliberately. That
	// refusal told the caller to "delete the memory and file it again as one
	// piece"; a supersede is that instruction performed correctly and without the
	// delete, so keeping the guard ahead of it would make correction impossible for
	// exactly the long documents that most need it.
	if patch.Content != nil {
		wing, room := current.Wing, current.Room
		if patch.Wing != nil {
			wing = *patch.Wing
		}
		if patch.Room != nil {
			room = *patch.Room
		}
		res, serr := s.supersedeInto(ctx, teamID, id, *patch.Content, patch.Reason, wing, room)
		if serr != nil {
			return UpdateResult{}, serr
		}
		d, gerr := s.getOn(ctx, s.writer, teamID, res.ID) // the successor is current by construction, and it was minted on the writer
		if gerr != nil {
			return UpdateResult{}, gerr
		}
		return UpdateResult{Drawer: d, Supersedes: res.Supersedes, Reason: res.Reason, EndedAt: res.EndedAt}, nil
	}

	// A memory over ChunkSize is several rows sharing a parent, and a MOVE has to
	// take all of them or none. This used to refuse instead (ADR-045 removed the
	// refusal), because the function could only patch the one row it was given and
	// moving that row split the memory across two scopes: no single search returned
	// all of it, and the fragment did not say it was one.
	//
	// Refusing was never protecting an invariant — it was the honest answer of a
	// function doing 1/N of the job, and it was the last row-scoped write path in
	// this package. Delete, Supersede and InvalidateDrawer all resolve MemoryChunks
	// in order to ACT on every chunk; this one resolved them in order to decline.
	//
	// What made it safe to remove is that a move changes no CONTENT. Chunk
	// boundaries and chunk_index are therefore unchanged, no row is minted or
	// destroyed, and every knowledge-graph fact, anchor and pinned tunnel keeps
	// pointing at a live id — which is why ADR-045 needs no answer to ADR-027's
	// open question about a reference into a chunk a re-chunk would delete.
	// Re-chunking on a CONTENT update remains unsolved and remains in the backlog.
	chunks, err := s.writer.MemoryChunks(ctx, teamID, id)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("look up the memory this drawer belongs to: %w", err)
	}

	finalWing, finalRoom := current.Wing, current.Room
	if patch.Wing != nil {
		finalWing = *patch.Wing
	}
	if patch.Room != nil {
		finalRoom = *patch.Room
	}

	if err := s.moveMemory(ctx, teamID, chunks, finalWing, finalRoom); err != nil {
		return UpdateResult{}, err
	}

	// ⚠ THE INDEX IS UPDATED AFTER THE ROWS, WHICH REVERSES THE OLD ORDER. This
	// path used to write the vector first and commit the row second ("index is
	// current; now commit the authoritative row"), which was safe while a move was
	// one row that could not partially fail. It is wrong for N rows with a rollback
	// path: a collision on chunk k aborts the transaction, and an index written
	// beforehand would describe a move that did not happen — the memory answering
	// from a wing it is not in.
	//
	// And it is SetPayload, not Upsert, so nothing is re-embedded. The text did not
	// change, so the stored vector is already correct; only the wing and room
	// strings a scoped search filters on need to move. am_merge_wing has relabelled
	// this way since it was written (see MergeWing) — this is the same repair on a
	// single memory.
	//
	// One call, not a batch: these are the chunks of ONE memory, so the id list is
	// bounded by how far a single note can be split, far below the batch size
	// MergeWing needs for a whole wing.
	ids := make([]string, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	// Fails OPEN, for the reason carryAnchors does: by this point the move is
	// durable, and returning an error would report a write that succeeded as one
	// that failed — sending the caller to retry a move that has already happened.
	//
	// ⚠ THE NAMED RECOVERY IS NARROWER THAN IT LOOKS, and stating the gap is the
	// point — this comment claimed "a stale payload is recoverable and named" until
	// a reviewer checked both commands (PR #147). Neither covers every case:
	//
	//   - `doctor --index` compares the payload's WING only (see indexdrift.go), so
	//     a ROOM-only move whose relabel failed reports CLEAN. The one check offered
	//     for this drift cannot see half of it.
	//   - `sync --repair-payload` refuses every backend but Qdrant (cmd/server/sync.go),
	//     so it is not a remedy on the default sqlite backend or on chromem — though
	//     on those two the source of truth IS the index, or is refilled from it at
	//     boot, which is why the gap is narrower than it first reads.
	//
	// Both are in BACKLOG.md. Until they close, the warning below is an operator's
	// only signal for a room-only move, so it names what moved rather than only
	// reporting that something did.
	if err := s.vectors.SetPayload(ctx, teamID, ids, map[string]string{"wing": finalWing, "room": finalRoom}); err != nil {
		slog.Warn("memory moved but its stored payloads were not relabelled, so a scoped search "+
			"will not find it at its new address; on Qdrant run `agentsmemory doctor --index` "+
			"then `agentsmemory sync --repair-payload` — but --index compares the WING only, so "+
			"a room-only move has to be checked against the row by hand",
			"error", err, "drawer", short12(id), "wing", finalWing, "room", finalRoom)
	}

	// Re-attach at the NEW address, after the commit and non-fatally, because
	// attachDerivedEdgeTo is non-fatal everywhere it is called: the rows are the
	// memory and the edge is only how it is reached. It edges the ROOT chunk only
	// and reads each drawer's CURRENT wing/room, so post-move copies are what it
	// needs — and a memory moved INTO the entry room mints that wing's by-name
	// root here, exactly as filing one there would.
	moved := make([]Drawer, len(chunks))
	copy(moved, chunks)
	for i := range moved {
		moved[i].Wing, moved[i].Room = finalWing, finalRoom
	}
	s.attachDerivedEdgeTo(ctx, teamID, moved)

	updated, err := s.writer.Get(ctx, teamID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return UpdateResult{}, ErrNotFound
	}
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{Drawer: updated}, nil
}

// moveMemory relabels every chunk of one memory to a new wing/room in a single
// transaction, so a memory is never left split across two scopes.
//
// The transaction is the whole point rather than a precaution. content_key hashes
// wing and room (ADR-038), so relocating an N-chunk memory recomputes N keys
// against a partial unique index, and any one of them can collide with a memory
// already at the destination — "this wing already has that memory" is precisely
// when somebody relocates one. Without a rollback, a collision on chunk k would
// leave chunks 0..k-1 moved, which is the split state the old refusal existed to
// prevent, reintroduced by the fix meant to remove it.
//
// Repo.Update writes through the Repo's own handle, so the transaction is passed
// as one — the same construction supersedeInto uses for persistRows. It also
// recomputes content_key from post-patch state and names a collision rather than
// leaking the driver's, so both behaviours carry over per chunk unchanged.
func (s *Service) moveMemory(ctx context.Context, teamID string, chunks []Drawer, wing, room string) error {
	ids := make([]string, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	return s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := repoOn(tx)
		for _, c := range chunks {
			w, r := wing, room
			if _, err := txRepo.Update(ctx, teamID, c.ID, DrawerPatch{Wing: &w, Room: &r}); err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrNotFound
				}
				return err
			}
		}
		// ⚠ AND END THE DERIVED EDGES, IN THIS SAME TRANSACTION. A derived edge
		// names the ROOM a drawer is in, so a move invalidates it by definition —
		// leave it current and a session traversing the old room is sent to a
		// memory that is no longer there.
		//
		// Inside the transaction because the ending and the relabel are one fact:
		// a collision that rolls the rows back must roll the edges back with them,
		// or the memory stays where it was with nothing pointing at it.
		//
		// Only DERIVED edges end. An AUTHORED edge keeps pointing at the drawer and
		// must, because a move preserves the id — that is the property that lets
		// ADR-045 relabel rows at all, and ending an author's edge would silently
		// discard a pointer somebody wove by hand.
		//
		// This is the FOURTH call site of a pair that already had three: Add
		// attaches, Supersede ends and re-attaches, Delete drops, InvalidateDrawer
		// ends. The move having none was a defect older than ADR-045 — a
		// SINGLE-chunk relocation has always been allowed and has always orphaned
		// its old room's edge.
		now := time.Now().UTC().Format(time.RFC3339)
		if err := endDerivedEdgesFor(tx, teamID, ids, now,
			"the drawer this derived edge points at was moved to another wing or room"); err != nil {
			return fmt.Errorf("end the moved memory's derived edges: %w", err)
		}
		// ⚠ AND IF THAT EMPTIED AN ENTRY ROOM, THE WING'S ROOT GOES WITH IT.
		//
		// EnsureWingRoot mints `<wing>.root` when a record lands in the entry room
		// and nothing ever ended it, so the move shipped a move-IN half with no
		// move-OUT half. endDerivedEdgesFor cannot be that half: it filters
		// `object IN drawerIDs`, and the root edge's object is the ROOM node, so
		// the loop above takes the record's own holds edge and leaves the root
		// pointing at a room that now holds nothing. That resolves `matched` with
		// zero edges one hop on — the state BackfillWingRoots' own comment calls
		// worse than unknown_term, and the one
		// TestBackfillLeavesAWingWithNoLiveEntryRecordNameless pins on the boot
		// path. Reported by review on PR #147.
		//
		// The condition covers a move to another ROOM and a move to another WING:
		// the latter keeps the record in an entry room, just not in THIS wing's.
		if from := chunks[0]; from.Room == EntryRoom && (room != EntryRoom || wing != from.Wing) {
			if err := endWingRootIfEntryRoomIsEmpty(tx, teamID, from.Wing, now,
				"the last live record left this wing's entry room"); err != nil {
				return fmt.Errorf("release the wing root the move emptied: %w", err)
			}
		}
		return nil
	})
}

// Delete removes a drawer's metadata row and its vector. The row goes first so
// the authoritative record is gone before the derived index; a failed vector
// delete leaves an orphan the next search harmlessly skips.
func (s *Service) Delete(ctx context.Context, teamID, id string) (n int, err error) {
	_, sp := telemetry.Start(ctx, telemetry.StageDelete)
	defer func() { endStage(sp, err, attribute.Int("am.count", n)) }()
	// The memory is the unit, not the row. A memory over ChunkSize is several
	// rows sharing a parent, and deleting one of them left the rest orphaned —
	// still embedded, still returned by search, and now pointing at a parent that
	// no longer exists. Reproduced: deleting the parent of a two-chunk memory
	// left chunk 1 live.
	//
	// Unlike an update, a delete has no reference ambiguity to weigh: the caller
	// is removing the memory, so removing all of it is what they asked for. The
	// count is returned so the caller can say how much went, rather than
	// reporting the one id it was given.
	chunks, err := s.writer.MemoryChunks(ctx, teamID, id)
	if err != nil {
		return 0, fmt.Errorf("look up the memory this drawer belongs to: %w", err)
	}
	ids := make([]string, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	if len(ids) == 0 {
		ids = []string{id} // no row to resolve; delete what we were given
	}
	// Same rule as purgeSource: a deleted drawer's derived edges go with it, or
	// they stay current pointing at nothing.
	if err := s.repo.DropDerivedEdgesFor(ctx, teamID, ids); err != nil {
		return 0, fmt.Errorf("drop derived edges: %w", err)
	}
	for _, cid := range ids {
		if err := s.repo.Delete(ctx, teamID, cid); err != nil {
			return 0, fmt.Errorf("delete drawer row: %w", err)
		}
	}
	if err := s.vectors.Delete(ctx, teamID, ids); err != nil {
		return 0, fmt.Errorf("delete drawer vectors: %w", err)
	}
	return len(ids), nil
}

// List paginates a team's drawers, optionally narrowed to a wing and/or room.
func (s *Service) List(ctx context.Context, teamID, wing, room string, limit, offset int) ([]Drawer, error) {
	out, err := s.repo.ListCurrent(ctx, teamID, wing, room, limit, offset)
	if err != nil {
		return nil, err
	}
	if err := s.attachSupersedes(ctx, teamID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// SearchQuery is the mempalace_search input.
type SearchQuery struct {
	Query       string
	Wing        string  // optional filter
	Room        string  // optional filter
	Limit       int     // 1..100, defaults to DefaultSearchLimit
	MaxDistance float64 // drop hits farther than this; <=0 disables the filter
	// SkipTelemetry keeps this search out of the recall statistics. Set by the
	// eval, whose thousands of synthetic queries would otherwise drown the real
	// usage signal the statistics exist to show.
	SkipTelemetry bool
	// Context is optional background the caller can supply to sharpen reranking —
	// what it is working on, so an ambiguous query lands in the right sense. It
	// feeds the cross-encoder ONLY (see rerankQuery); it deliberately does not
	// touch the embedding, because widening the query vector would quietly change
	// which candidates are retrieved rather than how they are ordered.
	Context string
	// RetrieveK is a per-call floor on how many distinct memories this search
	// fetches before ranking. Zero leaves the process default (and, when that
	// is also zero, the candidateKFor formula). It widens only; a value below
	// the formula is ignored. The page Limit is unchanged.
	RetrieveK int
	// IncludeHistory returns records that have been ended alongside current ones.
	// Default false, which is the point of ADR-038 T5: an ended record keeps its
	// vector, so without this filter a retracted claim competes with the
	// correction that replaced it and can outrank it.
	//
	// It is a filter, never a ranking change — ended records that survive it are
	// scored exactly as current ones are. Ranking history differently is deferred
	// (docs/adr/BACKLOG.md).
	IncludeHistory bool
}

// rerankQuery returns the text the cross-encoder scores against: the (already
// capped) query, with Context appended when the caller supplied any. A blank
// Context leaves the query exactly as the vector pass saw it.
func (q SearchQuery) rerankQuery(query string) string {
	if c := strings.TrimSpace(q.Context); c != "" {
		return query + "\n\n" + c
	}
	return query
}

// searchFilter renders a query's wing/room scope as the backend filter, matching
// the payload keys written at upsert time. An unscoped query yields nil, which
// every driver reads as "search everything".
func searchFilter(q SearchQuery) store.Filter {
	if q.Wing == "" && q.Room == "" {
		return nil
	}
	f := store.Filter{}
	if q.Wing != "" {
		f["wing"] = q.Wing
	}
	if q.Room != "" {
		f["room"] = q.Room
	}
	return f
}

// Search recalls memories by hybrid relevance to a query. It embeds the query,
// widens a vector prefix until it holds a pool of distinct logical memories, then
// ranks those memories through rankRetrieved. Storage stays chunked; the ranking
// unit is the memory.
//
// Each semantic stage records an OpenTelemetry span (embed, retrieve, hydrate,
// collapse, closet, fusion, recency, rerank, record) with outcome
// ran|bypassed|failed_open|failed_closed. SQLite search_events stay the product
// log; the two share one search_id so a sampled span can join a durable row.
// Telemetry never changes ranking: a collector that is down drops observability,
// not results. SkipTelemetry skips only the SQLite write (eval); OTEL spans
// still run and hit the noop provider when Setup was not called.
func (s *Service) SearchPage(ctx context.Context, teamID string, q SearchQuery) (SearchResult, error) {
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return SearchResult{}, fmt.Errorf("%w: query is required", ErrInvalidInput)
	}
	// Cap by runes, not bytes: the contract caps queries at 250 characters, and a
	// byte slice could split a multibyte rune into invalid UTF-8 before it reaches
	// the embedder and tokenizer.
	queryRune := []rune(query)
	queryRunes := len(queryRune)
	truncated := false
	if queryRunes > 250 {
		query = string(queryRune[:250])
		truncated = true
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}

	// One id for the SQLite product log and the OpenTelemetry trace. Generated
	// first so a sampled span can join a durable row without a migration; the
	// record stage writes it even when SkipTelemetry skips SQLite (eval).
	searchID := randomID()
	ctx = telemetry.WithSearchID(ctx, searchID)
	// searchCtx is the parent every stage (and outbound HTTP) must Start from.
	// Starting siblings from the pre-Search ctx leaves them parentless — which
	// is how the first eval dump shipped a forest of roots instead of a tree.
	attrs := append(searchAttrs(s, q, limit),
		// A LENGTH, never the text — ADR-025 keeps query text off spans. The pair
		// answers "did the embedder, the lexical channel and the cross-encoder all
		// see the question the caller actually asked?", which nothing could answer
		// before: a query cut mid-sentence left no evidence that the embedded text
		// differed from what was sent.
		attribute.Int("am.query_runes", queryRunes),
		attribute.Bool("am.query_truncated", truncated))
	searchCtx, parent := telemetry.Start(ctx, telemetry.StageSearch, attrs...)
	defer parent.End(telemetry.Ran)

	embedCtx, embedSpan := telemetry.Start(searchCtx, telemetry.StageEmbed)
	vec, err := s.embed.EmbedOne(embedCtx, query)
	if err != nil {
		embedSpan.End(telemetry.FailedClosed, telemetry.AttrReason(telemetry.ReasonError))
		parent.End(telemetry.FailedClosed, telemetry.AttrReason(telemetry.ReasonError))
		// Recall genuinely cannot proceed — a query has to become a vector — so
		// unlike filing this fails. Name the cause, because the same outage lets
		// writes succeed (queued), and an agent seeing one work and the other not
		// will otherwise conclude the memory itself is broken.
		return SearchResult{}, fmt.Errorf("embed query (the embedder is unreachable; writes are still being stored and queued, but recall needs it): %w", err)
	}
	embedAttrs := []attribute.KeyValue{attribute.Int("am.dim", len(vec))}
	// A distance is only interpretable against the model that produced it, and
	// am.dim cannot identify one: two different 1024-dimension models are two
	// different embedding spaces reporting the same number.
	if d, ok := s.embed.(EmbedDescriber); ok {
		backend, model, window := d.DescribeEmbedder()
		embedAttrs = append(embedAttrs,
			attribute.String("am.embed_backend", backend),
			attribute.String("am.embed_model", model))
		if window > 0 {
			// Only when the backend actually REPORTED it. Absent beats guessed:
			// every 8192 in this tree is a comment, and ChunkSize is 5% of it on
			// that authority alone.
			embedAttrs = append(embedAttrs, attribute.Int("am.embed_window_tokens", window))
		}
	}
	embedSpan.End(telemetry.Ran, embedAttrs...)

	// Over-fetch a re-rank pool: BM25 can only reorder what vector retrieval
	// surfaced, so the pool must be wider than the page (limit*multiplier) for a
	// lexical match outside the top-N to be promoted into it.
	//
	// The wing/room scope goes to the BACKEND rather than being applied to the
	// results, so every candidate the index returns is already in scope and the
	// pool stays the size the re-rank was designed for however narrow the filter
	// is. (This used to over-fetch 10 000 candidates and drop the non-matching
	// ones here — a cost that grew with the palace and was paid on every scoped
	// search, which is every search once wings are per-project.)
	candidateK := withRetrieveFloors(
		candidateKFor(limit, s.rerank != nil, s.rerankPool, s.rerankWeight),
		q.RetrieveK, s.retrieveK,
	)
	// THE fetch width, computed on every recall and until now recorded nowhere.
	// It is not derivable from the other attributes: candidateKFor is limit*3
	// raised to rerankPool as a FLOOR, then raised again by any retrieve-k, so
	// am.limit and am.rerank_pool together still do not say what was asked of the
	// index.
	//
	// Measured 2026-08-26 and the reason this exists: at the shipped limit=5 it is
	// 15, while an eval arm ranking the same query saw 100 — a gap of 0.027 MRR
	// and 8 golds no ranking change could reach, invisible on every span.
	telemetry.Annotate(searchCtx, attribute.Int("am.candidate_k", candidateK))
	hits, rows, stale, err := s.searchCandidates(searchCtx, teamID, q, vec, candidateK)
	if err != nil {
		parent.End(telemetry.FailedClosed)
		return SearchResult{}, err
	}
	q.Limit = limit
	results, reranked, skipReason, err := s.rankRetrieved(searchCtx, teamID, query, q, vec, hits, rows, stale)
	if err != nil {
		parent.End(telemetry.FailedClosed)
		return SearchResult{}, err
	}

	// What each returned record REPLACED, resolved on the ranked page only.
	//
	// Here rather than inside rankRetrieved because the eval arms call that
	// directly and measure ordering: adding a payload lookup to it would make
	// every arm pay for a field no arm reads. Recall is where a session meets a
	// memory, so it is the route that has to carry the reason — a session about to
	// redo a rejected thing does not know to ask for history.
	if err := s.attachSupersedesToHits(searchCtx, teamID, results); err != nil {
		// FAIL CLOSED, unlike the fact block below, and the difference is that this
		// one is an invariant rather than an enrichment. T5 states "the ending
		// REASON always reaches the default route" without qualification; a warning
		// logged server-side and a page that silently omits it is that invariant
		// quietly false, and the reader has no way to tell a record that replaced
		// nothing from one whose lineage lookup failed.
		//
		// It is one indexed query against the page's own roots, so a failure here
		// means something is wrong that a recall should not paper over.
		parent.End(telemetry.FailedClosed)
		return SearchResult{}, fmt.Errorf("resolve what these records replaced: %w", err)
	}

	// The fact block is assembled AFTER ranking and never feeds it. A failure
	// here degrades the answer to drawers-only rather than failing the recall:
	// the drawers are what a caller had before this existed, and losing them
	// because the graph refused would be the worse trade.
	// Entities come from the RANKED PAGE, not the candidate pool. `rows` is the
	// pool searchCandidates fetched — limit*3, so 30 drawers at the shipped
	// limit of 10 — and reading entities from all of it made the fact lookup pay
	// for candidates the caller will never see. The page is also the more correct
	// source: a fact should relate to what was actually returned.
	pageRows := make(map[string]Drawer, len(results))
	for _, h := range results {
		pageRows[h.Drawer.ID] = h.Drawer
	}
	facts, factsErr := s.factsFor(searchCtx, teamID, q.Wing, vec, pageRows)
	if factsErr != nil {
		slog.WarnContext(ctx, "fact block not assembled; recall degraded to drawers only", "err", factsErr)
		facts = FactBlock{}
	}

	_, rec := telemetry.Start(searchCtx, telemetry.StageRecord)
	if q.SkipTelemetry {
		rec.End(telemetry.Bypassed, telemetry.AttrReason(telemetry.ReasonSkipSQLite))
		parent.Set(attribute.Int("am.count", len(results)))
		return SearchResult{SearchID: searchID, Hits: results, Facts: facts}, nil
	}
	ev := searchEventRow{
		ID: searchID, TeamID: teamID, Wing: q.Wing, Room: q.Room, Query: query,
		// Whether reranking HAPPENED, not whether a reranker exists. The previous
		// value was boolToInt(s.rerank != nil), so at weight 0 — where
		// applyRerankWith returns before scoring anything — every event claimed a
		// cross-encoder pass that never ran. ADR-001 calibrates its abstention
		// threshold from these rows.
		Candidates: len(hits), Hits: len(results), Reranked: boolToInt(reranked),
		// WHY it was not reranked, beside WHETHER it was. This is the line that
		// SELECTS the value T1 returns: without it the reason is computed, put on
		// the span, and never reaches a row anyone can aggregate.
		RerankSkipReason: skipReason,
	}
	if len(results) > 0 {
		ev.TopScore = results[0].Score
		// The signal an abstention threshold can actually use. TopScore above is the
		// FUSED score, and under rrf that is 1/(60+rank) — rank, not quality.
		ev.TopRerankScore = results[0].RerankScore
	}
	s.repo.recordSearch(ctx, ev)
	rec.End(telemetry.Ran, attribute.Int("am.count", len(results)))
	parent.Set(attribute.Int("am.count", len(results)))

	return SearchResult{SearchID: searchID, Hits: results, Facts: facts}, nil
}

// Search is SearchPage's hits without the page's identity, kept because most
// callers want exactly that and because widening every one of them — 66 test
// call sites among them — would have been a large mechanical diff for a
// page-level field two of them need.
//
// It is a projection of SearchPage, not a second implementation: there is one
// ranking path, no flag selects between them, and this function cannot diverge
// because it does nothing but drop a field. Reach for SearchPage when the caller
// needs to name the recall it just ran.
func (s *Service) Search(ctx context.Context, teamID string, q SearchQuery) ([]SearchHit, error) {
	page, err := s.SearchPage(ctx, teamID, q)
	return page.Hits, err
}

// rankRetrieved is the one ranking pipeline. Search retrieves then calls it.
// Eval arms that reconstruct a served configuration call it on a Clone rather
// than reimplementing fusion, closet boost, recency, rerank, or collapse.
// The third return says WHETHER a cross-encoder ordered the page and the fourth
// says WHY it did not — empty when it did. Both travel to recordSearch, because
// `reranked` alone is 0 for a disabled reranker and a failing one alike.
//
// stale marks a page served while the search index was behind its source of
// truth (ADR-033), and rides onto every hit.
func (s *Service) rankRetrieved(ctx context.Context, teamID, query string, q SearchQuery, vec []float32, hits []store.Hit, rows map[string]Drawer, stale bool) ([]SearchHit, bool, string, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	// Keep the survivors that pass the wing/room/max-distance filters, in vector
	// order, carrying content (for BM25) and distance (for vector similarity).
	// The wing/room comparisons are redundant when the index honoured the filter
	// above, and deliberately kept: the drawer row is the truth about where a
	// drawer lives, and a stale index must never surface another wing's memory.
	//
	// ONE spelling of that rule, shared with searchCandidates — which needs the
	// same filter to know how many DISTINCT memories a widening round found, and
	// therefore whether to widen again. Two copies of a scope predicate is how a
	// stale one survives, so both paths call survivorsFrom instead.
	survivors, _, drops := survivorsFrom(hits, rows, q, stale)
	// Recorded HERE and nowhere else: this is the single call over the final pool.
	// Annotate paints the span currently on ctx — the am.search parent for a served
	// recall, an eval arm's own span for an arm — which is the correct per-caller
	// attribution, and the reason the counts are returned rather than taken inside
	// the (pure, context-free) predicate.
	if drops.Any() {
		telemetry.Annotate(ctx,
			attribute.Int("am.dropped_orphan", drops.Orphan),
			attribute.Int("am.dropped_out_of_scope", drops.OutOfScope),
			attribute.Int("am.dropped_over_distance", drops.OverDistance),
			attribute.Int("am.dropped_superseded", drops.Superseded))
	}

	// Collapse HERE, before scoring, so every consumer of rankRetrieved ranks
	// memories. Eval pool arms call this on a Clone: an arm reconstructing the
	// served configuration has to collapse the same way or it is measuring a
	// pipeline nobody runs.
	_, collapseSpan := telemetry.Start(ctx, telemetry.StageCollapse)
	if len(survivors) == 0 {
		collapseSpan.End(telemetry.Bypassed, telemetry.AttrReason(telemetry.ReasonEmpty), attribute.Int("am.count", 0))
	} else {
		collapsed, err := s.collapseCandidatesToMemories(ctx, teamID, q, survivors)
		if err != nil {
			collapseSpan.End(telemetry.FailedClosed, telemetry.AttrReason(telemetry.ReasonError))
			return nil, false, "", err
		}
		collapseSpan.End(telemetry.Ran, attribute.Int("am.count", len(collapsed)))
		survivors = collapsed
	}

	// Closet boost: search the team's closets with the same query and let the
	// best-matching closets lift the rank of the drawers from their source. Closets
	// are a SIGNAL, never a gate — a team that has never mined has no closets, so a
	// failed or empty closet search simply yields no boosts and search proceeds.
	closetBoostBySource := s.closetBoosts(ctx, teamID, vec)

	docs := make([]string, len(survivors))
	dists := make([]float64, len(survivors))
	boosts := make([]float64, len(survivors))
	dates := make([]string, len(survivors))
	for i, h := range survivors {
		docs[i] = h.rankingContent(query, false)
		dists[i] = h.Distance
		boosts[i] = closetBoostBySource[h.Drawer.SourceFile]
		dates[i] = h.Drawer.ContentDate
	}
	_, fusionSpan := telemetry.Start(ctx, telemetry.StageFusion, s.fusionAttrs()...)
	ranked := s.fusionRanker()(query, docs, dists, boosts)
	fusionSpan.End(telemetry.Ran, attribute.Int("am.count", len(ranked)))

	_, recencySpan := telemetry.Start(ctx, telemetry.StageRecency)
	if s.recencyBand > 0 {
		ranked = reorderByRecency(ranked, dates, s.recencyBand)
		recencySpan.End(telemetry.Ran, attribute.Float64("am.band", s.recencyBand))
	} else {
		recencySpan.End(telemetry.Bypassed, telemetry.AttrReason(telemetry.ReasonBandZero))
	}

	// Stage 4: cross-encode the shortlist. The fusion above is a cheap proxy built
	// from a query vector and term overlap; a cross-encoder reads the query and
	// the document together and is the better judge of MEANING — but the fused
	// score is the better judge of VOCABULARY, and a query naming an identifier
	// leans on exactly that. So the two are blended rather than one replacing the
	// other, and both are reported.
	ranked, reranked, skipReason := s.applyRerank(ctx, q.rerankQuery(query), query, vec, survivors, ranked)

	// A page is a page of MEMORIES. Chunks of one memory are similar to the same
	// query, so without this they cluster and crowd each other out: measured on a
	// live palace at limit 10, one query spent 2 slots on a single memory and
	// another spent 4 slots on two, the duplicates landing adjacent.
	//
	// The BEST-ranked chunk survives, not chunk 0, because the surviving chunk is
	// the one that matched and its snippet is the passage the caller asked for.
	// Ranking happens first for the same reason: collapsing earlier would score
	// whichever chunk happened to be picked, so a memory's rank would depend on
	// the order it was fetched in.
	//
	// A short page is the honest answer when the pool holds fewer than `limit`
	// distinct memories. Padding it with a second chunk of a memory already shown
	// is exactly what this removes.
	results := make([]SearchHit, 0, limit)
	slotOf := make(map[string]int, limit)
	for _, r := range ranked {
		hit := survivors[r.Index]
		mem := hit.MemoryID
		if mem == "" {
			mem = memoryOf(hit.Drawer)
		}
		if i, seen := slotOf[mem]; seen {
			results[i].ChunksMatched++
			continue
		}
		if len(results) >= limit {
			continue // keep counting chunks of memories already on the page
		}
		hit.Score = r.Fused
		hit.BM25 = r.BM25
		hit.ClosetBoost = r.Boost
		hit.RerankScore, hit.Reranked, hit.Blended = r.Rerank, r.Reranked, r.Blended
		if hit.ChunksMatched == 0 {
			hit.ChunksMatched = 1
		}
		slotOf[mem] = len(results)
		results = append(results, hit)
	}

	return results, reranked, skipReason, nil
}

// candidateKFor is how many vector neighbours a search fetches.
//
// A cross-encoder can only promote what retrieval surfaced, so widening the pool
// it sees is where the accuracy comes from — not from the scoring alone. But it
// widens only when the cross-encoder will actually RUN: at weight 0,
// applyRerankWith returns before scoring anything, so a configured reranker
// bought a wider fetch and a bigger GetMany join on every search and cross-
// encoded none of it.
//
// It is a function rather than a branch inline so the rule can be driven by a
// test; the inline version was correct and unfalsifiable.
func candidateKFor(limit int, rerankConfigured bool, rerankPool int, rerankWeight float64) int {
	k := limit * hybridCandidateMultiplier
	if rerankConfigured && rerankWeight > 0 && k < rerankPool {
		k = rerankPool
	}
	return k
}

// withRetrieveFloors raises k to the largest positive floor. Zero and negative
// floors are ignored, so a caller cannot shrink the fetch below candidateKFor.
func withRetrieveFloors(k int, floors ...int) int {
	for _, f := range floors {
		if f > k {
			k = f
		}
	}
	return k
}

// boolToInt maps a flag onto the INTEGER column SQLite uses for booleans.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// applyRerank cross-encodes the best rerankPool candidates and returns ranked
// reordered by cross-encoder score, with each reordered entry's Rerank set. The
// tail beyond the pool keeps its fused order and a zero Rerank — it was never
// scored, and pretending otherwise would put an unscored drawer above a scored
// one.
//
// It fails OPEN: with no reranker configured, nothing to score, or any error
// from the endpoint, ranked is returned untouched and search proceeds on the
// hybrid order. That mirrors the closet boost's rule that a ranking input is a
// signal, never a gate — a reranker that is down or slow must degrade recall,
// never break it.
func (s *Service) applyRerank(ctx context.Context, rerankQuery, evidenceQuery string, queryVector []float32, survivors []SearchHit, ranked []HybridScore) ([]HybridScore, bool, string) {
	return s.applyRerankWith(ctx, rerankQuery, evidenceQuery, queryVector, survivors, ranked, s.rerankWeight)
}

// applyRerankWith is applyRerank at an explicit blend weight, so the eval can
// measure what the weight is worth instead of the default being someone's taste.
//
// The two signals know different things: the cross-encoder reads the query and
// the document together, which the embedder never did, and the fused score
// carries the lexical evidence, which a cross-encoder logit does not
// distinguish. Blending keeps both; handing over discards one.
// The third return is WHY the cross-encoder did not order the page — one of
// telemetry's reason constants, or "" when it did. It is the same value the span
// carries, computed once and handed to both, because a trace and a durable row
// disagreeing about one recall is the defect ADR-034 exists to prevent.
func (s *Service) applyRerankWith(ctx context.Context, rerankQuery, evidenceQuery string, queryVector []float32, survivors []SearchHit, ranked []HybridScore, weight float64) ([]HybridScore, bool, string) {
	rerankCtx, sp := telemetry.Start(ctx, telemetry.StageRerank, attribute.Float64("am.weight", weight))
	switch {
	case s.rerank == nil:
		sp.End(telemetry.Bypassed, telemetry.AttrReason(telemetry.ReasonNoReranker))
		return ranked, false, telemetry.ReasonNoReranker
	case len(ranked) == 0:
		sp.End(telemetry.Bypassed, telemetry.AttrReason(telemetry.ReasonEmpty))
		return ranked, false, telemetry.ReasonEmpty
	case weight <= 0:
		sp.End(telemetry.Bypassed, telemetry.AttrReason(telemetry.ReasonWeightZero))
		return ranked, false, telemetry.ReasonWeightZero
	}
	pool := min(s.rerankPool, len(ranked))
	docs := make([]string, pool)
	for i := range docs {
		hit := survivors[ranked[i].Index]
		docs[i] = hit.rankingContent(evidenceQuery, hit.MemoryContent != "")
	}
	_, evSpan := telemetry.Start(rerankCtx, telemetry.StageEvidence)
	if s.MemoryEvidenceSelectorName() == semanticMemoryEvidenceSelector {
		semanticDocs, err := s.semanticRerankDocuments(rerankCtx, evidenceQuery, queryVector, survivors, ranked[:pool], docs)
		if err != nil {
			slog.Warn("semantic evidence selection failed, falling back to lexical evidence", "error", err, "candidates", pool)
			evSpan.End(telemetry.FailedOpen, telemetry.AttrReason(telemetry.ReasonSemanticFailed))
		} else {
			docs = semanticDocs
			evSpan.End(telemetry.Ran, attribute.Int("am.pool", pool))
		}
	} else {
		evSpan.End(telemetry.Bypassed, telemetry.AttrReason(telemetry.ReasonLexical))
	}

	scores, err := s.rerank.Rerank(rerankCtx, rerankQuery, docs)
	if err != nil {
		// A degraded reranker returns FALSE, deliberately. It failed open and the
		// page is the fused order, so a telemetry row claiming a cross-encoder pass
		// would be exactly as wrong as the weight-0 case this fix is about.
		// A blown budget and a sick endpoint both fail open, but they are not the
		// same incident and they do not have the same fix, so they must not share a
		// reason code.
		//
		// This asks the RERANKER whether its own budget was the binding constraint,
		// rather than inspecting the error's shape. An earlier version tested
		// net.Error.Timeout(), and a review on 2026-08-26 showed why that cannot
		// hold the promise: http.Client.Timeout covers DNS, connect, TLS and header
		// reads, so a stalled resolver or an unroutable endpoint answers Timeout()
		// true and was reported as a capacity signal — sending an operator to lower
		// the pool on a reranker that is simply not there.
		reason := telemetry.ReasonError
		var budget rerankBudgetExceeded
		if errors.As(err, &budget) && budget.RerankBudgetExceeded() {
			reason = telemetry.ReasonTimeout
		}
		slog.Warn("rerank failed, falling back to hybrid order", "error", err, "candidates", pool, "reason", reason)
		sp.End(telemetry.FailedOpen, telemetry.AttrReason(reason), attribute.Int("am.pool", pool))
		return ranked, false, reason
	}
	if len(scores) != pool {
		slog.Warn("rerank returned the wrong number of scores", "want", pool, "got", len(scores))
		sp.End(telemetry.FailedOpen, telemetry.AttrReason(telemetry.ReasonScoreCount), attribute.Int("am.pool", pool), attribute.Int("am.got", len(scores)))
		return ranked, false, telemetry.ReasonScoreCount
	}
	sp.End(telemetry.Ran, attribute.Int("am.pool", pool))
	// Empty, not a "ran" sentinel: the column T2 fills must stay empty on the rows
	// where nothing was skipped, or it measures nothing.
	return BlendRerankWith(ranked, scores, weight, s.RerankNormName()), true, ""
}

// RerankBudget reports the ceiling the configured reranker enforces on a
// complete call, or 0 when there is no reranker or it enforces none. It is what
// puts am.rerank_timeout on the search span, so a trace showing a rerank that
// took 11s can be read against the budget that was actually in force.
func (s *Service) RerankBudget() time.Duration {
	d, ok := s.rerank.(RerankDescriber)
	if !ok {
		return 0
	}
	return d.RerankBudget()
}

// RerankScoresFor fetches cross-encoder scores for the head of a fused ranking,
// or nil when there is no reranker or the call fails. The caller blends them with
// BlendRerank, possibly several times at different weights, without paying for
// the inference again.
func (s *Service) RerankScoresFor(ctx context.Context, query string, survivors []SearchHit, ranked []HybridScore) []float64 {
	if s.rerank == nil || len(ranked) == 0 {
		return nil
	}
	pool := min(s.rerankPool, len(ranked))
	docs := make([]string, pool)
	for i := range docs {
		docs[i] = survivors[ranked[i].Index].Drawer.Content
	}
	scores, err := s.rerank.Rerank(ctx, query, docs)
	if err != nil {
		slog.Warn("rerank failed, falling back to hybrid order", "error", err, "candidates", pool)
		return nil
	}
	if len(scores) != pool {
		slog.Warn("rerank returned the wrong number of scores", "want", pool, "got", len(scores))
		return nil
	}
	return scores
}

// BlendRerank combines a fused ranking with cross-encoder scores already
// obtained for its head, at the given weight.
//
// It is separate from the call that fetches those scores because the scores do
// not depend on the weight: an eval comparing several weights was calling the
// cross-encoder once per weight with identical inputs, which multiplied the
// slowest step in the pipeline by the number of arms for no information at all.
// Rerank-score normalisers. The name is what an eval arm selects and what
// am_status reports, so it is operator-facing and must stay stable.
const (
	// RerankNormMinMax rescales the pool's rerank scores to [0,1] by min-max.
	// It is the original behaviour and is SCALE-FREE: a pool whose scores differ
	// by 0.001 and one whose scores differ by 10 both come out spanning the full
	// range, so a cross-encoder that is indifferent is indistinguishable from one
	// that is certain. On a small pool it also forces the extremes to exactly
	// {0,1}, which at weight 0.5 makes an opposed pair tie and discards the
	// cross-encoder entirely.
	RerankNormMinMax = "minmax"
	// RerankNormSigmoid maps each raw logit through 1/(1+e^-x) independently of
	// the pool. It PRESERVES MAGNITUDE: indifferent scores land together near 0.5
	// and contribute almost nothing, so the fused evidence decides — which is the
	// honest reading of "the cross-encoder has no opinion" — while a confident
	// score still separates. It imports a scale assumption, since a logit is not a
	// calibrated probability, so it is measured as an arm rather than assumed.
	RerankNormSigmoid = "sigmoid"
	// RerankNormRank uses position alone, ignoring score magnitude. It cannot
	// amplify noise, because a 0.001 gap and a 10.0 gap produce the same steps —
	// but it still forces the extremes to {0,1}, so it does NOT fix the tie. It is
	// here to separate the two halves of the defect in the measurement.
	RerankNormRank = "rank"
	// DefaultRerankNorm is the served policy. It is sigmoid rather than min-max
	// because min-max is scale-free on BOTH axes: it cannot distinguish a
	// cross-encoder that is certain from one that is indifferent, and on a small
	// pool at weight 0.5 it makes an opposed pair tie, discarding the
	// cross-encoder's verdict entirely. Measured on this stack 2026-08-25 — a
	// served page returned two hits both at blended_score 0.5000 while the closest
	// hit by cosine distance was placed last.
	DefaultRerankNorm = RerankNormSigmoid
)

// normalizeSigmoid maps raw cross-encoder logits into (0,1) per element.
//
// Unlike normalizeScores this is POOL-INDEPENDENT, which is the entire point: a
// candidate's normalised value does not change because a different candidate was
// retrieved alongside it, so the blend can tell "all of these look equally good
// to me" from "this one is clearly best".
func normalizeSigmoid(in []float64) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = 1 / (1 + math.Exp(-v))
	}
	return out
}

// normalizeRank replaces each score with its position in the pool, best = 1.
//
// Scale-free by construction, so it cannot turn a rounding difference into a
// decisive one. It still maps the best and worst to 1 and 0, so it does not cure
// the weight-0.5 tie; the two failures are separable and this arm separates them.
func normalizeRank(in []float64) []float64 {
	out := make([]float64, len(in))
	if len(in) < 2 {
		for i := range out {
			out[i] = 1
		}
		return out
	}
	idx := make([]int, len(in))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return in[idx[a]] > in[idx[b]] })
	last := float64(len(in) - 1)
	for pos, i := range idx {
		out[i] = 1 - float64(pos)/last
	}
	return out
}

// normalizeBlendAxes applies the named policy to both blend inputs, defaulting to
// min-max on both so an unset field reproduces the original behaviour exactly.
func normalizeBlendAxes(rerank, fused []float64, norm string) (rerankNorm, fusedNorm []float64) {
	switch norm {
	case RerankNormSigmoid:
		// Magnitude-preserving on both: a logit through sigmoid, an RRF score
		// scaled by the pool max. An indifferent cross-encoder lands flat near 0.5
		// and lets the fused evidence decide; a confident one still separates.
		return normalizeSigmoid(rerank), normalizeByMax(fused)
	case RerankNormRank:
		// Position-only on both: cannot amplify noise, and cannot express
		// confidence either. It is the control that separates "magnitude mattered"
		// from "getting off min-max mattered".
		return normalizeRank(rerank), normalizeRank(fused)
	case RerankNormMinMax:
		return normalizeScores(rerank), normalizeScores(fused)
	default:
		return normalizeBlendAxes(rerank, fused, DefaultRerankNorm)
	}
}

// normalizeByMax scales a non-negative vector by its maximum, preserving ratios.
//
// This is the fused axis's counterpart to sigmoid. RRF scores are all near
// 1/(k+rank) and therefore CLOSE TOGETHER in absolute terms; min-max stretches
// whatever gap exists to the full [0,1] range, so two candidates differing by 1.6%
// arrive at the blend looking as far apart as first and last. Dividing by the max
// keeps a 1.6% difference a 1.6% difference.
func normalizeByMax(in []float64) []float64 {
	out := make([]float64, len(in))
	mx := 0.0
	for _, v := range in {
		if v > mx {
			mx = v
		}
	}
	if mx <= 0 {
		for i := range out {
			out[i] = 1
		}
		return out
	}
	for i, v := range in {
		out[i] = v / mx
	}
	return out
}

// BlendRerankWith is BlendRerank under a named normalisation POLICY.
//
// The policy governs BOTH axes, because normalising only one is not a smaller
// change — it is an incoherent one. Measured while building the fixture for
// ADR-030: sigmoid on the rerank axis alone turned a dead tie into 0.5033 vs
// 0.4967 and STILL ordered the page against a cross-encoder that had asked for
// the opposite by the widest margin it can express, because the fused axis was
// still being min-max stretched to {1, 0}. Both axes are scale-free or neither is.
func BlendRerankWith(ranked []HybridScore, scores []float64, weight float64, norm string) []HybridScore {
	pool := len(scores)
	if pool == 0 || pool > len(ranked) {
		return ranked
	}
	fusedRaw := make([]float64, pool)
	for i := range fusedRaw {
		fusedRaw[i] = ranked[i].Fused
	}
	rerankNorm, fusedNorm := normalizeBlendAxes(scores, fusedRaw, norm)

	head := make([]HybridScore, pool)
	for i := range head {
		head[i] = ranked[i]
		head[i].Rerank, head[i].Reranked = scores[i], true
		head[i].Blended = weight*rerankNorm[i] + (1-weight)*fusedNorm[i]
	}
	sort.SliceStable(head, func(a, b int) bool { return head[a].Blended > head[b].Blended })
	return append(head, ranked[pool:]...)
}

func BlendRerank(ranked []HybridScore, scores []float64, weight float64) []HybridScore {
	pool := len(scores)
	if pool == 0 || pool > len(ranked) {
		return ranked
	}

	// Normalize both terms within this page before combining them: a
	// cross-encoder logit and a fused [0,1] score are not comparable numbers, and
	// adding them raw would let whichever has the wider range decide everything.
	rerankNorm := normalizeScores(scores)
	fusedRaw := make([]float64, pool)
	for i := range fusedRaw {
		fusedRaw[i] = ranked[i].Fused
	}
	fusedNorm := normalizeScores(fusedRaw)

	head := make([]HybridScore, pool)
	for i := range head {
		head[i] = ranked[i]
		head[i].Rerank, head[i].Reranked = scores[i], true
		head[i].Blended = weight*rerankNorm[i] + (1-weight)*fusedNorm[i]
	}
	// Stable so equal blended scores keep the fused order as the tie-break,
	// exactly as rankHybrid keeps the vector order.
	sort.SliceStable(head, func(a, b int) bool { return head[a].Blended > head[b].Blended })
	return append(head, ranked[pool:]...)
}

// normalizeScores min-max scales a slice into [0,1]. An all-equal slice maps to
// 1 everywhere: there is nothing to choose between, so this term should not
// reorder anything.
func normalizeScores(in []float64) []float64 {
	out := make([]float64, len(in))
	if len(in) == 0 {
		return out
	}
	min, max := in[0], in[0]
	for _, v := range in {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	span := max - min
	for i, v := range in {
		if span == 0 {
			out[i] = 1
			continue
		}
		out[i] = (v - min) / span
	}
	return out
}

// DuplicateResult is the check_duplicate verdict: whether the most similar
// existing drawer crosses the threshold, that similarity, and the match (nil
// when nothing is similar enough).
type DuplicateResult struct {
	IsDuplicate bool
	Similarity  float64
	Drawer      *Drawer
}

// CheckDuplicate reports whether content is near-identical to an existing drawer.
// It embeds the content, takes the single nearest neighbour, and compares its
// cosine similarity to threshold (callers pass DefaultDupThreshold when unset).
func (s *Service) CheckDuplicate(ctx context.Context, teamID, content string, threshold float64) (DuplicateResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return DuplicateResult{}, fmt.Errorf("%w: content is required", ErrInvalidInput)
	}
	// Cosine similarity lives in [-1, 1]; a duplicate threshold outside [0, 1] is
	// nonsense (>1 can never match an exact duplicate, <0 marks everything a
	// duplicate), so clamp it rather than trust a stray argument.
	if threshold < 0 {
		threshold = 0
	}
	if threshold > 1 {
		threshold = 1
	}
	vec, err := s.embed.EmbedOne(ctx, content)
	if err != nil {
		return DuplicateResult{}, fmt.Errorf("embed content: %w", err)
	}
	// duplicateProbeK, not 1. An ended drawer keeps its vector, so the nearest
	// neighbour can be a record the team retracted — and asking for exactly one
	// then discarding it would report "no duplicate" while a current near-identical
	// memory sat second. The whole point of this tool is to stop an agent filing
	// something the palace already holds, so a false negative is the expensive
	// direction.
	searchRes, err := s.vectors.Search(ctx, teamID, vec, duplicateProbeK, nil)
	if err != nil {
		return DuplicateResult{}, fmt.Errorf("vector search: %w", err)
	}
	for _, top := range searchRes.H {
		d, err := s.repo.Get(ctx, teamID, top.ID)
		if err != nil {
			continue // orphan vector: the row is gone, so it duplicates nothing
		}
		if d.ValidTo != "" {
			continue // retracted: the team stopped asserting this, so re-filing it is not a duplicate
		}
		sim := float64(top.Score)
		res := DuplicateResult{IsDuplicate: sim >= threshold, Similarity: sim}
		if res.IsDuplicate {
			res.Drawer = &d
		}
		return res, nil
	}
	return DuplicateResult{IsDuplicate: false}, nil
}

// duplicateProbeK is how deep check_duplicate looks for the nearest CURRENT
// neighbour. Small on purpose: it answers "is this already filed", and a
// retracted record ahead of a current one is the only case that needs the extra
// depth. Ten covers a memory superseded several times over without turning a
// cheap probe into a recall.
const duplicateProbeK = 10

// Taxonomy is the get_taxonomy view: every wing with its rooms and counts.
type Taxonomy struct {
	Wings []TaxonomyWing `json:"wings"`
}

// TaxonomyWing is one wing in the taxonomy: its totals and the rooms inside it.
type TaxonomyWing struct {
	Wing string `json:"wing"`
	// Drawers is current rows; Memories is items. See WingStat for why both are
	// reported rather than one replacing the other.
	Drawers  int        `json:"drawers"`
	Memories int        `json:"memories"`
	Rooms    []RoomStat `json:"rooms"`
}

// GetTaxonomy assembles the wing -> rooms tree from the two indexed
// aggregations, so an agent can see the shape of a team's memory before searching.
func (s *Service) GetTaxonomy(ctx context.Context, teamID string) (Taxonomy, error) {
	wings, err := s.repo.Wings(ctx, teamID)
	if err != nil {
		return Taxonomy{}, err
	}
	rooms, err := s.repo.Rooms(ctx, teamID, "")
	if err != nil {
		return Taxonomy{}, err
	}
	byWing := make(map[string][]RoomStat, len(wings))
	for _, r := range rooms {
		byWing[r.Wing] = append(byWing[r.Wing], r)
	}
	tax := Taxonomy{Wings: make([]TaxonomyWing, 0, len(wings))}
	for _, w := range wings {
		tax.Wings = append(tax.Wings, TaxonomyWing{
			Wing:     w.Wing,
			Drawers:  w.Drawers,
			Memories: w.Memories,
			Rooms:    byWing[w.Wing],
		})
	}
	return tax, nil
}

// Wings and Rooms expose the list_wings / list_rooms aggregations directly.
func (s *Service) Wings(ctx context.Context, teamID string) ([]WingStat, error) {
	return s.repo.Wings(ctx, teamID)
}

// Rooms lists a team's rooms, optionally within one wing.
func (s *Service) Rooms(ctx context.Context, teamID, wing string) ([]RoomStat, error) {
	return s.repo.Rooms(ctx, teamID, wing)
}

// ClosetsByWing lists one wing's closets — the pointer index built by mining.
// It completes the read surface a wing export needs (drawers, closets, tunnels,
// wing stats), so one *Service satisfies both halves of a wing transfer rather
// than callers having to hold a separate repository handle for this one query.
func (s *Service) ClosetsByWing(ctx context.Context, teamID, wing string) ([]Closet, error) {
	return s.repo.ClosetsByWing(ctx, teamID, wing)
}

// Reconnect re-readies a tenant's vector namespace and confirms the store is
// reachable. The Python tool invalidated a cached Qdrant client; this server is
// stateless (no per-session cache), so reconnect has no client to drop — it is
// instead a cheap liveness probe agents can call to verify the backend before a
// burst of writes. EnsureNamespace is idempotent, so re-running it is safe.
func (s *Service) Reconnect(ctx context.Context, teamID string) error {
	if err := s.vectors.EnsureNamespace(ctx, teamID, s.dim); err != nil {
		return fmt.Errorf("reconnect: vector store unreachable: %w", err)
	}
	return nil
}

// DiaryWriteInput is the diary_write payload: whose journal (Agent), the AAAK
// entry text, an optional Topic (defaulting to DefaultDiaryTopic), and an
// optional Wing (defaulting to the agent's own wing).
type DiaryWriteInput struct {
	Agent string
	Entry string
	Topic string
	Wing  string
}

// DiaryWriteResult reports what diary_write filed: the logical entry id (the
// first chunk's id), the normalized agent and topic, the entry's timestamp, how
// many chunks it became, and — only when it chunked — every physical chunk id.
type DiaryWriteResult struct {
	EntryID   string
	Agent     string
	Topic     string
	Timestamp string
	Chunks    int
	ChunkIDs  []string
	// PendingEmbedding is true when the entry is durable but not yet searchable
	// because the embedder could not be reached; the background worker will index
	// it. See AddResult for why this is surfaced rather than swallowed.
	PendingEmbedding bool
}

// WriteDiary files an agent's journal entry. It mirrors the frozen tool: the
// agent name is lowercased (so reads are case-insensitive, #1243), the topic
// defaults to "general", and the wing defaults to the agent's own wing
// (wing_<agent>) unless one is supplied. The entry rides the same chunk -> embed
// -> store machinery as add_drawer, but — unlike add_drawer's content-hashed,
// idempotent ids — each diary id folds in the write timestamp, so journaling the
// *same* reflection twice keeps both entries instead of overwriting one: a
// journal is append-only. (The frozen tool used a non-idempotent add for exactly
// this reason; the timestamp seed makes a same-id collision effectively
// impossible, so reusing the idempotent upsert store path is safe.)
func (s *Service) WriteDiary(ctx context.Context, teamID string, in DiaryWriteInput) (DiaryWriteResult, error) {
	agent, err := SanitizeName(in.Agent, "agent_name")
	if err != nil {
		return DiaryWriteResult{}, err
	}
	agent = strings.ToLower(agent)

	entry, err := SanitizeContent(in.Entry)
	if err != nil {
		return DiaryWriteResult{}, err
	}

	topic := in.Topic
	if strings.TrimSpace(topic) == "" {
		topic = DefaultDiaryTopic
	}
	if topic, err = SanitizeName(topic, "topic"); err != nil {
		return DiaryWriteResult{}, err
	}

	wing := strings.TrimSpace(in.Wing)
	if wing == "" {
		// Default to the agent's own wing. The agent is already sanitized and
		// lowercased; spaces become underscores so the result still satisfies the
		// safe-name pattern (underscores are legal in a name's interior).
		wing = "wing_" + strings.ReplaceAll(agent, " ", "_")
	} else if wing, err = SanitizeName(wing, "wing"); err != nil {
		return DiaryWriteResult{}, err
	}

	// One timestamp per write: it stamps every chunk's FiledAt (so diary_read can
	// order entries newest-first) and seeds the id (so the entry is unique).
	// RFC3339Nano gives enough resolution that two successive writes never collide.
	now := time.Now().UTC()
	filedAt := now.Format(diaryTimeLayout)
	date := now.Format("2006-01-02")
	// seed makes the id unique per write: the timestamp orders entries, the random
	// nonce guarantees uniqueness even if two writes (e.g. on two scaled instances)
	// land on the same nanosecond — without it a same-ns, same-content write would
	// collide and the idempotent store upsert would silently overwrite a prior
	// journal entry. The clean filedAt (no nonce) is what stamps FiledAt for sorting.
	seed := diarySeed(filedAt)

	// SanitizeContent guarantees a non-empty entry, so diaryChunks yields >= 1
	// chunk and drawers[0] below is always present. diaryChunks (not ChunkText)
	// keeps the journal entry verbatim — no overlap, no trim — matching the frozen
	// tool. EntryID is the first chunk's id (our ParentID model makes chunk 0 the
	// canonical, fetchable handle); the frozen tool's logical handle was opaque and
	// un-fetchable, but for the common single-chunk AAAK entry the two coincide.
	chunks := diaryChunks(entry, ChunkSize)
	vectors := s.embedOrDefer(ctx, chunks)

	drawers := make([]Drawer, len(chunks))
	for i, c := range chunks {
		parentID := ""
		if i > 0 {
			parentID = drawers[0].ID
		}
		drawers[i] = Drawer{
			ID: diaryEntryID(teamID, wing, agent, topic, c.Index, c.Content, seed),
			// Through contentKeyFor, NOT a hardcoded "". Both produce an empty key
			// for a diary row, and that is exactly the problem with the literal: it
			// makes contentKeyFor's diary branch dead on this path, so a mutant that
			// deletes the branch survives — measured 2026-08-27, the fence passed
			// with the exemption removed. One rule, one place that states it.
			ContentKey: contentKeyFor(Drawer{TeamID: teamID, Wing: wing, Room: DiaryRoom, ChunkIndex: c.Index, Content: c.Content}),
			TeamID:     teamID,
			Wing:       wing,
			Room:       DiaryRoom,
			ChunkIndex: c.Index,
			Content:    c.Content,
			// Per CHUNK, and the same extractor Add and Mine use. A diary entry is
			// the richest source the derived graph has — a session summary names
			// the systems that MET, which is exactly what a hallway is made of —
			// and it was the last write path filing memories the graph never saw.
			Entities:    extractEntities(c.Content),
			FiledAt:     filedAt,
			ContentDate: date,
			ParentID:    parentID,
			Agent:       agent,
			Topic:       topic,
		}
	}
	pending := vectors == nil
	if pending {
		if err := s.repo.SaveUnembedded(ctx, drawers); err != nil {
			return DiaryWriteResult{}, fmt.Errorf("save diary entry (embedding deferred): %w", err)
		}
	} else if err := s.storeDrawers(ctx, teamID, drawers, vectors); err != nil {
		return DiaryWriteResult{}, err
	}

	res := DiaryWriteResult{
		PendingEmbedding: pending,
		EntryID:          drawers[0].ID,
		Agent:            agent,
		Topic:            topic,
		Timestamp:        filedAt,
		Chunks:           len(drawers),
	}
	// A single-chunk entry's id is already EntryID; only a chunked entry needs its
	// physical ids enumerated so a caller can fetch each piece by id.
	if len(drawers) > 1 {
		res.ChunkIDs = make([]string, len(drawers))
		for i, d := range drawers {
			res.ChunkIDs[i] = d.ID
		}
	}
	return res, nil
}

// diarySeed combines the write timestamp with a random nonce to seed a diary
// id, so the id is unique even when two writes share a nanosecond. crypto/rand is
// the source; on the near-impossible event it fails, we fall back to the
// timestamp alone rather than block a journal write — at worst reintroducing the
// vanishingly small same-nanosecond collision the nonce exists to remove.
func diarySeed(filedAt string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return filedAt
	}
	return filedAt + "|" + hex.EncodeToString(b[:])
}

// DiaryEntry is one entry diary_read returns: when it was written, its topic, and
// the verbatim text — the read projection of a diary Drawer.
type DiaryEntry struct {
	Date      string `json:"date"`
	Timestamp string `json:"timestamp"`
	Topic     string `json:"topic"`
	Content   string `json:"content"`
}

// DiaryReadResult is the diary_read response: the normalized agent, the page of
// entries (newest first), the total entries in scope, and how many are shown.
type DiaryReadResult struct {
	Agent   string       `json:"agent"`
	Entries []DiaryEntry `json:"entries"`
	Total   int64        `json:"total"`
	Showing int          `json:"showing"`
}

// ReadDiary returns an agent's most recent diary entries, newest first. Like the
// frozen tool it lowercases the agent (case-insensitive reads), clamps lastN to
// [1, MaxDiaryReadN], and treats an empty wing as "every wing this agent has
// journaled in" — hook-written entries land in project wings, so a wingless read
// must still see them. Total is the full count in scope, so a caller can tell its
// journal is larger than the returned window.
func (s *Service) ReadDiary(ctx context.Context, teamID, agent, wing string, lastN int) (DiaryReadResult, error) {
	cleanAgent, err := SanitizeName(agent, "agent_name")
	if err != nil {
		return DiaryReadResult{}, err
	}
	cleanAgent = strings.ToLower(cleanAgent)

	if wing = strings.TrimSpace(wing); wing != "" {
		if wing, err = SanitizeName(wing, "wing"); err != nil {
			return DiaryReadResult{}, err
		}
	}

	if lastN <= 0 {
		lastN = DefaultDiaryReadN
	}
	if lastN > MaxDiaryReadN {
		lastN = MaxDiaryReadN
	}

	rows, err := s.repo.Diary(ctx, teamID, cleanAgent, wing, lastN)
	if err != nil {
		return DiaryReadResult{}, fmt.Errorf("read diary: %w", err)
	}
	total, err := s.repo.DiaryCount(ctx, teamID, cleanAgent, wing)
	if err != nil {
		return DiaryReadResult{}, fmt.Errorf("count diary: %w", err)
	}

	entries := make([]DiaryEntry, len(rows))
	for i, d := range rows {
		entries[i] = DiaryEntry{
			Date:      d.ContentDate,
			Timestamp: d.FiledAt,
			Topic:     d.Topic,
			Content:   d.Content,
		}
	}
	return DiaryReadResult{
		Agent:   cleanAgent,
		Entries: entries,
		Total:   total,
		Showing: len(entries),
	}, nil
}

// distanceFromScore converts a cosine similarity in [-1, 1] into a distance in
// [0, 2] (0 = identical), matching the Python contract's max_distance scale.
func distanceFromScore(score float32) float64 {
	d := 1 - float64(score)
	if d < 0 {
		return 0
	}
	if d > 2 {
		return 2
	}
	return d
}

// short12 trims an id for an error message.
func short12(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// WingIsEmpty reports whether a wing holds no drawers yet.
func (s *Service) WingIsEmpty(ctx context.Context, teamID, wing string) (bool, error) {
	return s.repo.WingIsEmpty(ctx, teamID, wing)
}

// WingNames lists the wings a team has written to.
func (s *Service) WingNames(ctx context.Context, teamID string) ([]string, error) {
	return s.repo.WingNames(ctx, teamID)
}

// InboxCount counts the drawers in one wing's room.
func (s *Service) InboxCount(ctx context.Context, teamID, wing, room string) (int, error) {
	return s.repo.InboxCount(ctx, teamID, wing, room)
}

// MemorySize reports the rune length of the whole logical memory a chunk belongs
// to, and how many chunks it is stored in.
//
// It exists for ADR-044 F-2: am_get_drawer without whole:true hands back ONE
// chunk, and a fragment a caller cannot tell is a fragment is the defect that
// record is about. Marking it needs the memory's full length, and no row carries
// one — reassembly removes chunk overlap, so the length is neither the chunk's
// nor the sum of the chunks'. Measured 2026-08-29: a 60,237-rune memory is stored
// as 47 chunks whose lengths sum to ~75,200, so the sum is an upper bound and
// reporting it would be a wrong number rather than a missing one.
//
// So it reassembles, and that is a deliberate trade rather than an oversight. The
// SEARCH path already reassembles every candidate memory on every recall
// (memory_search.go, representative.MemoryContent), so this is parity with the
// path already measured, not a new class of cost. What it buys is the asymmetry
// ADR-044 rests on: server-side bytes are cheap and an agent's context is not, so
// loading a memory to report ONE number while returning one chunk is the right
// direction to spend in.
//
// The count is returned beside the length because a caller marking a fragment
// must key on "this memory has more than one chunk" and NOT on ParentID: the ROOT
// chunk of a 47-chunk memory has no parent and chunk_index 0, so a ParentID test
// leaves exactly the case this was written for unmarked.
func (s *Service) MemorySize(ctx context.Context, teamID, id string) (length, chunks int, err error) {
	cs, err := s.GetMemory(ctx, teamID, id)
	if err != nil {
		return 0, 0, err
	}
	return len([]rune(reassembleMemory(cs))), len(cs), nil
}
