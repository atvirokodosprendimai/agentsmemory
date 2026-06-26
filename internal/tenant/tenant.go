// Package tenant is the multi-tenancy bounded context: teams, users,
// memberships and the API keys that agents present to the remote MCP server.
//
// It exists because the Python mempalace had no notion of identity at all — a
// single local palace, no auth. The SaaS rewrite makes the *team* the unit of
// tenancy: every memory and skill is owned by a team, and an inbound MCP
// request is resolved to exactly one team via its bearer token before any tool
// runs. This package owns that resolution and nothing about memory itself.
package tenant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Role enumerates a member's authority within a team. Writes to shared
// artifacts (e.g. updating a centralised skill) require writer or admin.
type Role string

const (
	RoleMember Role = "member"
	RoleWriter Role = "writer"
	RoleAdmin  Role = "admin"
)

// ErrInvalidToken is returned when a bearer token matches no active API key.
// It is deliberately opaque so callers cannot distinguish "unknown" from
// "revoked" — both are simply unauthorized.
var ErrInvalidToken = errors.New("tenant: invalid or revoked token")

// Tenant is the resolved identity of an authenticated MCP session. It is the
// value injected into the request context; every tool scopes its work to
// Tenant.TeamID. It is a plain value (no behaviour) so it travels cheaply.
type Tenant struct {
	TeamID string
	UserID string
	Role   Role
}

// --- gorm models (table names pinned to the goose migration) ---

// Team is the tenancy root — a workspace. One team maps to one Qdrant
// collection (collection-per-tenant isolation) and to one plan (its price tier).
// Kind distinguishes a single-user personal workspace from a shared enterprise
// one; a single human may own several teams across kinds/plans and mint a
// separate API key in each.
type Team struct {
	ID        string `gorm:"primaryKey"`
	Name      string
	Slug      string `gorm:"uniqueIndex"`
	Kind      string  // personal | enterprise
	PlanID    *string // FK to plans.id; nil until a plan is attached
	CreatedAt string
}

// TableName pins the gorm model to the goose-managed table.
func (Team) TableName() string { return "teams" }

// Plan is a purchasable price tier a workspace subscribes to. The catalog is
// seeded by migration; the app reads it to attach a plan to a workspace.
type Plan struct {
	ID         string `gorm:"primaryKey"`
	Code       string `gorm:"uniqueIndex"`
	Kind       string // personal | enterprise
	Name       string
	PriceCents int
	Currency   string
	CreatedAt  string
}

// TableName pins the gorm model to the goose-managed table.
func (Plan) TableName() string { return "plans" }

// User is a human account that manages a team's keys via the dashboard.
type User struct {
	ID           string `gorm:"primaryKey"`
	Email        string `gorm:"uniqueIndex"`
	PasswordHash string
	DisplayName  string
	CreatedAt    string
}

// TableName pins the gorm model to the goose-managed table.
func (User) TableName() string { return "users" }

// Membership ties a user to a team with a role.
type Membership struct {
	ID        string `gorm:"primaryKey"`
	TeamID    string
	UserID    string
	Role      string
	CreatedAt string
}

// TableName pins the gorm model to the goose-managed table.
func (Membership) TableName() string { return "memberships" }

// APIKey is the bearer credential an agent presents. Only the hash is stored.
type APIKey struct {
	ID         string `gorm:"primaryKey"`
	TeamID     string
	UserID     string
	Name       string
	Prefix     string
	TokenHash  string `gorm:"uniqueIndex"`
	CreatedAt  string
	LastUsedAt *string
	RevokedAt  *string
}

// TableName pins the gorm model to the goose-managed table.
func (APIKey) TableName() string { return "api_keys" }

// HashToken returns the hex SHA-256 of a plaintext token. The same one-way hash
// is used at mint time (to store) and at resolve time (to look up), so the
// plaintext never touches the database.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// GenerateToken mints a new random bearer token and returns the plaintext
// (shown to the user exactly once) alongside its non-secret prefix. 32 random
// bytes hex-encoded is 64 chars of 256-bit entropy.
func GenerateToken() (plaintext, prefix string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	plaintext = hex.EncodeToString(buf)
	return plaintext, plaintext[:8], nil
}

// Repo is the persistence boundary for the tenant context. It is a struct over
// a *gorm.DB; consumers depend on the methods they need, not on gorm directly.
type Repo struct {
	db *gorm.DB
}

