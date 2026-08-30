package palace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The knowledge graph is a temporal store of subject -> predicate -> object facts.
// Each fact has a validity window: it is CURRENT while valid_to is empty, and
// ending it (kg_invalidate) sets valid_to without deleting the row, so history is
// never lost. Queries can ask "as of" a moment in time. It is pure relational
// state (no embeddings), team-scoped, ported from the frozen knowledge_graph.py.
// Temporal values are TEXT compared lexicographically; date-only values are
// normalized to a datetime (start of day for lower bounds, end of day for upper)
// so a bare date and a precise datetime compare correctly.

// MaxKGValueLen caps a knowledge-graph subject or object. It is exported so the
// MCP tool descriptions can be BUILT from it rather than restating it: an agent
// that does not know the cap discovers it by failing, which is how one session
// spent four calls learning that a paragraph of evidence cannot be smuggled into
// an object. A number stated in prose beside the number that enforces it is a
// drift waiting to happen; a number the prose is generated from cannot drift.
const MaxKGValueLen = 128 // frozen MAX_NAME_LENGTH, shared by KG values
const kgTimelineLimit = 100

var (
	// kgDateRE / kgDateTimeRE are the frozen accepted temporal shapes: a calendar
	// date, or a canonical UTC datetime (Z or +00:00). Nothing else is allowed, so
	// the TEXT comparisons the queries rely on stay well-ordered.
	kgDateRE     = regexp.MustCompile(`^\d{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])$`)
	kgDateTimeRE = regexp.MustCompile(`^\d{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:Z|\+00:00)$`)
)

// sanitizeKGValue validates a subject/object value: non-empty, within the length
// bound, no NUL. It is deliberately more permissive than SanitizeName (KG values
// are natural-language entities that may carry commas, colons, parentheses), so it
// only enforces the minimal safety bounds, matching the frozen sanitize_kg_value.
func sanitizeKGValue(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s must be a non-empty string", ErrInvalidInput, field)
	}
	if len([]rune(value)) > MaxKGValueLen {
		return "", fmt.Errorf("%w: %s exceeds maximum length of %d characters", ErrInvalidInput, field, MaxKGValueLen)
	}
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("%w: %s contains null bytes", ErrInvalidInput, field)
	}
	return value, nil
}

// sanitizeISOTemporal validates an ISO-8601 date or canonical UTC datetime,
// returning "" unchanged (empty means "unbounded"). A `+00:00` offset is
// normalized to `Z` so all stored datetimes share one shape and compare correctly.
// Partial dates, naive datetimes and non-UTC offsets are rejected — the frozen
// sanitize_iso_temporal contract — because the temporal columns are compared as
// plain text and only one canonical shape keeps that ordering sound.
func sanitizeISOTemporal(value, field string) (string, error) {
	// A genuinely empty value means "unbounded" and passes through. A whitespace-only
	// value, by contrast, is a malformed input: after trimming it becomes "" and
	// falls through to the switch default below, which rejects it — matching the
	// frozen sanitizer, which checks emptiness before stripping.
	if value == "" {
		return "", nil
	}
	value = strings.TrimSpace(value)
	switch {
	case kgDateRE.MatchString(value):
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return "", fmt.Errorf("%w: %s=%q is not a valid calendar date", ErrInvalidInput, field, value)
		}
		return value, nil
	case kgDateTimeRE.MatchString(value):
		if strings.HasSuffix(value, "+00:00") {
			value = strings.TrimSuffix(value, "+00:00") + "Z"
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return "", fmt.Errorf("%w: %s=%q is not a valid UTC datetime", ErrInvalidInput, field, value)
		}
		return value, nil
	default:
		return "", fmt.Errorf("%w: %s=%q is not a valid ISO-8601 date or UTC datetime (expected YYYY-MM-DD or YYYY-MM-DDTHH:MM:SSZ)", ErrInvalidInput, field, value)
	}
}

// isDateOnly reports whether a temporal value is a bare YYYY-MM-DD.
func isDateOnly(v string) bool {
	return len(v) == 10 && v[4] == '-' && v[7] == '-'
}

// temporalStartKey normalizes a lower-bound temporal value for comparison: a bare
// date becomes the start of that day, so "2026-01-01" includes all of Jan 1.
func temporalStartKey(v string) string {
	if v == "" {
		return ""
	}
	if isDateOnly(v) {
		return v + "T00:00:00Z"
	}
	return v
}

// temporalEndKey normalizes an upper-bound temporal value: a bare date becomes the
// END of that day, so a fact valid_to "2026-01-31" stays in effect through Jan 31.
//
// ⚠ THAT PROMOTION IS WHY as_of AND status:"current" CAN DISAGREE, and it is kept
// rather than fixed. status selects on valid_to != "" and so drops a retracted
// fact immediately; as_of compares against the key this returns, so a date-only
// valid_to keeps the fact in effect until midnight. Issue #47 reproduced the gap.
// Narrowing the promotion here would silently re-read every already-ended row in
// every palace — the inclusive reading is what those rows were written under — so
// the fix went to the WRITE path instead: KGInvalidate and KGSupersede both stamp
// an RFC3339 instant, which never stretches. What is left is a date-only valid_to
// that a caller passed explicitly, or a row stored before that change, and the
// kg_query `as_of` description says so rather than leaving a reader to find out.
func temporalEndKey(v string) string {
	if v == "" {
		return ""
	}
	if isDateOnly(v) {
		return v + "T23:59:59Z"
	}
	return v
}

// normalizeEntityID maps a display name to its canonical id: lowercased, spaces to
// underscores, apostrophes dropped (frozen _entity_id). Two spellings that differ
// only in case/spacing/apostrophes resolve to the same entity.
func normalizeEntityID(name string) string {
	id := strings.ToLower(name)
	id = strings.ReplaceAll(id, " ", "_")
	id = strings.ReplaceAll(id, "'", "")
	return id
}

// normalizePredicate canonicalizes a predicate: lowercased with spaces to
// underscores (frozen predicate.lower().replace(" ", "_")). The caller validates
// it with SanitizeName first.
func normalizePredicate(predicate string) string {
	return strings.ReplaceAll(strings.ToLower(predicate), " ", "_")
}

// tripleID is a fact's identity: the entity ids and predicate, plus a hash of the
// validity start and the record time, so two facts about the same triple at
// different times get distinct ids (frozen make_triple_id).
func tripleID(subID, predicate, objID, validFrom, recordedAt string) string {
	sum := sha256.Sum256([]byte(validFrom + "|" + recordedAt))
	return fmt.Sprintf("t_%s_%s_%s_%s", subID, predicate, objID, hex.EncodeToString(sum[:])[:12])
}

// --- rows + repo ----------------------------------------------------------

type kgEntityRow struct {
	TeamID    string `gorm:"column:team_id;primaryKey"`
	ID        string `gorm:"column:id;primaryKey"`
	Name      string `gorm:"column:name"`
	CreatedAt string `gorm:"column:created_at"`
}

func (kgEntityRow) TableName() string { return "kg_entities" }

type kgTripleRow struct {
	TeamID         string  `gorm:"column:team_id;primaryKey"`
	ID             string  `gorm:"column:id;primaryKey"`
	Subject        string  `gorm:"column:subject"`
	Predicate      string  `gorm:"column:predicate"`
	Object         string  `gorm:"column:object"`
	ValidFrom      string  `gorm:"column:valid_from"`
	ValidTo        string  `gorm:"column:valid_to"`
	Confidence     float64 `gorm:"column:confidence"`
	SourceCloset   string  `gorm:"column:source_closet"`
	SourceFile     string  `gorm:"column:source_file"`
	SourceDrawerID string  `gorm:"column:source_drawer_id"`
	ExtractedAt    string  `gorm:"column:extracted_at"`
	// Derived marks an edge the server inferred rather than one a writer
	// authored. A nil pointer means the row predates the distinction, which is
	// not the same claim as "authored" — see 00028_kg_triples_derived.sql.
	Derived *bool `gorm:"column:derived"`
	// EndedReason is WHY the fact stopped being true. The store already kept THAT
	// a fact ended, in valid_to; the reason is the expensive half and it had
	// nowhere to land until 00032_kg_ended_reason.sql.
	EndedReason string `gorm:"column:ended_reason"`
}

func (kgTripleRow) TableName() string { return "kg_triples" }

// UpsertKGEntity inserts an entity if absent, keeping the first-seen display name
// (INSERT OR IGNORE) — adding a fact auto-creates its endpoints.
func (r *Repo) UpsertKGEntity(ctx context.Context, teamID, id, name, now string) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&kgEntityRow{TeamID: teamID, ID: id, Name: name, CreatedAt: now}).Error
}

