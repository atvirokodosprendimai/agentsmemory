package palace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm/clause"
)

// Closets are the topic/quote pointer index the miner builds alongside drawers. A
// closet packs short pointer LINES — each a "topic|entities|date:Lstart-Lend|→drawer_refs"
// shorthand for a span of mined content — embedded and searched next to drawers.
// A closet that matches a query lends a rank boost to the drawers from the same
// source (search closet-boost). Closets are derived: they are rebuilt (purged +
// re-written) every time their source is re-mined, so they never go stale. Ported
// from the frozen palace.build_closet_lines / upsert_closet_lines.

const (
	// closetEntityLimit caps how many entities a closet line lists (frozen top-5).
	closetEntityLimit = 5
	// closetCharLimit packs lines into a closet until ~1500 chars, then starts a
	// new one — never splitting a line across closets (frozen CLOSET_CHAR_LIMIT).
	closetCharLimit = 1500
	// closetExtractWindow bounds the content scanned for topics/quotes/entities.
	closetExtractWindow = 5000
	// closetTopicLimit / closetQuoteLimit cap topics and quotes per source.
	closetTopicLimit = 12
	closetQuoteLimit = 3
	// closetDrawerRefLimit is how many drawer ids a pointer line references.
	closetDrawerRefLimit = 3
)

var (
	// closetActionRE finds work-describing phrases ("built the cache", "fixed auth")
	// — the frozen action-verb topic pattern.
	closetActionRE = regexp.MustCompile(`(?i)\b(?:built|fixed|wrote|added|pushed|tested|created|decided|migrated|reviewed|deployed|configured|removed|updated)\s+[\w\s]{3,40}`)
	// closetHeaderRE captures markdown headers (levels 1-3) as topics.
	closetHeaderRE = regexp.MustCompile(`(?m)^#{1,3}\s+(.{5,60})$`)
	// closetQuoteRE captures double-quoted spans of a sentence-ish length.
	closetQuoteRE = regexp.MustCompile(`"([^"]{15,150})"`)
)

// buildClosetLines produces a source's closet pointer lines from its content and
// the ids of the drawers it produced. Each line is one complete topic or quote
// pointer; dateLineSeg (when non-empty) inserts the Tier-6a "YYYY-MM-DD:Lstart-Lend"
// locator. When no topic or quote is found, a single fallback line keyed on the
// source location is emitted so the source is still discoverable via closets.
func buildClosetLines(source string, drawerIDs []string, content, wing, room, dateLineSeg string) []string {
	runes := []rune(content)
	if len(runes) > closetExtractWindow {
		runes = runes[:closetExtractWindow]
	}
	window := string(runes)

	refs := drawerIDs
	if len(refs) > closetDrawerRefLimit {
		refs = refs[:closetDrawerRefLimit]
	}
	drawerRef := strings.Join(refs, ",")
	entityStr := strings.Join(closetEntities(window), ";")

	// pointer assembles one line: 4 segments with the date locator, else 3.
	pointer := func(prefix string) string {
		if dateLineSeg != "" {
			return prefix + "|" + entityStr + "|" + dateLineSeg + "|→" + drawerRef
		}
		return prefix + "|" + entityStr + "|→" + drawerRef
	}

	// Topics: action phrases + headers, deduped (lowercased) and capped.
	seen := map[string]struct{}{}
	var topics []string
	add := func(t string) {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			return
		}
		if _, dup := seen[t]; dup {
			return
		}
		seen[t] = struct{}{}
		topics = append(topics, t)
	}
	for _, m := range closetActionRE.FindAllString(window, -1) {
		add(m)
	}
	for _, m := range closetHeaderRE.FindAllStringSubmatch(window, -1) {
		add(m[1])
	}
	if len(topics) > closetTopicLimit {
		topics = topics[:closetTopicLimit]
	}

	var lines []string
	for _, t := range topics {
		lines = append(lines, pointer(t))
	}
	quotes := closetQuoteRE.FindAllStringSubmatch(window, -1)
	for i, q := range quotes {
		if i >= closetQuoteLimit {
			break
		}
		lines = append(lines, pointer(`"`+q[1]+`"`))
	}

	// Fallback: nothing matched, but the source still deserves a pointer so it can
	// be recalled. Key it on wing/room/<source-stem>.
	if len(lines) == 0 {
		name := path.Base(source)
		if ext := path.Ext(name); ext != "" {
			name = strings.TrimSuffix(name, ext)
		}
		if len([]rune(name)) > 40 {
			name = string([]rune(name)[:40])
		}
		lines = append(lines, pointer(wing+"/"+room+"/"+name))
	}
	return lines
}

