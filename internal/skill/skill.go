// Package skill is the centralised-skill bounded context. It serves the
// load_skill MCP tool: a team's agents pull a shared, versioned skill body
// (the SKILL.md content) by name, instead of each developer copy-pasting local
// skill files. Skills are mutable, named, permissioned authored artifacts —
// CRUD rows, deliberately NOT memory drawers — so this context owns a plain
// relational table and shares only tenancy/auth with the memory palace.
package skill

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Bounds on a skill write, so update_skill cannot store a blank name or an
// unbounded body. A skill body (a SKILL.md) can be large but not arbitrary; the
// description is a one-liner, so it is kept short. These guard both write
// surfaces — the MCP update_skill tool and the web editor — since both funnel
// through Service.Update.
const (
	maxSkillNameLen        = 128
	maxSkillContentLen     = 1_000_000
	maxSkillDescriptionLen = 1_024
)

// ErrNotFound is returned when no skill with the given name exists in the team.
var ErrNotFound = errors.New("skill: not found")

// ErrForbidden is returned when a caller lacks the role to mutate a skill.
var ErrForbidden = errors.New("skill: write requires writer or admin role")

// ErrInvalidName / ErrInvalidContent / ErrInvalidDescription reject a malformed
// update_skill payload before it is stored.
var ErrInvalidName = errors.New("skill: name must be non-empty and at most 128 characters")
var ErrInvalidContent = errors.New("skill: content must be non-empty and within the size limit")
var ErrInvalidDescription = errors.New("skill: description must be at most 1024 characters")

// Skill is a centralised, versioned skill owned by a team. content is the body
// an agent loads; version is bumped on every update so a later load serves the
// newest text to every agent.
type Skill struct {
	ID          string `gorm:"primaryKey"`
	TeamID      string
	Name        string
	Description string
	Content     string
	Version     int
	UpdatedBy   string
	CreatedAt   string
	UpdatedAt   string
}

// TableName pins the gorm model to the goose-managed table.
func (Skill) TableName() string { return "skills" }

// Repo is the persistence boundary for skills.
type Repo struct {
	db *gorm.DB
}

// NewRepo constructs a Repo over an open gorm connection.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// GetByName fetches a team's skill by its unique-per-team name.
func (r *Repo) GetByName(ctx context.Context, teamID, name string) (Skill, error) {
	var s Skill
	err := r.db.WithContext(ctx).
		Where("team_id = ? AND name = ?", teamID, name).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Skill{}, ErrNotFound
	}
	return s, err
}

// List returns every skill in a team ordered by name (metadata for a future
// list_skills tool — content omitted at the call site that wants a summary).
func (r *Repo) List(ctx context.Context, teamID string) ([]Skill, error) {
	var out []Skill
	err := r.db.WithContext(ctx).
		Where("team_id = ?", teamID).Order("name").Find(&out).Error
	return out, err
}

// Upsert creates or replaces a team's skill by name, bumping the version on
// update. Used by seeding now and by the future update_skill tool.
func (r *Repo) Upsert(ctx context.Context, teamID, name, description, content, updatedBy string) (Skill, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	existing, err := r.GetByName(ctx, teamID, name)
	switch {
	case errors.Is(err, ErrNotFound):
		s := Skill{
			ID: uuid.NewString(), TeamID: teamID, Name: name,
			Description: description, Content: content, Version: 1,
			UpdatedBy: updatedBy, CreatedAt: now, UpdatedAt: now,
		}
		return s, r.db.WithContext(ctx).Create(&s).Error
	case err != nil:
		return Skill{}, err
	default:
		existing.Description = description
		existing.Content = content
		existing.Version++
		existing.UpdatedBy = updatedBy
		existing.UpdatedAt = now
		return existing, r.db.WithContext(ctx).Save(&existing).Error
	}
}