// NewRepo constructs a Repo over an open gorm connection.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// ResolveToken maps a plaintext bearer token to its Tenant. It rejects revoked
// keys and best-effort stamps last_used_at. This is the single choke point that
// turns an opaque HTTP credential into a team scope — every MCP call flows
// through it, so isolation is enforced in exactly one place.
func (r *Repo) ResolveToken(ctx context.Context, plaintext string) (Tenant, error) {
	if plaintext == "" {
		return Tenant{}, ErrInvalidToken
	}
	var key APIKey
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND revoked_at IS NULL", HashToken(plaintext)).
		First(&key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Tenant{}, ErrInvalidToken
	}
	if err != nil {
		return Tenant{}, err
	}

	// Look up the caller's role on the owning team (defaults to member if the
	// membership row is somehow missing — the key already proves team scope).
	role := RoleMember
	var m Membership
	if err := r.db.WithContext(ctx).
		Where("team_id = ? AND user_id = ?", key.TeamID, key.UserID).
		First(&m).Error; err == nil && m.Role != "" {
		role = Role(m.Role)
	}

	// Best-effort touch; a failed timestamp update must not deny the request.
	now := time.Now().UTC().Format(time.RFC3339)
	_ = r.db.WithContext(ctx).Model(&APIKey{}).
		Where("id = ?", key.ID).Update("last_used_at", now).Error

	return Tenant{TeamID: key.TeamID, UserID: key.UserID, Role: role}, nil
}

// SeedTeamWithKey creates a team, an owner user (admin), and one API key in a
// single transaction, returning the tenant and the one-time plaintext token.
// It exists so a fresh skeleton is runnable end-to-end (and tests have a
// fixture) without a dashboard yet.
func (r *Repo) SeedTeamWithKey(ctx context.Context, teamName, slug, email string) (Tenant, string, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	user := User{ID: uuid.NewString(), Email: email, DisplayName: email, CreatedAt: now}
	if err := r.db.WithContext(ctx).Create(&user).Error; err != nil {
		return Tenant{}, "", err
	}
	// Delegate workspace + membership + key creation to the shared path, so the
	// "personal workspace on the personal plan" flow is identical to any other
	// workspace a user later creates.
	return r.CreateWorkspaceForUser(ctx, user.ID, teamName, slug, "personal", "plan_personal")
}

// CreateWorkspaceForUser provisions an additional workspace (team) owned by an
// existing user on a given plan, with a fresh API key. This is the path behind
// "one user, several workspaces across plans": a user can run a couple of
// personal workspaces and one or more enterprise ones, each its own isolated
// tenant (separate Qdrant collection) priced by its plan. Returns the tenant
// and the one-time plaintext token.
func (r *Repo) CreateWorkspaceForUser(ctx context.Context, userID, name, slug, kind, planID string) (Tenant, string, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var pid *string
	if planID != "" {
		pid = &planID
	}
	team := Team{ID: uuid.NewString(), Name: name, Slug: slug, Kind: kind, PlanID: pid, CreatedAt: now}
	plaintext, prefix, err := GenerateToken()
	if err != nil {
		return Tenant{}, "", err
	}
	// The workspace creator is its admin (can manage keys and shared skills).
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&team).Error; err != nil {
			return err
		}
		if err := tx.Create(&Membership{
			ID: uuid.NewString(), TeamID: team.ID, UserID: userID,
			Role: string(RoleAdmin), CreatedAt: now,
		}).Error; err != nil {
			return err
		}
		return tx.Create(&APIKey{
			ID: uuid.NewString(), TeamID: team.ID, UserID: userID,
			Name: "default", Prefix: prefix, TokenHash: HashToken(plaintext), CreatedAt: now,
		}).Error
	})
	if err != nil {
		return Tenant{}, "", err
	}
	return Tenant{TeamID: team.ID, UserID: userID, Role: RoleAdmin}, plaintext, nil
}

// CreateAPIKey mints an additional bearer token for a user within a workspace
// they belong to, returning the one-time plaintext. A user may hold many keys
// per workspace — e.g. one per agent or CI job — each independently revocable.
func (r *Repo) CreateAPIKey(ctx context.Context, teamID, userID, name string) (string, error) {
	plaintext, prefix, err := GenerateToken()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	key := APIKey{
		ID: uuid.NewString(), TeamID: teamID, UserID: userID, Name: name,
		Prefix: prefix, TokenHash: HashToken(plaintext), CreatedAt: now,
	}
	if err := r.db.WithContext(ctx).Create(&key).Error; err != nil {
		return "", err
	}
	return plaintext, nil
}