// CurrentTripleID returns the id of the current (not-yet-ended) triple for a
// subject/predicate/object, or "" if none — the dedup check kg_add uses.
func (r *Repo) CurrentTripleID(ctx context.Context, teamID, subject, predicate, object string) (string, error) {
	var ids []string
	if err := r.db.WithContext(ctx).Model(&kgTripleRow{}).
		Where("team_id = ? AND subject = ? AND predicate = ? AND object = ? AND valid_to = ''", teamID, subject, predicate, object).
		Limit(1).Pluck("id", &ids).Error; err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", nil
	}
	return ids[0], nil
}

// InsertKGTriple writes a new fact.
func (r *Repo) InsertKGTriple(ctx context.Context, row kgTripleRow) error {
	return r.db.WithContext(ctx).Create(&row).Error
}

// CurrentTriples returns the current triples for a subject/predicate/object — the
// rows kg_invalidate will end (and validate the new end against their starts).
func (r *Repo) CurrentTriples(ctx context.Context, teamID, subject, predicate, object string) ([]kgTripleRow, error) {
	var rows []kgTripleRow
	err := r.db.WithContext(ctx).
		Where("team_id = ? AND subject = ? AND predicate = ? AND object = ? AND valid_to = ''", teamID, subject, predicate, object).
		Find(&rows).Error
	return rows, err
}

// InvalidateKGTriples ends every current triple for a subject/predicate/object by
// setting its valid_to, reporting how many it ended.
func (r *Repo) InvalidateKGTriples(ctx context.Context, teamID, subject, predicate, object, ended, reason string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&kgTripleRow{}).
		Where("team_id = ? AND subject = ? AND predicate = ? AND object = ? AND valid_to = ''", teamID, subject, predicate, object).
		Updates(map[string]any{"valid_to": ended, "ended_reason": reason})
	return res.RowsAffected, res.Error
}

// kgStatusScope narrows a triple query to one half of a fact's life, or leaves it
// whole for KGStatusAll. It is a REFINEMENT of a more selective entry point —
// a subject, object or predicate — never the term that finds the rows.
//
// Both directions are exact comparisons against the empty string, which is this
// schema's "not yet ended" sentinel (00010_kg.sql chose it over NULL so a Go string
// column never has to scan NULL). That exactness is why the endedness test is safe
// to index at all: it never compares two temporal values, so the mixed date-only
// and datetime formats KGAdd stores verbatim cannot affect the result. A *range*
// over valid_to has no such protection — see ADR-026 §2b, and do not push one
// into SQL.
//
// ⚠ The unary + on valid_to is load-bearing, and MEASURED rather than assumed. It
// makes the term unusable by an index (SQLite's documented meaning) while leaving
// the value untouched. Without it, idx_kg_triples_team_valid_to CAPTURES this
// query: nothing in this repo runs ANALYZE, so with no stats the planner preferred
// the newest usable index and resolved an entity lookup through valid_to instead
// of through subject. An empty valid_to matches ~96% of a tenant's rows and a subject
// matches a handful, so the index added to make the default path cheaper made it
// read almost the whole tenant. The plan still printed
// `SEARCH … USING INDEX … (team_id=? AND valid_to=?)` throughout — an index, on
// the column being filtered, and still the wrong one. See TestStatusFilterRefines
// TheEntryPointRatherThanReplacingIt, which is what stops this regressing.
func kgStatusScope(db *gorm.DB, status string) *gorm.DB {
	switch status {
	case KGStatusCurrent:
		return db.Where("+valid_to = ''")
	case KGStatusEnded:
		return db.Where("valid_to <> ''")
	}
	return db
}

// kgTripleFilter is what narrows a triple lookup. The distinction it draws is the
// one the query planner cares about: column/value is the ENTRY POINT, the term
// meant to find the rows through an index, while status and predicate are
// REFINEMENTS that shrink what the entry point found.
//
// column is interpolated into the SQL, so it takes only the package-internal
// literals its exported wrappers pass; it is never reachable from a caller's input.
// predicate is left empty when predicate IS the entry point, so the same term is
// never both.
type kgTripleFilter struct {
	column    string
	value     string
	status    string
	predicate string
}

// kgTripleQuery builds the statement every triple lookup issues. It is a builder
// rather than inline SQL so a test can render the SHIPPED statement through a
// dry-run session and read its query plan, instead of asserting against a
// hand-copied echo that can drift.
func (r *Repo) kgTripleQuery(ctx context.Context, teamID string, f kgTripleFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&kgTripleRow{}).
		Where("team_id = ? AND "+f.column+" = ?", teamID, f.value)
	if f.predicate != "" {
		// No unary + here, unlike the status scope: with ~one distinct predicate
		// per fact in this corpus, predicate is often MORE selective than the
		// entity, so either index is a good entry point and the planner should be
		// free to pick. The endedness test is the opposite case — it matches
		// almost every row — which is why only that one is held back.
		q = q.Where("predicate = ?", f.predicate)
	}
	return kgStatusScope(q, f.status)
}

// kgTriples loads a team's triples matching a filter.
func (r *Repo) kgTriples(ctx context.Context, teamID string, f kgTripleFilter) ([]kgTripleRow, error) {
	var rows []kgTripleRow
	err := r.kgTripleQuery(ctx, teamID, f).Find(&rows).Error
	return rows, err
}

// kgTriplesCount counts the same shape kgTriples loads, without reading the rows.
// It is how a filtered response reports what it withheld: the caller runs it once
// with the complement status rather than re-filtering rows it deliberately never
// fetched.
func (r *Repo) kgTriplesCount(ctx context.Context, teamID string, f kgTripleFilter) (int64, error) {
	var n int64
	err := r.kgTripleQuery(ctx, teamID, f).Count(&n).Error
	return n, err
}

// KGTriplesBySubject / KGTriplesByObject / KGTriplesByPredicate load a team's
// triples on one entry point, narrowed by status and optionally by predicate.
//
// KGTriplesByPredicate is the entry point ADR-026 T5 opens. idx_kg_triples_team_predicate
// has existed since 00010_kg.sql and no query ever used it: the schema was built
// for predicate lookups and the query layer never arrived. So this costs no
// migration, and it makes selectable the one dimension the graph is built from —
// "show me every retracts edge" is how you audit what the team changed its mind
// about, and it was a scan by eye.
func (r *Repo) KGTriplesBySubject(ctx context.Context, teamID, subject, status, predicate string) ([]kgTripleRow, error) {
	return r.kgTriples(ctx, teamID, kgTripleFilter{column: "subject", value: subject, status: status, predicate: predicate})
}

func (r *Repo) KGTriplesByObject(ctx context.Context, teamID, object, status, predicate string) ([]kgTripleRow, error) {
	return r.kgTriples(ctx, teamID, kgTripleFilter{column: "object", value: object, status: status, predicate: predicate})
}

func (r *Repo) KGTriplesByPredicate(ctx context.Context, teamID, predicate, status string) ([]kgTripleRow, error) {
	return r.kgTriples(ctx, teamID, kgTripleFilter{column: "predicate", value: predicate, status: status})
}

// KGTriplesBySubjectCount / KGTriplesByObjectCount / KGTriplesByPredicateCount
// count one entry point's triples at a given status, for the withheld tally.
func (r *Repo) KGTriplesBySubjectCount(ctx context.Context, teamID, subject, status, predicate string) (int64, error) {
	return r.kgTriplesCount(ctx, teamID, kgTripleFilter{column: "subject", value: subject, status: status, predicate: predicate})
}

func (r *Repo) KGTriplesByObjectCount(ctx context.Context, teamID, object, status, predicate string) (int64, error) {
	return r.kgTriplesCount(ctx, teamID, kgTripleFilter{column: "object", value: object, status: status, predicate: predicate})
}

func (r *Repo) KGTriplesByPredicateCount(ctx context.Context, teamID, predicate, status string) (int64, error) {
	return r.kgTriplesCount(ctx, teamID, kgTripleFilter{column: "predicate", value: predicate, status: status})
}

// KGEntityNames resolves entity ids to their display names for a team.
func (r *Repo) KGEntityNames(ctx context.Context, teamID string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []kgEntityRow
	if err := r.db.WithContext(ctx).Where("team_id = ? AND id IN ?", teamID, ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.Name
	}
	return out, nil
}