// LoadResult is the payload the load_skill tool returns to the calling agent —
// enough to drop the content straight into a skill slot plus provenance.
type LoadResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     int    `json:"version"`
	Description string `json:"description"`
	Content     string `json:"content"`
	UpdatedBy   string `json:"updated_by"`
	UpdatedAt   string `json:"updated_at"`
}

// Service holds the skill use-cases. It depends on the narrow Reader/Writer
// behaviour it needs, so tests can substitute a fake without a database.
type Service struct {
	repo Store
}

// Store is the persistence behaviour the Service needs. Defining it at the
// consumer (not exporting *Repo) keeps the dependency minimal and mockable.
type Store interface {
	GetByName(ctx context.Context, teamID, name string) (Skill, error)
	Upsert(ctx context.Context, teamID, name, description, content, updatedBy string) (Skill, error)
	List(ctx context.Context, teamID string) ([]Skill, error)
}

// Summary is a skill's metadata without its (potentially large) body — the shape
// list_skills returns, so an agent can see what is available before loading one.
type Summary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     int    `json:"version"`
	UpdatedBy   string `json:"updated_by"`
	UpdatedAt   string `json:"updated_at"`
}

// NewService wires a Service over any Store implementation.
func NewService(store Store) *Service { return &Service{repo: store} }

// Load resolves a team's skill by name into a LoadResult. This is the read path
// behind the load_skill MCP tool: any team member may load. It does no vector
// search — a centralised skill is a direct keyed lookup, fast and exact.
func (s *Service) Load(ctx context.Context, teamID, name string) (LoadResult, error) {
	sk, err := s.repo.GetByName(ctx, teamID, name)
	if err != nil {
		return LoadResult{}, err
	}
	return LoadResult{
		ID: sk.ID, Name: sk.Name, Version: sk.Version,
		Description: sk.Description, Content: sk.Content,
		UpdatedBy: sk.UpdatedBy, UpdatedAt: sk.UpdatedAt,
	}, nil
}

// Update is the phase-2 write path (update_skill): a writer/admin replaces a
// skill's body, bumping its version. Wired now so the role gate and version
// semantics are tested from the start; the MCP tool surfacing it lands later.
func (s *Service) Update(ctx context.Context, t RoleHolder, name, description, content string) (Skill, error) {
	if !t.CanWrite() {
		return Skill{}, ErrForbidden
	}
	// Validate the untrusted payload before it is stored: a trimmed, bounded name
	// and a non-empty, size-bounded body. The name is trimmed so " x " and "x"
	// address the same skill; content is kept verbatim (only length-checked).
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxSkillNameLen {
		return Skill{}, ErrInvalidName
	}
	if strings.TrimSpace(content) == "" || len(content) > maxSkillContentLen {
		return Skill{}, ErrInvalidContent
	}
	// Description is optional (may be empty) but bounded — it is a one-liner, not
	// a second body. Trimmed so trailing whitespace does not consume the budget.
	description = strings.TrimSpace(description)
	if len(description) > maxSkillDescriptionLen {
		return Skill{}, ErrInvalidDescription
	}
	return s.repo.Upsert(ctx, t.Team(), name, description, content, t.User())
}

// List returns a team's skills as metadata summaries (no bodies). Any team member
// may list; it is the discovery path that pairs with load_skill's read path.
func (s *Service) List(ctx context.Context, teamID string) ([]Summary, error) {
	skills, err := s.repo.List(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, len(skills))
	for i, sk := range skills {
		out[i] = Summary{
			Name: sk.Name, Description: sk.Description, Version: sk.Version,
			UpdatedBy: sk.UpdatedBy, UpdatedAt: sk.UpdatedAt,
		}
	}
	return out, nil
}

// RoleHolder is the minimal slice of an authenticated caller the skill context
// needs for authorization, decoupling it from the tenant package's concrete
// type (accept an interface at the consumer).
type RoleHolder interface {
	Team() string
	User() string
	CanWrite() bool
}
