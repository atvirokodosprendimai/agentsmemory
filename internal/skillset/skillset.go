// Package skillset is the global platform-instructions bounded context. It owns a
// single, superadmin-authored document — the "wakeup" playbook that teaches an
// agent how to drive the server's am_* tools: which to call, in what order, and
// which centralised skills to load. It is the remote, platform-owned twin of a
// local /M-style bootstrap.
//
// This is deliberately NOT internal/skill. That context is per-team and
// member-authored (each workspace curates its own centralised skills); this one
// is a SINGLE global artifact, identical for every tenant and editable only by a
// platform superadmin. They share nothing but the "centralised authored
// instructions" idea, so each owns its own table, service, and bounds — keeping
// the per-team write gate (writer/admin) and the platform write gate (superadmin)
// from ever bleeding into one another.
package skillset

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// globalID is the primary key of the one row this table ever holds. The platform
// has exactly one wakeup playbook, so every read and write targets this id.
const globalID = "global"

// maxContentLen bounds a stored playbook. It is a curated document — large, but
// not unbounded — and the cap guards every write surface (the dashboard editor
// and any future tool) because they all funnel through Service.Set.
const maxContentLen = 1_000_000

// ErrForbidden is returned when a non-superadmin attempts to write the global
// skillset. The write path is reserved for the platform owner; a team admin's
// authority stops at their own workspace's skills.
var ErrForbidden = errors.New("skillset: editing the global skillset requires a platform superadmin")

// ErrInvalidContent rejects an empty or oversized playbook before it is stored.
var ErrInvalidContent = errors.New("skillset: content must be non-empty and within the size limit")

// ErrNotSet is returned by the repo when the global skillset has never been
// written. The service translates it into a found=false read rather than an
// error, since "no playbook yet" is a normal state, not a failure.
var ErrNotSet = errors.New("skillset: global skillset not set")

// Skillset is the single global wakeup playbook. content is the verbatim body an
// agent receives from am_skillset; version is bumped on every edit so an operator
// can see the playbook evolve and a stale cache can be detected.
type Skillset struct {
	ID        string `gorm:"primaryKey"`
	Content   string
	Version   int
	UpdatedBy string
	CreatedAt string
	UpdatedAt string
}

// TableName pins the gorm model to the goose-managed table (migration 00012).
func (Skillset) TableName() string { return "skillset" }

// Repo is the persistence boundary for the global skillset. It is a struct over a
// *gorm.DB; consumers depend on the Store behaviour they need, not on gorm.
type Repo struct {
	db *gorm.DB
}

// NewRepo constructs a Repo over an open gorm connection.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// Get loads the single global skillset, or ErrNotSet when it has never been
// written. The lookup is by the fixed singleton id, so it is a fast keyed read —
// no scan, no per-tenant scope (the playbook is the same for everyone).
func (r *Repo) Get(ctx context.Context) (Skillset, error) {
	var s Skillset
	err := r.db.WithContext(ctx).Where("id = ?", globalID).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Skillset{}, ErrNotSet
	}
	return s, err
}

// Set upserts the global skillset, bumping the version on update. It is the only
// write path; the singleton row is created on first write and replaced in place
// thereafter, so there is never more than one row. updatedBy records provenance.
func (r *Repo) Set(ctx context.Context, content, updatedBy string) (Skillset, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	existing, err := r.Get(ctx)
	switch {
	case errors.Is(err, ErrNotSet):
		s := Skillset{
			ID: globalID, Content: content, Version: 1,
			UpdatedBy: updatedBy, CreatedAt: now, UpdatedAt: now,
		}
		return s, r.db.WithContext(ctx).Create(&s).Error
	case err != nil:
		return Skillset{}, err
	default:
		existing.Content = content
		existing.Version++
		existing.UpdatedBy = updatedBy
		existing.UpdatedAt = now
		return existing, r.db.WithContext(ctx).Save(&existing).Error
	}
}