// KGTimeline returns up to kgTimelineLimit triples for a team ordered by validity
// start (empties last), narrowed to those touching entity eid when it is non-empty.
func (r *Repo) KGTimeline(ctx context.Context, teamID, eid string) ([]kgTripleRow, error) {
	q := r.db.WithContext(ctx).Where("team_id = ?", teamID)
	if eid != "" {
		q = q.Where("subject = ? OR object = ?", eid, eid)
	}
	var rows []kgTripleRow
	// "valid_from = '' ASC" puts dated facts first and the open-start ones last
	// (the frozen NULLS LAST), then chronological within the dated ones.
	if err := q.Order("valid_from = '' ASC, valid_from ASC, id ASC").Limit(kgTimelineLimit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// kgCurrentQuery is the team-wide "which facts are still true" statement: the
// tenant plus endedness, with no more selective term to lean on.
//
// This is the shape idx_kg_triples_team_valid_to exists to serve, and the ONLY one
// — so unlike kgStatusScope it writes valid_to plainly, without the unary + that
// keeps the endedness test from driving an index. The two spellings encode which
// term is meant to find the rows, and TestStatusCurrentIsIndexed pins this half.
func (r *Repo) kgCurrentQuery(ctx context.Context, teamID string) *gorm.DB {
	return r.db.WithContext(ctx).Model(&kgTripleRow{}).Where("team_id = ? AND valid_to = ''", teamID)
}

// KGCounts returns the entity count, total triples, and current (not-ended) triple
// count for a team — the numeric half of kg_stats.
func (r *Repo) KGCounts(ctx context.Context, teamID string) (entities, triples, current int64, err error) {
	if err = r.db.WithContext(ctx).Model(&kgEntityRow{}).Where("team_id = ?", teamID).Count(&entities).Error; err != nil {
		return
	}
	if err = r.db.WithContext(ctx).Model(&kgTripleRow{}).Where("team_id = ?", teamID).Count(&triples).Error; err != nil {
		return
	}
	err = r.kgCurrentQuery(ctx, teamID).Count(&current).Error
	return
}

// KGPredicates returns a team's distinct predicates, sorted.
func (r *Repo) KGPredicates(ctx context.Context, teamID string) ([]string, error) {
	var preds []string
	err := r.db.WithContext(ctx).Model(&kgTripleRow{}).
		Where("team_id = ?", teamID).Distinct().Order("predicate").Pluck("predicate", &preds).Error
	return preds, err
}

// --- service --------------------------------------------------------------

// The statuses a graph query can ask for. They select on ENDEDNESS — whether a
// fact has been retracted — which is a different question from as_of's "was this
// in effect at that moment", and the two compose rather than overlap.
//
// KGStatusCurrent is named for the wire field it filters on (KGFact.Current), but
// both mean OPEN-ENDED: a fact whose valid_to is future-dated is still current by
// this test. KGAdd can write such a fact and nothing does today.
const (
	KGStatusCurrent = "current"
	KGStatusEnded   = "ended"
	KGStatusAll     = "all"
)

// KGQueryInput is what a graph query can ask for. It is a struct rather than a
// parameter list because ADR-026 grows it twice more — the predicate entry point,
// then the default flip — and each of those must be a one-line change at the call
// site rather than a re-typing of every caller.
//
// Status empty means KGStatusAll. The DEFAULT lives at the MCP registration, not
// here, so flipping it is one string literal in one place (ADR-026 §Rollback).
//
// Entity and Predicate are each an entry point, and at least one is required.
// With both, Predicate refines the entity's facts; with Predicate alone the query
// answers "every fact of this relation", which is how you audit a whole relation
// type; with Entity alone it behaves exactly as it always has.
type KGQueryInput struct {
	Entity    string
	Predicate string
	AsOf      string
	Direction string
	Status    string
}

// KGResolution says which of three things a successful lookup found. The three
// are exhaustive and mutually exclusive BY STAGE, which "absence versus failure"
// was not: that phrase is two words for four things, and two of them overlap.
//
// Stage A resolves the term: it is either known to the graph or it is not.
// Stage B, reached only when the term is known, either matches triples or does
// not. So:
//
//	matched               the term is known and triples matched
//	known_term_no_facts   the term is known and nothing matched
//	unknown_term          the term is not in the graph at all
//
// A backend failure is deliberately NOT a fourth value. It is returned as an
// error, out of band, exactly as this package already does — a failed lookup has
// no result to carry a state on. What matters is that it never FAILS OPEN into
// one of the three: measured 2026-08-26, a nonexistent entity and a nonexistent
// predicate both returned count:0 with no error, which is indistinguishable from
// a real empty answer and is what made a sibling-wing pointer untrustworthy.
type KGResolution string

// The three resolution states. See KGResolution for why there is no fourth.
const (
	KGResolutionMatched         KGResolution = "matched"
	KGResolutionKnownTermNoFact KGResolution = "known_term_no_facts"
	KGResolutionUnknownTerm     KGResolution = "unknown_term"
)

// KGQueryResult is a graph query's answer together with what it did not return.
//
// Withheld is the count the status filter removed, taken from the store rather
// than recomputed from the rows — a filtered query never fetches what it dropped,
// so re-deriving the number would mean re-running the filter with the opposite
// answer and would be a second place to be wrong. WithheldStatus names what those
// rows are, so the surface reporting them does not have to re-derive the
// complement and risk disagreeing with the count beside it.
type KGQueryResult struct {
	Entity         string
	Predicate      string
	Facts          []KGFact
	Status         string
	Withheld       int64
	WithheldStatus string
	// Resolution distinguishes a real empty answer from a term the graph has
	// never heard of. Without it a caller cannot tell "nothing is filed about
	// this" from "you asked about something that does not exist here", and a
	// pointer built on the second is a pointer to nowhere.
	Resolution KGResolution
	// Unresolved names WHICH entry point did not resolve — "entity" or
	// "predicate" — when Resolution is unknown_term. A query may give both, and
	// "something you named is unknown" is not actionable without knowing which.
	Unresolved string
}

// KGFact is one fact a query/timeline returns, with display names resolved and the
// current flag computed.
//
// Current means OPEN-ENDED — valid_to is empty — not "true right now". The two
// differ for a future-dated valid_to, which KGAdd can write and nothing does. The
// field is not renamed to open_ended because it is a live contract agents read;
// this comment is the correction ADR-026 chose instead.
//
// RecordedAt is TRANSACTION time, not validity time, and the pair is what makes
// the graph bitemporal: as_of answers "what was true on D" and recorded_at answers
// "what did we KNOW on D". It has been half-bitemporal since it was built and
// unable to say so, because the column was written on every fact and returned by
// nothing.
type KGFact struct {
	Direction      string  `json:"direction,omitempty"`
	Subject        string  `json:"subject"`
	Predicate      string  `json:"predicate"`
	Object         string  `json:"object"`
	ValidFrom      string  `json:"valid_from,omitempty"`
	ValidTo        string  `json:"valid_to,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	SourceCloset   string  `json:"source_closet,omitempty"`
	SourceFile     string  `json:"source_file,omitempty"`
	SourceDrawerID string  `json:"source_drawer_id,omitempty"`
	RecordedAt     string  `json:"recorded_at,omitempty"`
	Current        bool    `json:"current"`
	// Derived says the SERVER inferred this edge rather than a writer authoring
	// it. False covers both "authored" and "filed before the distinction
	// existed" — the row keeps that difference (a NULL), but a reader acting on
	// a fact only needs to know whether to trust it as somebody's decision.
	Derived bool `json:"derived,omitempty"`
	// EndedReason is WHY the fact stopped being true, and it is the half of a
	// retraction a reader cannot reconstruct: valid_to already says THAT it ended.
	// Empty on a current fact, and on any fact ended before ADR-038 required one.
	EndedReason string `json:"ended_reason,omitempty"`
}

// kgRowFieldRenames maps a kgTripleRow field to the KGFact field that returns it,
// for the one pair not spelled the same. extracted_at is surfaced as recorded_at
// because "extracted" describes how kg-extract produced a fact and says nothing to
// an agent asking when the graph learned it.
var kgRowFieldRenames = map[string]string{"ExtractedAt": "RecordedAt"}

// kgRowFieldsExcluded names every stored column deliberately NOT returned, each
// with the reason it is withheld. A column absent from both this map and KGFact is
// a column written and invisible, which is what TestEveryStoredTripleColumnIsReturnedOrExcluded
// refuses — three such columns are exactly what ADR-026 T6 was fixing.
var kgRowFieldsExcluded = map[string]string{
	"TeamID": "tenancy comes from the session; a caller-supplied team is a hole, not a field",
	"ID":     "fetch-by-triple-id is a different tool's shape, and no read path takes one",
}

// KGAddResult / KGStatsResult are the structured tool returns.
type KGAddResult struct {
	TripleID string `json:"triple_id"`
	Fact     string `json:"fact"`
}

type KGStatsResult struct {
	Entities          int64    `json:"entities"`
	Triples           int64    `json:"triples"`
	CurrentFacts      int64    `json:"current_facts"`
	ExpiredFacts      int64    `json:"expired_facts"`
	RelationshipTypes []string `json:"relationship_types"`
}

// KGAdd records a fact. It validates the inputs and the validity interval, auto-
// creates the subject/object entities, and inserts the triple — UNLESS an
// identical current fact already exists, in which case it returns that fact's id
// (the frozen no-auto-supersede rule: to replace a fact, invalidate it first).
func (s *Service) KGAdd(ctx context.Context, teamID, subject, predicate, object, validFrom, validTo, sourceCloset, sourceFile, sourceDrawerID string) (result KGAddResult, err error) {
	_, sp := telemetry.Start(ctx, telemetry.StageKGAdd)
	defer func() { endStage(sp, err) }()
	subj, err := sanitizeKGValue(subject, "subject")
	if err != nil {
		return KGAddResult{}, err
	}
	pred, err := SanitizeName(predicate, "predicate")
	if err != nil {
		return KGAddResult{}, err
	}
	obj, err := sanitizeKGValue(object, "object")
	if err != nil {
		return KGAddResult{}, err
	}
	vf, err := sanitizeISOTemporal(validFrom, "valid_from")
	if err != nil {
		return KGAddResult{}, err
	}
	vt, err := sanitizeISOTemporal(validTo, "valid_to")
	if err != nil {
		return KGAddResult{}, err
	}
	if vf != "" && vt != "" && temporalEndKey(vt) < temporalStartKey(vf) {
		return KGAddResult{}, fmt.Errorf("%w: valid_to=%q is before valid_from=%q; an inverted interval is invisible to every query", ErrInvalidInput, vt, vf)
	}

	res, err := kgAddOn(ctx, s.repo, teamID, subj, pred, obj, vf, vt, sourceCloset, sourceFile, sourceDrawerID)
	if err != nil {
		return KGAddResult{}, err
	}
	s.indexFactLabels(ctx, teamID, subj, obj)
	return res, nil
}

// kgAddOn writes a fact through the repo it is handed — s.repo for an ordinary
// add, a TRANSACTION-BOUND copy for a supersede. It exists so KGSupersede can put
// the end and the add in one transaction without a second copy of the
// upsert/dedupe/insert sequence drifting away from this one.
//
// It deliberately does NOT index the endpoint labels. That is a network call to
// the embedder, and holding SQLite's single write transaction open across one is
// how a slow embedder becomes a locked database. The caller indexes after the
// write has landed.
//
// Its arguments are already sanitized; it is unexported and both callers validate
// first, so re-validating here would only be a second place for the rules to
// disagree.
func kgAddOn(ctx context.Context, r *Repo, teamID, subj, pred, obj, vf, vt, sourceCloset, sourceFile, sourceDrawerID string) (KGAddResult, error) {
	// Provenance is checked against the CORPUS, which is why it sits here rather
	// than with the shape validation the doc above says not to repeat: no amount of
	// looking at the string tells you whether the row is there.
	//
	// It runs BEFORE the entity upserts so a refused fact leaves nothing behind. The
	// upserts mint kg_entities rows, and a check placed after them would refuse the
	// write while still having created the two nodes it was called with.
	if sourceDrawerID != "" {
		exists, err := r.DrawerExists(ctx, teamID, sourceDrawerID)
		if err != nil {
			return KGAddResult{}, fmt.Errorf("check the source drawer: %w", err)
		}
		if !exists {
			return KGAddResult{}, fmt.Errorf(
				"%w: source_drawer_id %s names no drawer in this team, so the fact would "+
					"carry provenance that resolves to nothing. Copy the id whole: a shortened or "+
					"retyped one lands here, which is the point of the check. Omit "+
					"source_drawer_id if this fact does not come from a drawer",
				ErrSourceDrawerNotFound, short12(sourceDrawerID))
		}
	}

	subID, objID, p := normalizeEntityID(subj), normalizeEntityID(obj), normalizePredicate(pred)
	now := time.Now().UTC().Format(time.RFC3339)
	if err := r.UpsertKGEntity(ctx, teamID, subID, subj, now); err != nil {
		return KGAddResult{}, err
	}
	if err := r.UpsertKGEntity(ctx, teamID, objID, obj, now); err != nil {
		return KGAddResult{}, err
	}

	fact := subj + " → " + p + " → " + obj
	if existing, err := r.CurrentTripleID(ctx, teamID, subID, p, objID); err != nil {
		return KGAddResult{}, err
	} else if existing != "" {
		return KGAddResult{TripleID: existing, Fact: fact}, nil
	}

	id := tripleID(subID, p, objID, vf, now)
	if err := r.InsertKGTriple(ctx, kgTripleRow{
		TeamID: teamID, ID: id, Subject: subID, Predicate: p, Object: objID,
		ValidFrom: vf, ValidTo: vt, Confidence: 1.0,
		SourceCloset: sourceCloset, SourceFile: sourceFile, SourceDrawerID: sourceDrawerID, ExtractedAt: now,
	}); err != nil {
		return KGAddResult{}, err
	}
	return KGAddResult{TripleID: id, Fact: fact}, nil
}

// indexFactLabels indexes both endpoint labels so the fact is reachable by a
// QUESTION, not only by spelling the entity exactly. This is the incremental half
// of the lifecycle: an index built once at backfill is stale by its second day and
// never says so — it just answers with yesterday's graph.
//
// Non-fatal. The fact is written; the label index is only how it is found, and
// refusing the write because the embedder is down is the trade this codebase
// already declined on the drawer path.
func (s *Service) indexFactLabels(ctx context.Context, teamID, subj, obj string) {
	for _, e := range []struct{ id, label string }{
		{normalizeEntityID(subj), subj},
		{normalizeEntityID(obj), obj},
	} {
		if err := s.IndexEntityLabel(ctx, teamID, e.id, e.label); err != nil {
			slog.WarnContext(ctx, "entity label not indexed; the fact is stored but unreachable by question",
				"entity", e.id, "err", err)
		}
	}
}

// KGInvalidate ends a current fact by setting its valid_to (defaulting to today)
// and recording WHY. It rejects an end that precedes the fact's own start.
// Ending a fact never deletes it — the history stays queryable as-of an earlier
// time.
//
// The reason is REQUIRED (ADR-038). valid_to already recorded that a fact stopped
// being true; the reason is the half a later reader cannot reconstruct from the
// row, and the store kept the cheap one and dropped the expensive one for as long
// as there was nowhere to put it.
func (s *Service) KGInvalidate(ctx context.Context, teamID, subject, predicate, object, ended, reason string) (endedFacts int64, fact, resolvedEnded string, err error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return 0, "", "", fmt.Errorf("%w: a reason is required to end a fact — an invalidation that "+
			"records only THAT a fact ended keeps the cheapest half and drops the expensive one. "+
			"Say what changed, or use kg_supersede if something replaces it", ErrInvalidInput)
	}
	subj, err := sanitizeKGValue(subject, "subject")
	if err != nil {
		return 0, "", "", err
	}
	pred, err := SanitizeName(predicate, "predicate")
	if err != nil {
		return 0, "", "", err
	}
	obj, err := sanitizeKGValue(object, "object")
	if err != nil {
		return 0, "", "", err
	}
	e, err := sanitizeISOTemporal(ended, "ended")
	if err != nil {
		return 0, "", "", err
	}
	if e == "" {
		// ⚠ AN INSTANT, NEVER A DATE, and the format is the whole fix for issue #47.
		// This defaulted to "2006-01-02", and `ended` is optional, so EVERY
		// retraction made through am_kg_invalidate without an explicit instant
		// landed as a bare date — the default path, not an edge case. A bare date
		// takes temporalEndKey's end-of-day promotion, so the fact stayed visible
		// to as_of:<that day> until midnight while status:"current" dropped it the
		// instant the row was written: two filters, two answers, one day, and
		// ADR-026 had just told callers the two COMPOSE.
		//
		// KGSupersede already stamps an instant for exactly this reason
		// (supersede.go) — it sidestepped the path rather than fixing it, because
		// fixing it here is what stops NEW rows joining the ambiguity.
		//
		// What this deliberately does NOT do is change temporalEndKey. A caller
		// may still pass a date-only `ended`, and every row already stored is one;
		// deciding what a date-only valid_to MEANS re-reads every already-ended
		// fact, which is a decision record's job and not this line's. The
		// remaining lag is documented on the tool surface instead of hidden.
		e = time.Now().UTC().Format(time.RFC3339)
	}
	subID, objID, p := normalizeEntityID(subj), normalizeEntityID(obj), normalizePredicate(pred)

	// Reject an end before any matching fact's start (the inverted-interval guard).
	current, err := s.repo.CurrentTriples(ctx, teamID, subID, p, objID)
	if err != nil {
		return 0, "", "", err
	}
	for _, row := range current {
		if row.ValidFrom != "" && temporalEndKey(e) < temporalStartKey(row.ValidFrom) {
			return 0, "", "", fmt.Errorf("%w: ended=%q is before valid_from=%q", ErrInvalidInput, e, row.ValidFrom)
		}
	}
	// RowsAffected is the ANSWER here, not a diagnostic, and discarding it was the
	// defect M reported on 2026-08-27: this returned nil for a fact it had never
	// touched and the MCP handler rendered a hardcoded "success": true. Reproduced
	// against the running server — invalidating a triple that had never existed
	// answered success while kg_triples ended nothing.
	//
	// It is this repository's characteristic defect wearing a temporal hat: a
	// write that reports success and changes nothing. Worse here than elsewhere,
	// because the entire purpose of an invalidation is that the fact stops being
	// returned, so an agent that retracts a wrong fact, is told it worked, and
	// finds it still current has been misled by the one operation that exists to
	// keep the store honest.
	n, err := s.repo.InvalidateKGTriples(ctx, teamID, subID, p, objID, e, reason)
	if err != nil {
		return 0, "", "", err
	}
	if n == 0 {
		// Name the NORMALIZED terms, not the caller's spelling. normalizeEntityID
		// and normalizePredicate rewrite all three, so the likeliest cause of a
		// legitimate miss is a spelling that resolved somewhere the caller did not
		// expect — and echoing their own input back explains nothing. The other
		// cause is an already-ended fact, which is named too because it is the
		// case that looks most like a bug from the outside.
		return 0, "", "", fmt.Errorf(
			"%w: %s → %s → %s. Either it was never filed, or it is already ended "+
				"(am_kg_query with status \"ended\" shows it). Nothing was changed",
			ErrFactNotFound, subID, p, objID)
	}
	return n, subj + " → " + p + " → " + obj, e, nil
}

// kgComplementStatus returns the status a withheld tally must count: what a query
// at this status deliberately did not fetch. KGStatusAll withholds nothing, and
// returning "" for it is what lets the caller skip the count query entirely — so
// the pre-ADR-026 default path costs exactly what it did before.
func kgComplementStatus(status string) string {
	switch status {
	case KGStatusCurrent:
		return KGStatusEnded
	case KGStatusEnded:
		return KGStatusCurrent
	}
	return ""
}

// KGQuery returns facts from one of two entry points — an entity, a predicate, or
// both — optionally only those in effect at as_of, in a chosen direction, and only
// those at a given endedness. Display names are resolved from the entity table.
//
// The status and predicate filters run in SQL rather than over the returned rows.
// That is the point of them: a fact filtered in the client has already crossed the
// wire and entered the agent's context window, which is the cost being removed
// (ADR-026).
func (s *Service) KGQuery(ctx context.Context, teamID string, in KGQueryInput) (out KGQueryResult, err error) {
	_, sp := telemetry.Start(ctx, telemetry.StageKGQuery)
	defer func() {
		endStage(sp, err, attribute.Int("am.count", len(out.Facts)), attribute.Int("am.withheld", int(out.Withheld)))
	}()
	// Exactly one of the two entry points is required, not both, because either
	// alone finds rows through an index. Neither would mean "every fact this team
	// owns", which is a table dump wearing a query's clothes.
	if strings.TrimSpace(in.Entity) == "" && strings.TrimSpace(in.Predicate) == "" {
		return KGQueryResult{}, fmt.Errorf("%w: give an entity, a predicate, or both", ErrInvalidInput)
	}
	var ent string
	if strings.TrimSpace(in.Entity) != "" {
		var err error
		if ent, err = sanitizeKGValue(in.Entity, "entity"); err != nil {
			return KGQueryResult{}, err
		}
	}
	var pred string
	if strings.TrimSpace(in.Predicate) != "" {
		valid, err := SanitizeName(in.Predicate, "predicate")
		if err != nil {
			return KGQueryResult{}, err
		}
		pred = normalizePredicate(valid)
	}
	ao, err := sanitizeISOTemporal(in.AsOf, "as_of")
	if err != nil {
		return KGQueryResult{}, err
	}
	direction := in.Direction
	if direction == "" {
		direction = "both"
	}
	if direction != "outgoing" && direction != "incoming" && direction != "both" {
		return KGQueryResult{}, fmt.Errorf("%w: direction must be 'outgoing', 'incoming', or 'both'", ErrInvalidInput)
	}
	status := in.Status
	if status == "" {
		status = KGStatusAll
	}
	if status != KGStatusCurrent && status != KGStatusEnded && status != KGStatusAll {
		return KGQueryResult{}, fmt.Errorf("%w: status must be 'current', 'ended', or 'all'", ErrInvalidInput)
	}
	asOfKey := temporalStartKey(ao)
	dropped := kgComplementStatus(status)
	out = KGQueryResult{Entity: ent, Predicate: pred, Status: status, WithheldStatus: dropped}

	// With no entity, the predicate IS the entry point and direction has nothing to
	// be relative to — there is no queried endpoint for a fact to be incoming or
	// outgoing OF — so both endpoints are resolved and the facts carry no direction,
	// the same shape KGTimeline returns.
	if ent == "" {
		rows, err := s.repo.KGTriplesByPredicate(ctx, teamID, pred, status)
		if err != nil {
			return KGQueryResult{}, err
		}
		names, err := s.repo.KGEntityNames(ctx, teamID, append(otherIDs(rows, true), otherIDs(rows, false)...))
		if err != nil {
			return KGQueryResult{}, err
		}
		for _, row := range rows {
			if !inEffectAt(row, asOfKey) {
				continue
			}
			out.Facts = append(out.Facts, kgFact("", names[row.Subject], row.Predicate, names[row.Object], row))
		}
		if dropped != "" {
			n, err := s.repo.KGTriplesByPredicateCount(ctx, teamID, pred, dropped)
			if err != nil {
				return KGQueryResult{}, err
			}
			out.Withheld += n
		}
		if out.Resolution, out.Unresolved, err = s.classifyKGResult(ctx, teamID, out, "", "", pred); err != nil {
			return KGQueryResult{}, err
		}
		return out, nil
	}

	eid := normalizeEntityID(ent)
	if direction == "outgoing" || direction == "both" {
		rows, err := s.repo.KGTriplesBySubject(ctx, teamID, eid, status, pred)
		if err != nil {
			return KGQueryResult{}, err
		}
		names, err := s.repo.KGEntityNames(ctx, teamID, otherIDs(rows, true))
		if err != nil {
			return KGQueryResult{}, err
		}
		for _, row := range rows {
			if !inEffectAt(row, asOfKey) {
				continue
			}
			out.Facts = append(out.Facts, kgFact("outgoing", ent, row.Predicate, names[row.Object], row))
		}
		if dropped != "" {
			n, err := s.repo.KGTriplesBySubjectCount(ctx, teamID, eid, dropped, pred)
			if err != nil {
				return KGQueryResult{}, err
			}
			out.Withheld += n
		}
	}
	if direction == "incoming" || direction == "both" {
		rows, err := s.repo.KGTriplesByObject(ctx, teamID, eid, status, pred)
		if err != nil {
			return KGQueryResult{}, err
		}
		names, err := s.repo.KGEntityNames(ctx, teamID, otherIDs(rows, false))
		if err != nil {
			return KGQueryResult{}, err
		}
		for _, row := range rows {
			if !inEffectAt(row, asOfKey) {
				continue
			}
			out.Facts = append(out.Facts, kgFact("incoming", names[row.Subject], row.Predicate, ent, row))
		}
		if dropped != "" {
			n, err := s.repo.KGTriplesByObjectCount(ctx, teamID, eid, dropped, pred)
			if err != nil {
				return KGQueryResult{}, err
			}
			out.Withheld += n
		}
	}
	if out.Resolution, out.Unresolved, err = s.classifyKGResult(ctx, teamID, out, eid, ent, pred); err != nil {
		return KGQueryResult{}, err
	}
	return out, nil
}

// classifyKGResult sets the resolution state for a completed lookup.
func (s *Service) classifyKGResult(ctx context.Context, teamID string, out KGQueryResult, eid, ent, pred string) (KGResolution, string, error) {
	if len(out.Facts) > 0 {
		return KGResolutionMatched, "", nil
	}
	return s.resolveKGTerms(ctx, teamID, eid, ent, pred)
}

// resolveKGTerms classifies a lookup that returned no facts: is the term known to
// the graph, or has the graph never heard of it?
//
// It runs ONLY when nothing matched. The classification costs a query, and when
// facts came back the answer is already known — spending it on every call would
// make the common path pay for the rare one.
func (s *Service) resolveKGTerms(ctx context.Context, teamID, eid, ent, pred string) (KGResolution, string, error) {
	if ent != "" {
		names, err := s.repo.KGEntityNames(ctx, teamID, []string{eid})
		if err != nil {
			return "", "", err
		}
		if names[eid] == "" {
			return KGResolutionUnknownTerm, "entity", nil
		}
	}
	if pred != "" {
		// A predicate is known when ANY triple uses it, in any status. Asking
		// under the caller's own status filter would report a predicate whose
		// every fact has ended as unknown, which is a different thing entirely.
		rows, err := s.repo.KGTriplesByPredicate(ctx, teamID, pred, KGStatusAll)
		if err != nil {
			return "", "", err
		}
		if len(rows) == 0 {
			return KGResolutionUnknownTerm, "predicate", nil
		}
	}
	return KGResolutionKnownTermNoFact, "", nil
}

// KGStats summarizes the team's graph: entity and triple totals, current vs
// expired facts, and the distinct relationship types.
func (s *Service) KGStats(ctx context.Context, teamID string) (KGStatsResult, error) {
	entities, triples, current, err := s.repo.KGCounts(ctx, teamID)
	if err != nil {
		return KGStatsResult{}, err
	}
	preds, err := s.repo.KGPredicates(ctx, teamID)
	if err != nil {
		return KGStatsResult{}, err
	}
	return KGStatsResult{
		Entities: entities, Triples: triples, CurrentFacts: current, ExpiredFacts: triples - current,
		RelationshipTypes: preds,
	}, nil
}

// KGTimeline returns a chronological page of facts (validity start ascending, open
// starts last), for one entity or — when entity is empty — across the whole graph.
func (s *Service) KGTimeline(ctx context.Context, teamID, entity string) ([]KGFact, string, error) {
	label := "all"
	eid := ""
	if strings.TrimSpace(entity) != "" {
		ent, err := sanitizeKGValue(entity, "entity")
		if err != nil {
			return nil, "", err
		}
		label = ent
		eid = normalizeEntityID(ent)
	}
	rows, err := s.repo.KGTimeline(ctx, teamID, eid)
	if err != nil {
		return nil, "", err
	}
	// Resolve both endpoints' names in one batch.
	idset := map[string]struct{}{}
	for _, row := range rows {
		idset[row.Subject] = struct{}{}
		idset[row.Object] = struct{}{}
	}
	ids := make([]string, 0, len(idset))
	for id := range idset {
		ids = append(ids, id)
	}
	names, err := s.repo.KGEntityNames(ctx, teamID, ids)
	if err != nil {
		return nil, "", err
	}
	facts := make([]KGFact, len(rows))
	for i, row := range rows {
		facts[i] = kgFact("", names[row.Subject], row.Predicate, names[row.Object], row)
	}
	return facts, label, nil
}

// kgFact builds a KGFact from a row with the names already resolved.
func kgFact(direction, subject, predicate, object string, row kgTripleRow) KGFact {
	return KGFact{
		Direction: direction, Subject: subject, Predicate: predicate, Object: object,
		ValidFrom: row.ValidFrom, ValidTo: row.ValidTo, Confidence: row.Confidence,
		SourceCloset: row.SourceCloset, SourceFile: row.SourceFile,
		SourceDrawerID: row.SourceDrawerID, RecordedAt: row.ExtractedAt, EndedReason: row.EndedReason,
		Current: row.ValidTo == "",
		Derived: row.Derived != nil && *row.Derived,
	}
}

// otherIDs collects the far-endpoint entity ids of a set of triples (objects when
// the queried entity is the subject, subjects otherwise) for name resolution.
func otherIDs(rows []kgTripleRow, queriedIsSubject bool) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if queriedIsSubject {
			ids = append(ids, row.Object)
		} else {
			ids = append(ids, row.Subject)
		}
	}
	return ids
}

// inEffectAt reports whether a fact is valid at asOfKey (a normalized datetime).
// An empty asOfKey means "no time filter" — every fact passes. Otherwise the fact
// must have started by then and not yet ended by then.
func inEffectAt(row kgTripleRow, asOfKey string) bool {
	if asOfKey == "" {
		return true
	}
	if row.ValidFrom != "" && temporalStartKey(row.ValidFrom) > asOfKey {
		return false
	}
	if row.ValidTo != "" && temporalEndKey(row.ValidTo) < asOfKey {
		return false
	}
	return true
}

// DerivedEdgePredicate is the one reserved verb a server-derived containment edge
// uses. It is fixed rather than inferred: a predicate the server picks per drawer
// would be a vocabulary nobody agreed to, spread across the whole corpus, and
// unremovable without knowing which verbs were the server's.
const DerivedEdgePredicate = "holds"

// DerivedEdgeSubject is the node a derived edge hangs a drawer from: the drawer's
// own room, as a stable label.
//
// The room is chosen over the wing because it is the finest scope the drawer
// already carries, and over a per-drawer node because that would make an edge
// from a thing to itself — reachable from nothing, which is where the corpus
// already is.
func DerivedEdgeSubject(wing, room string) string {
	return "room:" + wing + "/" + room
}

// EdgeAttachment says what attachDerivedEdge actually did, because "no error"
// covered three different outcomes and the caller reported all of them as a
// freshly derived edge — so a drawer a writer had deliberately placed came back
// claiming the server had guessed for it.
type EdgeAttachment int

// The three outcomes. Authored and AlreadyDerived both mean "nothing was written".
const (
	EdgeAuthored EdgeAttachment = iota
	EdgeAlreadyDerived
	EdgeNewlyDerived
)

// attachDerivedEdge makes a newly filed drawer reachable by traversal.
//
// Measured 2026-08-26 on the live palace: 57 of 1,985 drawers carry any edge
// (2.9%), and 0 are named as a triple OBJECT — so the taxonomy pattern the team's
// own operating skill is built on has zero adoption in the workspace that wrote
// it. This is the write-path half of the fix; the existing 1,928 orphans need a
// backfill, which is deferred with a receipt in BACKLOG.md.
//
// An AUTHORED edge always wins. If any triple already names this drawer as its
// object, nothing is derived: the writer has said where it belongs, and a server
// guess must not sit beside a human decision as though the two were equivalent.
func (s *Service) attachDerivedEdge(ctx context.Context, teamID string, d Drawer) (EdgeAttachment, error) {
	existing, err := s.repo.KGTriplesByObject(ctx, teamID, normalizeEntityID(d.ID), KGStatusAll, "")
	if err != nil {
		return EdgeAuthored, err
	}
	for _, row := range existing {
		if row.Derived == nil || !*row.Derived {
			return EdgeAuthored, nil // authored; leave it alone
		}
	}

	subj := DerivedEdgeSubject(d.Wing, d.Room)
	subID, objID := normalizeEntityID(subj), normalizeEntityID(d.ID)
	p := normalizePredicate(DerivedEdgePredicate)
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.repo.UpsertKGEntity(ctx, teamID, subID, subj, now); err != nil {
		return EdgeAuthored, err
	}
	if err := s.repo.UpsertKGEntity(ctx, teamID, objID, d.ID, now); err != nil {
		return EdgeAuthored, err
	}
	if id, err := s.repo.CurrentTripleID(ctx, teamID, subID, p, objID); err != nil {
		return EdgeAuthored, err
	} else if id != "" {
		return EdgeAlreadyDerived, nil
	}
	derived := true
	return EdgeNewlyDerived, s.repo.InsertKGTriple(ctx, kgTripleRow{
		TeamID: teamID, ID: tripleID(subID, p, objID, "", now),
		Subject: subID, Predicate: p, Object: objID,
		Confidence: 1.0, SourceDrawerID: d.ID, ExtractedAt: now,
		Derived: &derived,
	})
}

// WingRootSubject is the by-name address of a wing's entry point: `<wing>.root`.
//
// ⚠ IT IS A NAME A SESSION CAN TYPE, which is the whole reason it exists beside
// DerivedEdgeSubject. `room:<wing>/llm_init` is derived from where a drawer
// happened to be filed, so reaching it means knowing the room-naming convention
// first; `<wing>.root` is chosen, and a session that knows one string can reach
// any wing's front door. Derived-versus-chosen is the distinction BACKLOG.md
// records under "The must-load tier is reachable by a chosen name on ONE palace"
// — named by heading rather than by record number, because the record that
// argued it was closed unmerged on 2026-08-30 and a number would resolve to
// nothing.
func WingRootSubject(wing string) string {
	return wing + ".root"
}

// attachWingRootEdge points a wing's by-name root at its entry room, so the
// address a session can guess resolves to the one the code already mints.
//
// ⚠ UNTIL THIS EXISTED, NOTHING IN THE CODEBASE CREATED A `.root` NODE AT ALL —
// grepped 2026-08-30 across non-test source: zero hits. Every `<wing>.root` in
// any palace was hand-authored through am_kg_add, which is why three sessions in
// three repositories each got `unknown_term` from the first call the entry
// protocol tells them to make, on a graph holding 839 entities and 545 current
// facts. The tier was not missing; the door had no name.
//
// ⚠ IT FIRES ONLY FOR THE ENTRY ROOM. Minting a root edge for every room would
// put the fan-out back on one node, which is the 109-edge front door this
// project already measured spilling at 63KB. One wing, one root, one hop to the
// room am_entry_point resolves.
//
// The MEMBERSHIP of the tier is deliberately not touched: which records a session
// must load is a judgement no code can make — a record is in that tier because
// you cannot notice you needed it until after you have broken something. This
// mints the skeleton; curating what hangs off it stays a human or agent act.
func (s *Service) attachWingRootEdge(ctx context.Context, teamID, wing string) error {
	return s.repo.EnsureWingRoot(ctx, teamID, wing)
}

// EnsureWingRoot mints `<wing>.root --holds--> room:<wing>/llm_init` unless that
// edge already exists, and is the single definition of what a wing root IS.
//
// ⚠ IT LIVES ON Repo RATHER THAN Service SO THE BOOT PATH CAN REACH IT.
// BackfillWingRoots runs inside buildServicesWith's prepare block, beside
// BackfillContentKeys, where no Service has been composed yet — a Service-only
// mint would have to run after composition, which is outside the block that
// distinguishes the writing path from the read-only one.
func (r *Repo) EnsureWingRoot(ctx context.Context, teamID, wing string) error {
	subj := WingRootSubject(wing)
	obj := DerivedEdgeSubject(wing, EntryRoom)
	subID, objID := normalizeEntityID(subj), normalizeEntityID(obj)
	p := normalizePredicate(DerivedEdgePredicate)
	now := time.Now().UTC().Format(time.RFC3339)

	if err := r.UpsertKGEntity(ctx, teamID, subID, subj, now); err != nil {
		return err
	}
	if err := r.UpsertKGEntity(ctx, teamID, objID, obj, now); err != nil {
		return err
	}
	// Idempotent: a wing gets one root edge however many entry-room drawers it
	// accumulates — and however many times the backfill runs over it.
	if id, err := r.CurrentTripleID(ctx, teamID, subID, p, objID); err != nil {
		return err
	} else if id != "" {
		return nil
	}
	derived := true
	return r.InsertKGTriple(ctx, kgTripleRow{
		TeamID: teamID, ID: tripleID(subID, p, objID, "", now),
		Subject: subID, Predicate: p, Object: objID,
		Confidence: 1.0, ExtractedAt: now, Derived: &derived,
	})
}

// endWingRootIfEntryRoomIsEmpty ends a wing's by-name root once its entry room
// holds nothing live, and is the move-OUT half EnsureWingRoot shipped without.
//
// ⚠ THE ASYMMETRY WAS INVISIBLE TO THE OBVIOUS GUARD. endDerivedEdgesFor filters
// `object IN drawerIDs`, so it ends the edges that POINT AT a moved drawer. A wing
// root's object is the ROOM node, not a drawer, so no set of drawer ids reaches it
// and a move emptied the room while leaving the root current. What that produces is
// not a missing answer but a confident empty one: `<wing>.root` resolves `matched`,
// and the hop a session makes next answers known_term_no_facts with zero edges —
// the exact shape BackfillWingRoots' comment calls worse than unknown_term, arriving
// through the move path instead of through the backfill. Found by review on PR #147.
//
// ⚠ THE TEST IS LIVE `holds` EDGES, NOT ROWS, for the reason the backfill's query is:
// endDerivedEdgesFor ends a room's holds edge when the drawer it names is retracted,
// so "no live edge" already covers a room whose records are all retracted as well as
// one whose records have left. Counting rows would keep a root over a room no session
// can read, which is the population the backfill exists to refuse.
//
// It takes a *gorm.DB rather than sitting on Repo so moveMemory can call it inside
// the transaction that did the relabelling: a collision that rolls the move back must
// roll the root's ending back with it, or a wing loses its front door to a move that
// never happened.
func endWingRootIfEntryRoomIsEmpty(db *gorm.DB, teamID, wing, endedAt, reason string) error {
	roomID := normalizeEntityID(DerivedEdgeSubject(wing, EntryRoom))
	p := normalizePredicate(DerivedEdgePredicate)

	var live int64
	if err := db.Model(&kgTripleRow{}).
		Where("team_id = ? AND subject = ? AND predicate = ? AND valid_to = '' AND derived = ?",
			teamID, roomID, p, true).
		Count(&live).Error; err != nil {
		return err
	}
	if live > 0 {
		return nil
	}
	return db.Model(&kgTripleRow{}).
		Where("team_id = ? AND subject = ? AND predicate = ? AND object = ? AND valid_to = '' AND derived = ?",
			teamID, normalizeEntityID(WingRootSubject(wing)), p, roomID, true).
		Updates(map[string]any{"valid_to": endedAt, "ended_reason": reason}).Error
}

// BackfillWingRoots gives a name to every entry room that has none, and returns
// how many roots it minted.
//
// ⚠ THE MINT FIRES ON A WRITE, SO A WING THAT STOPPED WRITING KEEPS A NAMELESS
// DOOR FOREVER. attachWingRootEdge was added on 2026-08-30 and only runs when a
// drawer lands in the entry room; nothing walked the rooms that were already
// there. Measured on this project's own palace the next morning:
// wing_agentmemories filed its entry records at 09:34-09:46 and wing_craft filed
// one at 10:27 — so craft, playtrix and quality-harness all had roots and
// agentmemories, forty minutes too early, answered unknown_term to the very first
// call its entry protocol prescribes. A fix that only helps wings which happen to
// write again is not a fix for the wings that need it.
//
// It runs on every prepared boot rather than as a migration, for the reason
// BackfillContentKeys records: goose stamps a version the first time its SQL runs
// and never runs it again, so a backfill expressed as "runs once" cannot resume
// after an abort. On a fully-rooted palace this costs one bounded SELECT.
//
// ⚠ CURRENT ROWS ONLY (`valid_to = ”`). A room whose every entry record has been
// retracted is an empty room, and am_entry_point drops edges it cannot read — so
// a root minted for it would resolve `matched` with nothing behind it, which is a
// worse answer than unknown_term because it reads as an answer. A SUPERSEDED
// entry record still leaves its successor current, so that wing is rooted.
func (r *Repo) BackfillWingRoots(ctx context.Context) (int, error) {
	type wingRow struct {
		TeamID string `gorm:"column:team_id"`
		Wing   string `gorm:"column:wing"`
	}
	// ⚠ THE UNIVERSE IS EDGES, NOT ROWS, and keying it on rows gave a root to a
	// room a session cannot read. am_entry_point resolves the room node's `holds`
	// edges, which attachDerivedEdge mints at FILE time — so a wing whose entry
	// drawers predate that mechanism has current rows and no edges, and it is
	// exactly the population this backfill exists for. Rooting it produced the
	// shape this function's own comment calls worse than unknown_term: the root
	// resolves `matched`, and one hop on the room answers known_term_no_facts with
	// zero edges. Reported by review 2026-08-31 and reproduced here before the fix.
	//
	// Selecting on live holds edges SUBSUMES the valid_to guard rather than
	// replacing it: endDerivedEdgesFor ends a room's holds edge when the drawer it
	// points at is retracted, so a fully retracted entry room has no live edge
	// either — which TestBackfillLeavesAWingWithNoLiveEntryRecordNameless still
	// proves, unchanged, against this narrower query.
	var rows []struct {
		TeamID  string `gorm:"column:team_id"`
		Subject string `gorm:"column:subject"`
	}
	// ⚠ THE LIKE IS A PREFILTER, NOT THE TEST. EntryRoom is "llm_init" and `_` is a
	// single-character wildcard in SQL LIKE, so this pattern also matches
	// room:<wing>/llm-init, llm.init, llm init — any llm?init. Probed: one drawer
	// filed into a room named "llm-init" minted `wing_alpha/llm-init.root`, a root
	// whose name is not a wing, pointing at a node that holds nothing. That is the
	// door-with-nothing-behind-it this function exists to prevent, arriving through
	// the query instead of through the guard. Reported by review 2026-08-31, new
	// with the edge-keyed universe — the row-keyed version compared room = ?.
	//
	// The affixes are therefore checked in Go rather than trusted to the pattern.
	// ESCAPE '\' would also work and is one line, but it puts the correctness in a
	// SQL escape clause that the next person to rename EntryRoom has to remember;
	// HasPrefix/HasSuffix survive that rename on their own.
	err := r.db.WithContext(ctx).Model(&kgTripleRow{}).
		Select("DISTINCT team_id, subject").
		Where("predicate = ? AND valid_to = '' AND subject LIKE ?",
			normalizePredicate(DerivedEdgePredicate), "room:%/"+EntryRoom).
		Find(&rows).Error
	if err != nil {
		return 0, fmt.Errorf("read the entry rooms a session can read: %w", err)
	}
	wings := make([]wingRow, 0, len(rows))
	for _, row := range rows {
		// The room entity is "room:<wing>/<room>"; a wing name is sanitised and
		// carries no slash, so stripping the exact affixes recovers it.
		//
		// ⚠ NOT TrimPrefix/TrimSuffix ALONE: both no-op silently when the affix is
		// absent, so a subject the wildcard let through arrived here looking like a
		// wing name and was rooted as one.
		if !strings.HasPrefix(row.Subject, "room:") || !strings.HasSuffix(row.Subject, "/"+EntryRoom) {
			continue
		}
		wing := strings.TrimSuffix(strings.TrimPrefix(row.Subject, "room:"), "/"+EntryRoom)
		if wing == "" {
			continue
		}
		wings = append(wings, wingRow{TeamID: row.TeamID, Wing: wing})
	}
	minted := 0
	for _, row := range wings {
		subID := normalizeEntityID(WingRootSubject(row.Wing))
		objID := normalizeEntityID(DerivedEdgeSubject(row.Wing, EntryRoom))
		p := normalizePredicate(DerivedEdgePredicate)
		// Asked before minting so the RETURNED COUNT is what changed, not what was
		// looked at. EnsureWingRoot is idempotent either way, but a backfill that
		// reports the whole corpus every boot says nothing when it matters.
		id, err := r.CurrentTripleID(ctx, row.TeamID, subID, p, objID)
		if err != nil {
			return minted, fmt.Errorf("check the root of %s: %w", row.Wing, err)
		}
		if id != "" {
			continue
		}
		if err := r.EnsureWingRoot(ctx, row.TeamID, row.Wing); err != nil {
			return minted, fmt.Errorf("mint the root of %s: %w", row.Wing, err)
		}
		minted++
	}
	return minted, nil
}

// endDerivedEdgesFor ends every CURRENT DERIVED edge pointing at these drawers,
// on the handle it is given, and leaves AUTHORED edges alone.
//
// ⚠ ONE HELPER BECAUSE THERE ARE FOUR DOORS, and the first fix only closed one.
// A row can stop being current through a correction, through a re-file that
// purges a source, or through an outright retraction — and every one of them
// used to leave the server's own derived edge pointing at ended content. Review
// 2026-08-30 found the three this missed after supersede was fixed.
//
// ⚠ THE ASYMMETRY IS THE RULE. A DERIVED edge is the server's: it minted it from
// the room, no call lets an author end one, so the server must clean it up. An
// AUTHORED edge is a person's deliberate pointer and must survive even when the
// row it names is superseded — the author may mean exactly that. `derived IS
// NULL` is left alone too: those rows predate the distinction and "not marked
// derived" is not the same claim as "authored" (00028).
//
// It takes a *gorm.DB so a caller inside a transaction passes its tx — reaching
// for s.repo.db there would open a second connection to a file the transaction
// already holds a write lock on.
func endDerivedEdgesFor(db *gorm.DB, teamID string, drawerIDs []string, endedAt, reason string) error {
	if len(drawerIDs) == 0 {
		return nil
	}
	return db.Model(&kgTripleRow{}).
		Where("team_id = ? AND object IN ? AND valid_to = '' AND derived = ?", teamID, drawerIDs, true).
		Updates(map[string]any{"valid_to": endedAt, "ended_reason": reason}).Error
}

// AllKGEntities returns every entity a team owns, for the label backfill.
//
// Unpaged deliberately: the whole point is a one-shot index build, and the live
// palace holds 342 entities (measured 2026-08-26). If a workspace ever grows to
// where this matters, the backfill is the thing to page, not this read.
func (r *Repo) AllKGEntities(ctx context.Context, teamID string) ([]kgEntityRow, error) {
	var rows []kgEntityRow
	err := r.db.WithContext(ctx).Where("team_id = ?", teamID).Find(&rows).Error
	return rows, err
}

// WingsForDrawers resolves specific drawer ids to the wing each is filed in, for
// WingPolicy. Absent ids are simply omitted, which is what makes a dangling
// provenance pointer UNLOCATABLE rather than an error.
//
// Distinct from DrawerWings, which loads the whole team for the drift check: this
// one is asked about the handful of ids a single recall's facts point at.
func (r *Repo) WingsForDrawers(ctx context.Context, teamID string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	// Scanned into a narrow anonymous struct, not []Drawer: Drawer carries an
	// Entities slice gorm cannot map, and Find would error on a type this query
	// never asked for.
	var rows []struct {
		ID   string
		Wing string
	}
	if err := r.db.WithContext(ctx).Model(&drawerRow{}).Select("id", "wing").
		Where("team_id = ? AND id IN ?", teamID, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.Wing
	}
	return out, nil
}

// CorrectionPredicates are the three ways a record can be corrected by a later
// one. All three, always — running only `retracts` on 2026-08-25 shipped a
// pointer to an ADR that was not on main, because the edge that mattered that day
// was a `qualifies`.
var CorrectionPredicates = []string{"retracts", "supersedes", "qualifies"}

// Correction is one record's correction by another, as returned to a reader.
type Correction struct {
	// Predicate is which of the three kinds of correction this is.
	Predicate string `json:"predicate"`
	// ReplacementID is the record that corrects this one, when the correcting
	// record is itself a drawer this viewer may see. Empty when the correcting
	// record lives in another wing — the fact that a correction EXISTS still
	// travels, because a reader who is not told is a reader acting on something
	// somebody has already contradicted.
	ReplacementID string `json:"replacement_id,omitempty"`
	// ElsewhereWing names the wing holding the correcting record when it is not
	// this viewer's. A name, never content — the same rule the fact block obeys.
	ElsewhereWing string `json:"elsewhere_wing,omitempty"`
}

// CorrectionsFor resolves, for each given record id, the corrections attached to
// it — read INCOMING.
//
// Direction is the whole point and it is easy to get backwards. A correction
// attaches to the record it corrects as an INCOMING edge, so an outgoing walk
// from a record can never see that it has been retracted. That is why the team's
// own operating skill instructs agents to run three predicate queries by hand:
// this is the server doing it once, correctly, instead.
//
// One resolver, consumed by both the search path (T5) and the bootstrap (T8).
// Two implementations of the same sweep diverge on the path nobody tested, and
// the one that diverges silently serves contradicted records as current.
func (s *Service) CorrectionsFor(ctx context.Context, teamID string, recordIDs []string, policy WingPolicy) (map[string][]Correction, error) {
	out := map[string][]Correction{}
	if len(recordIDs) == 0 {
		return out, nil
	}
	want := map[string]bool{}
	for _, id := range recordIDs {
		want[normalizeEntityID(id)] = true
	}

	for _, pred := range CorrectionPredicates {
		rows, err := s.repo.KGTriplesByPredicate(ctx, teamID, normalizePredicate(pred), KGStatusCurrent)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			// The OBJECT is the record being corrected; the SUBJECT is the record
			// doing the correcting.
			if !want[row.Object] {
				continue
			}
			c := Correction{Predicate: pred}
			// Authorized on row.Subject — the correcting record, whose id is what
			// ReplacementID exposes — and not on row.SourceDrawerID, which is
			// merely where the fact was extracted from. The two are independent,
			// so checking provenance both disclosed foreign replacements (local
			// provenance, foreign corrector) and suppressed local ones (absent
			// provenance, local corrector).
			placement, wing := policy.Place(ctx, row.Subject)
			if policy.MayReturnContent(placement) {
				c.ReplacementID = row.Subject
			} else if placement == PlacementForeign {
				c.ElsewhereWing = wing
			}
			out[row.Object] = append(out[row.Object], c)
		}
	}
	return out, nil
}

// KGTriplesForEntities loads every current triple touching ANY of the given
// entity ids, in one statement per direction rather than one query per entity.
//
// factsFor previously issued a full KGQuery per candidate entity — each costing
// several statements — across every entity on every drawer in the candidate pool.
// At the shipped limit of 10 the pool is 30 drawers, so a single am_search could
// reach four figures of serial SQL. This is the batched replacement: the cost
// becomes two statements plus one name resolution, independent of how many
// entities the page mentions.
func (r *Repo) KGTriplesForEntities(ctx context.Context, teamID string, ids []string, status string) ([]kgTripleRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []kgTripleRow
	q := r.db.WithContext(ctx).Where("team_id = ? AND (subject IN ? OR object IN ?)", teamID, ids, ids)
	switch status {
	case KGStatusCurrent:
		q = q.Where("valid_to = ''")
	case KGStatusEnded:
		q = q.Where("valid_to <> ''")
	}
	// Ordered, because which duplicate SPELLING survives factsFor's
	// canonical-key dedup is decided by iteration order over these rows: the
	// canonical key collapses both spellings of a two-directional walk, so the
	// final sort over facts cannot repair a nondeterministic winner. Without
	// this the winner is backend row order.
	if err := q.Order("subject, predicate, object, valid_from, extracted_at").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// DropDerivedEdgesFor removes the server-derived edges naming any of these
// drawers, and only those.
//
// Deletion paths purge drawer rows and vectors and left their derived triples
// behind, still current, pointing at ids that no longer resolve. That is worse
// than an orphan drawer: it is an edge asserting a record exists where none
// does, and it accumulates on every re-file of changed content because a changed
// drawer gets a NEW id and a new edge beside the stale one.
//
// AUTHORED edges are never touched. A writer's placement outliving the drawer it
// named is a fact about the graph a human should resolve, not something a purge
// should silently erase.
func (r *Repo) DropDerivedEdgesFor(ctx context.Context, teamID string, drawerIDs []string) error {
	if len(drawerIDs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(drawerIDs))
	for _, id := range drawerIDs {
		ids = append(ids, normalizeEntityID(id))
	}
	return r.db.WithContext(ctx).
		Where("team_id = ? AND object IN ? AND derived = ?", teamID, ids, true).
		Delete(&kgTripleRow{}).Error
}
