package palace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

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

const maxKGValueLen = 128 // frozen MAX_NAME_LENGTH, shared by KG values
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
	if len([]rune(value)) > maxKGValueLen {
		return "", fmt.Errorf("%w: %s exceeds maximum length of %d characters", ErrInvalidInput, field, maxKGValueLen)
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
func (r *Repo) InvalidateKGTriples(ctx context.Context, teamID, subject, predicate, object, ended string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&kgTripleRow{}).
		Where("team_id = ? AND subject = ? AND predicate = ? AND object = ? AND valid_to = ''", teamID, subject, predicate, object).
		Update("valid_to", ended)
	return res.RowsAffected, res.Error
}

// KGTriplesBySubject / KGTriplesByObject load a team's triples on one endpoint.
func (r *Repo) KGTriplesBySubject(ctx context.Context, teamID, subject string) ([]kgTripleRow, error) {
	var rows []kgTripleRow
	err := r.db.WithContext(ctx).Where("team_id = ? AND subject = ?", teamID, subject).Find(&rows).Error
	return rows, err
}

func (r *Repo) KGTriplesByObject(ctx context.Context, teamID, object string) ([]kgTripleRow, error) {
	var rows []kgTripleRow
	err := r.db.WithContext(ctx).Where("team_id = ? AND object = ?", teamID, object).Find(&rows).Error
	return rows, err
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

// KGCounts returns the entity count, total triples, and current (not-ended) triple
// count for a team — the numeric half of kg_stats.
func (r *Repo) KGCounts(ctx context.Context, teamID string) (entities, triples, current int64, err error) {
	if err = r.db.WithContext(ctx).Model(&kgEntityRow{}).Where("team_id = ?", teamID).Count(&entities).Error; err != nil {
		return
	}
	if err = r.db.WithContext(ctx).Model(&kgTripleRow{}).Where("team_id = ?", teamID).Count(&triples).Error; err != nil {
		return
	}
	err = r.db.WithContext(ctx).Model(&kgTripleRow{}).Where("team_id = ? AND valid_to = ''", teamID).Count(&current).Error
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

// KGFact is one fact a query/timeline returns, with display names resolved and the
// current flag computed.
type KGFact struct {
	Direction    string  `json:"direction,omitempty"`
	Subject      string  `json:"subject"`
	Predicate    string  `json:"predicate"`
	Object       string  `json:"object"`
	ValidFrom    string  `json:"valid_from,omitempty"`
	ValidTo      string  `json:"valid_to,omitempty"`
	Confidence   float64 `json:"confidence,omitempty"`
	SourceCloset string  `json:"source_closet,omitempty"`
	Current      bool    `json:"current"`
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
func (s *Service) KGAdd(ctx context.Context, teamID, subject, predicate, object, validFrom, validTo, sourceCloset, sourceFile, sourceDrawerID string) (KGAddResult, error) {
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

	subID, objID, p := normalizeEntityID(subj), normalizeEntityID(obj), normalizePredicate(pred)
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.repo.UpsertKGEntity(ctx, teamID, subID, subj, now); err != nil {
		return KGAddResult{}, err
	}
	if err := s.repo.UpsertKGEntity(ctx, teamID, objID, obj, now); err != nil {
		return KGAddResult{}, err
	}

	fact := subj + " → " + p + " → " + obj
	if existing, err := s.repo.CurrentTripleID(ctx, teamID, subID, p, objID); err != nil {
		return KGAddResult{}, err
	} else if existing != "" {
		return KGAddResult{TripleID: existing, Fact: fact}, nil
	}

	id := tripleID(subID, p, objID, vf, now)
	if err := s.repo.InsertKGTriple(ctx, kgTripleRow{
		TeamID: teamID, ID: id, Subject: subID, Predicate: p, Object: objID,
		ValidFrom: vf, ValidTo: vt, Confidence: 1.0,
		SourceCloset: sourceCloset, SourceFile: sourceFile, SourceDrawerID: sourceDrawerID, ExtractedAt: now,
	}); err != nil {
		return KGAddResult{}, err
	}
	return KGAddResult{TripleID: id, Fact: fact}, nil
}

// KGInvalidate ends a current fact by setting its valid_to (defaulting to today).
// It rejects an end that precedes the fact's own start. Ending a fact never
// deletes it — the history stays queryable as-of an earlier time.
func (s *Service) KGInvalidate(ctx context.Context, teamID, subject, predicate, object, ended string) (fact, resolvedEnded string, err error) {
	subj, err := sanitizeKGValue(subject, "subject")
	if err != nil {
		return "", "", err
	}
	pred, err := SanitizeName(predicate, "predicate")
	if err != nil {
		return "", "", err
	}
	obj, err := sanitizeKGValue(object, "object")
	if err != nil {
		return "", "", err
	}
	e, err := sanitizeISOTemporal(ended, "ended")
	if err != nil {
		return "", "", err
	}
	if e == "" {
		e = time.Now().UTC().Format("2006-01-02")
	}
	subID, objID, p := normalizeEntityID(subj), normalizeEntityID(obj), normalizePredicate(pred)

	// Reject an end before any matching fact's start (the inverted-interval guard).
	current, err := s.repo.CurrentTriples(ctx, teamID, subID, p, objID)
	if err != nil {
		return "", "", err
	}
	for _, row := range current {
		if row.ValidFrom != "" && temporalEndKey(e) < temporalStartKey(row.ValidFrom) {
			return "", "", fmt.Errorf("%w: ended=%q is before valid_from=%q", ErrInvalidInput, e, row.ValidFrom)
		}
	}
	if _, err := s.repo.InvalidateKGTriples(ctx, teamID, subID, p, objID, e); err != nil {
		return "", "", err
	}
	return subj + " → " + p + " → " + obj, e, nil
}

// KGQuery returns an entity's facts, optionally only those in effect at as_of and
// in a chosen direction (outgoing where it is the subject, incoming where it is the
// object, or both). Display names are resolved from the entity table.
func (s *Service) KGQuery(ctx context.Context, teamID, entity, asOf, direction string) ([]KGFact, string, error) {
	ent, err := sanitizeKGValue(entity, "entity")
	if err != nil {
		return nil, "", err
	}
	ao, err := sanitizeISOTemporal(asOf, "as_of")
	if err != nil {
		return nil, "", err
	}
	if direction == "" {
		direction = "both"
	}
	if direction != "outgoing" && direction != "incoming" && direction != "both" {
		return nil, "", fmt.Errorf("%w: direction must be 'outgoing', 'incoming', or 'both'", ErrInvalidInput)
	}
	eid := normalizeEntityID(ent)
	asOfKey := temporalStartKey(ao)

	var facts []KGFact
	if direction == "outgoing" || direction == "both" {
		rows, err := s.repo.KGTriplesBySubject(ctx, teamID, eid)
		if err != nil {
			return nil, "", err
		}
		names, err := s.repo.KGEntityNames(ctx, teamID, otherIDs(rows, true))
		if err != nil {
			return nil, "", err
		}
		for _, row := range rows {
			if !inEffectAt(row, asOfKey) {
				continue
			}
			facts = append(facts, kgFact("outgoing", ent, row.Predicate, names[row.Object], row))
		}
	}
	if direction == "incoming" || direction == "both" {
		rows, err := s.repo.KGTriplesByObject(ctx, teamID, eid)
		if err != nil {
			return nil, "", err
		}
		names, err := s.repo.KGEntityNames(ctx, teamID, otherIDs(rows, false))
		if err != nil {
			return nil, "", err
		}
		for _, row := range rows {
			if !inEffectAt(row, asOfKey) {
				continue
			}
			facts = append(facts, kgFact("incoming", names[row.Subject], row.Predicate, ent, row))
		}
	}
	return facts, ent, nil
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
		SourceCloset: row.SourceCloset, Current: row.ValidTo == "",
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