// packClosets greedily packs pointer lines into closet documents, each at most
// limit characters, never splitting a line across two closets (frozen
// upsert_closet_lines packing). Lines are newline-joined within a document. A
// single oversized line still becomes its own document rather than being dropped.
func packClosets(lines []string, limit int) []string {
	var docs []string
	var cur []string
	curChars := 0
	flush := func() {
		if len(cur) > 0 {
			docs = append(docs, strings.Join(cur, "\n"))
			cur = nil
			curChars = 0
		}
	}
	for _, line := range lines {
		// +1 accounts for the newline that will separate this line from the prior.
		if curChars > 0 && curChars+len(line)+1 > limit {
			flush()
		}
		cur = append(cur, line)
		curChars += len(line) + 1
	}
	flush()
	return docs
}

// closetDateLineSegment builds the Tier-6a "<date>:L<start>-L<end>" locator from
// the first chunk's line range and the source's date (content date if known, else
// the filed-at date). It returns "" when there is no line range, dropping the
// closet line back to its 3-segment form.
func closetDateLineSegment(first mineChunk, contentDate, filedAtDate string) string {
	date := contentDate
	if date == "" {
		date = filedAtDate
	}
	if date == "" {
		return ""
	}
	return date + ":L" + strconv.Itoa(first.LineStart) + "-L" + strconv.Itoa(first.LineEnd)
}

// closetID is the deterministic identity of one packed closet: a hash of its
// tenant, location, source and sequence number. Like DrawerID it NUL-separates
// parts so distinct tuples cannot collide by concatenation, and folds in teamID
// so closet ids are unique per tenant.
func closetID(teamID, wing, room, source string, num int) string {
	h := sha256.New()
	for _, part := range []string{teamID, wing, room, source, strconv.Itoa(num)} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// closetNamespace is the per-tenant vector namespace closets live in — distinct
// from the drawer namespace (the bare teamID) so closet and drawer searches never
// mix, while both ride the same store seam.
func closetNamespace(teamID string) string { return teamID + "::closets" }

// Closet is one packed pointer index: its id, tenant, location, the source it
// indexes, the packed document text (embedded for search), its top entities, and
// when it was filed.
type Closet struct {
	ID         string
	TeamID     string
	Wing       string
	Room       string
	SourceFile string
	Document   string
	Entities   []string
	FiledAt    string
}

// closetRow is the gorm view of the closets table (migration 00008).
type closetRow struct {
	TeamID     string `gorm:"column:team_id;primaryKey"`
	ID         string `gorm:"column:id;primaryKey"`
	Wing       string `gorm:"column:wing"`
	Room       string `gorm:"column:room"`
	SourceFile string `gorm:"column:source_file"`
	Document   string `gorm:"column:document"`
	Entities   string `gorm:"column:entities"` // semicolon-joined
	FiledAt    string `gorm:"column:filed_at"`
	// EmbeddedAt mirrors drawerRow: NULL while awaiting background embedding,
	// RFC3339 once the closet's vector is built (migration 00013).
	EmbeddedAt *string `gorm:"column:embedded_at"`
}

// TableName pins the table so gorm does not pluralise to "closet_rows".
func (closetRow) TableName() string { return "closets" }

// closetToRow flattens a domain Closet into its storage shape (sans embedded_at,
// which the two save paths set differently).
func closetToRow(c Closet) closetRow {
	return closetRow{
		TeamID:     c.TeamID,
		ID:         c.ID,
		Wing:       c.Wing,
		Room:       c.Room,
		SourceFile: c.SourceFile,
		Document:   c.Document,
		Entities:   strings.Join(c.Entities, ";"),
		FiledAt:    c.FiledAt,
	}
}

// SaveClosets upserts closets by (team_id, id). An empty slice is a no-op. Like
// drawer Save, every caller embedded the closet first, so the row is stamped
// embedded_at=now and never enters the background queue.
func (r *Repo) SaveClosets(ctx context.Context, closets []Closet) error {
	if len(closets) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rows := make([]closetRow, len(closets))
	for i, c := range closets {
		rows[i] = closetToRow(c)
		rows[i].EmbeddedAt = &now
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&rows).Error
}

// SaveClosetsUnembedded inserts closet rows with embedded_at NULL — the absorb
// half of a migration import. On conflict it refreshes the mutable columns but
// leaves embedded_at untouched (mirrors SaveUnembedded): a re-absorb updates
// metadata without re-queueing an already-indexed closet. The id hashes the
// document, so document/source never differ on a conflict.
func (r *Repo) SaveClosetsUnembedded(ctx context.Context, closets []Closet) error {
	if len(closets) == 0 {
		return nil
	}
	rows := make([]closetRow, len(closets))
	for i, c := range closets {
		rows[i] = closetToRow(c) // EmbeddedAt nil -> NULL -> pending on insert
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "team_id"}, {Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"wing", "room", "source_file", "document", "entities", "filed_at",
			}),
		}).
		Create(&rows).Error
}