// Store is the persistence behaviour the Service needs. Defined at the consumer so
// the Service can be tested with a fake and never imports gorm transitively.
type Store interface {
	Get(ctx context.Context) (Skillset, error)
	Set(ctx context.Context, content, updatedBy string) (Skillset, error)
}

// SuperHolder is the minimal slice of an authenticated caller the write path
// needs: who they are (recorded as updated_by) and whether they are a platform
// superadmin. Defining it here (not importing the web/session type) keeps this
// context decoupled from how superadmin status is determined upstream.
type SuperHolder interface {
	User() string
	IsSuperAdmin() bool
}

// Service holds the global-skillset use-cases over any Store.
type Service struct {
	repo Store
}

// NewService wires a Service over a Store implementation.
func NewService(store Store) *Service { return &Service{repo: store} }

// Get returns the global playbook and found=false when it has never been set. Any
// authenticated tenant may read it — it is identical for everyone — so there is no
// authorization here; the read is the whole point of am_skillset.
func (s *Service) Get(ctx context.Context) (sk Skillset, found bool, err error) {
	sk, err = s.repo.Get(ctx)
	if errors.Is(err, ErrNotSet) {
		return Skillset{}, false, nil
	}
	if err != nil {
		return Skillset{}, false, err
	}
	return sk, true, nil
}

// Set writes the global playbook. It enforces the superadmin gate as the single
// platform-write enforcement point (mirroring how skill.Service.Update centralises
// the per-team role gate), then validates the untrusted payload before storing.
func (s *Service) Set(ctx context.Context, caller SuperHolder, content string) (Skillset, error) {
	if !caller.IsSuperAdmin() {
		return Skillset{}, ErrForbidden
	}
	// Validate before persisting: a non-empty body within the size cap. Content is
	// kept verbatim (only length-checked) — it is a document, not structured input.
	if strings.TrimSpace(content) == "" || len(content) > maxContentLen {
		return Skillset{}, ErrInvalidContent
	}
	return s.repo.Set(ctx, content, caller.User())
}

// DefaultPlaybook is the wakeup playbook a fresh database is seeded with, so
// am_skillset is useful on day one before any superadmin edits it. It is held
// here (not inline in the seed) so the starting text is version-controlled and the
// superadmin edits a real document rather than a blank page. The live tool
// catalogue is appended by the am_skillset tool, so this text covers only the
// when/which/how that the bare tool descriptions cannot.
const DefaultPlaybook = `# agentsmemory — wakeup skillset

You are connected to the agentsmemory MCP server. Every tool is prefixed ` + "`am_`" + ` and
scoped to your team. This server is your long-term memory across sessions — read
from it before acting, write to it before you stop.

## On wake-up (every session, in this order)
1. ` + "`am_status`" + ` — your team, role, memory overview (wings → rooms), and remaining quota.
2. ` + "`am_get_aaak_spec`" + ` — the compressed dialect you WRITE memories in.
3. ` + "`am_search \"<your task>\"`" + ` — recall first. Never re-derive what you already remember.
4. ` + "`am_list_skills`" + ` then ` + "`am_load_skill <name>`" + ` — pull the centralised skills you
   need (e.g. ` + "`effective-go`" + `) instead of guessing the team's conventions.

## While you work
- ` + "`am_search`" + ` again the moment you reach unfamiliar code, decisions, or rationale.
- ` + "`am_check_duplicate`" + ` before filing near-identical content.
- ` + "`am_add_drawer <wing> <room>`" + ` to file new memory VERBATIM (never summarised).

## Before you stop
- ` + "`am_diary_write <agent>`" + ` — journal what you did, decided, and learned (in AAAK).
- ` + "`am_kg_add`" + ` — record durable subject → predicate → object facts.
- ` + "`am_create_tunnel`" + ` — link related memories across wings.

The live catalogue of every available tool follows below.
`