// PendingClosets returns up to limit un-embedded closets for a team, oldest first.
func (r *Repo) PendingClosets(ctx context.Context, teamID string, limit int) ([]Closet, error) {
	if limit <= 0 {
		limit = 64
	}
	var rows []closetRow
	if err := r.db.WithContext(ctx).
		Where("team_id = ? AND embedded_at IS NULL", teamID).
		Order("filed_at ASC, id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Closet, 0, len(rows))
	for _, row := range rows {
		out = append(out, Closet{
			ID:         row.ID,
			TeamID:     row.TeamID,
			Wing:       row.Wing,
			Room:       row.Room,
			SourceFile: row.SourceFile,
			Document:   row.Document,
			Entities:   splitEntities(row.Entities),
			FiledAt:    row.FiledAt,
		})
	}
	return out, nil
}

// MarkClosetsEmbedded stamps embedded_at on the given closet ids within a team,
// after their vectors are durably upserted. An empty id slice is a no-op.
func (r *Repo) MarkClosetsEmbedded(ctx context.Context, teamID string, ids []string, at string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Model(&closetRow{}).
		Where("team_id = ? AND id IN ?", teamID, ids).
		Update("embedded_at", at).Error
}

// TeamsWithPendingClosets lists distinct teams with at least one un-embedded
// closet. limit bounds the slice (0 = unbounded).
func (r *Repo) TeamsWithPendingClosets(ctx context.Context, limit int) ([]string, error) {
	q := r.db.WithContext(ctx).
		Model(&closetRow{}).
		Distinct("team_id").
		Where("embedded_at IS NULL")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var teams []string
	if err := q.Pluck("team_id", &teams).Error; err != nil {
		return nil, err
	}
	return teams, nil
}

// PendingClosetCount is how many of a team's closets still await embedding.
func (r *Repo) PendingClosetCount(ctx context.Context, teamID string) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).
		Model(&closetRow{}).
		Where("team_id = ? AND embedded_at IS NULL", teamID).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// ClosetIDsBySource returns the ids of every closet built from a source, so a
// re-mine can drop the prior closets (rows + vectors) before writing fresh ones.
func (r *Repo) ClosetIDsBySource(ctx context.Context, teamID, source string) ([]string, error) {
	var ids []string
	if err := r.db.WithContext(ctx).
		Model(&closetRow{}).
		Where("team_id = ? AND source_file = ?", teamID, source).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// DeleteClosetsBySource removes every closet row for a source within a team — the
// row half of a closet purge (the caller drops the matching vectors).
func (r *Repo) DeleteClosetsBySource(ctx context.Context, teamID, source string) error {
	return r.db.WithContext(ctx).
		Where("team_id = ? AND source_file = ?", teamID, source).
		Delete(&closetRow{}).Error
}

// ClosetsByIDs loads closets by id within a team as an id->Closet map, so search
// can resolve closet hits back to their source for the rank boost. Absent ids are
// simply missing from the map.
func (r *Repo) ClosetsByIDs(ctx context.Context, teamID string, ids []string) (map[string]Closet, error) {
	out := make(map[string]Closet, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []closetRow
	if err := r.db.WithContext(ctx).
		Where("team_id = ? AND id IN ?", teamID, ids).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = Closet{
			ID:         row.ID,
			TeamID:     row.TeamID,
			Wing:       row.Wing,
			Room:       row.Room,
			SourceFile: row.SourceFile,
			Document:   row.Document,
			Entities:   splitEntities(row.Entities),
			FiledAt:    row.FiledAt,
		}
	}
	return out, nil
}
