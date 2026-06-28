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
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
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

// ErrNotMember is returned by MembershipRole when a user has no membership in a
// team. The dashboard treats it as "this project is not yours" — a project-scoped
// page must never render for a team the signed-in user does not belong to.
var ErrNotMember = errors.New("tenant: user is not a member of this team")

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
	ID                string `gorm:"primaryKey"`
	Code              string `gorm:"uniqueIndex"`
	Kind              string // personal | enterprise
	Name              string
	PriceCents        int
	Currency          string
	MonthlyRequestCap int // metered MCP requests allowed per calendar month
	CreatedAt         string
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
// ClientKey is the public OAuth client_id ("mck_<hex>"); the plaintext token is
// the OAuth client_secret. Direct callers send the token as a Bearer; OAuth
// clients exchange (client_key, token) for a sealed Bearer.
type APIKey struct {
	ID         string `gorm:"primaryKey"`
	TeamID     string
	UserID     string
	Name       string
	Prefix     string
	ClientKey  string
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

// GenerateClientKey mints a public OAuth client_id of the form "mck_<24 hex>".
// It is NOT a secret — it appears in the /authorize URL — so it only needs to be
// unguessable enough to avoid accidental collisions; the token is the secret.
func GenerateClientKey() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "mck_" + hex.EncodeToString(buf), nil
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
	r.touchKey(ctx, key.ID)
	return r.tenantFromKey(ctx, key), nil
}

// tenantFromKey resolves the caller's role on the key's team (defaulting to
// member if the membership row is missing — the key already proves team scope).
func (r *Repo) tenantFromKey(ctx context.Context, key APIKey) Tenant {
	role := RoleMember
	var m Membership
	if err := r.db.WithContext(ctx).
		Where("team_id = ? AND user_id = ?", key.TeamID, key.UserID).
		First(&m).Error; err == nil && m.Role != "" {
		role = Role(m.Role)
	}
	return Tenant{TeamID: key.TeamID, UserID: key.UserID, Role: role}
}

// MembershipRole returns the signed-in user's role in a team, or ErrNotMember if
// no membership row ties them to it. The dashboard calls this to authorize every
// project-scoped action: the team id arrives from the URL (untrusted), so access
// is granted only when a membership exists — never inferred from the id alone.
// An empty stored role is normalized to the least-privileged RoleMember.
func (r *Repo) MembershipRole(ctx context.Context, userID, teamID string) (Role, error) {
	var m Membership
	err := r.db.WithContext(ctx).
		Where("team_id = ? AND user_id = ?", teamID, userID).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrNotMember
	}
	if err != nil {
		return "", err
	}
	if m.Role == "" {
		return RoleMember, nil
	}
	return Role(m.Role), nil
}

// touchKey best-effort stamps last_used_at; a failed update must not deny access.
func (r *Repo) touchKey(ctx context.Context, id string) {
	now := time.Now().UTC().Format(time.RFC3339)
	_ = r.db.WithContext(ctx).Model(&APIKey{}).Where("id = ?", id).Update("last_used_at", now).Error
}

// ClientByKey resolves an OAuth client_id (client_key) to its tenant WITHOUT
// checking the secret. Used at /authorize, where only the public client_id is
// present; the secret is verified later at /token. Rejects unknown/revoked keys.
func (r *Repo) ClientByKey(ctx context.Context, clientKey string) (Tenant, error) {
	if clientKey == "" {
		return Tenant{}, ErrInvalidToken
	}
	var key APIKey
	err := r.db.WithContext(ctx).
		Where("client_key = ? AND revoked_at IS NULL", clientKey).First(&key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Tenant{}, ErrInvalidToken
	}
	if err != nil {
		return Tenant{}, err
	}
	return r.tenantFromKey(ctx, key), nil
}

// ValidateClient verifies an OAuth (client_id, client_secret) pair at /token and
// returns the tenant. The secret is compared by hash; unknown client, revoked
// key, or wrong secret all yield ErrInvalidToken (opaque, non-enumerable).
func (r *Repo) ValidateClient(ctx context.Context, clientKey, secret string) (Tenant, error) {
	if clientKey == "" || secret == "" {
		return Tenant{}, ErrInvalidToken
	}
	var key APIKey
	err := r.db.WithContext(ctx).
		Where("client_key = ? AND revoked_at IS NULL", clientKey).First(&key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Tenant{}, ErrInvalidToken
	}
	if err != nil {
		return Tenant{}, err
	}
	if subtle.ConstantTimeCompare([]byte(key.TokenHash), []byte(HashToken(secret))) != 1 {
		return Tenant{}, ErrInvalidToken
	}
	r.touchKey(ctx, key.ID)
	return r.tenantFromKey(ctx, key), nil
}

// SeedTeamWithKey creates a team, an owner user (admin), and one API key in a
// single transaction, returning the tenant and the one-time plaintext token.
// It exists so a fresh skeleton is runnable end-to-end (and tests have a
// fixture) without a dashboard yet.
func (r *Repo) SeedTeamWithKey(ctx context.Context, teamName, slug, email string) (Tenant, Credential, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	user := User{ID: uuid.NewString(), Email: email, DisplayName: email, CreatedAt: now}
	if err := r.db.WithContext(ctx).Create(&user).Error; err != nil {
		return Tenant{}, Credential{}, err
	}
	// Delegate workspace + membership + key creation to the shared path, so the
	// "personal workspace on the personal plan" flow is identical to any other
	// workspace a user later creates.
	return r.CreateWorkspaceForUser(ctx, user.ID, teamName, slug, "personal", "plan_personal")
}

// Credential is the one-time secret material returned when an API key is minted:
// the public OAuth client_id and the plaintext token (the Bearer / client_secret).
// Both are shown to the user exactly once.
type Credential struct {
	ClientKey string
	Secret    string
}

// newAPIKey builds an APIKey row plus its one-time Credential (token + client_key).
func newAPIKey(teamID, userID, name, now string) (APIKey, Credential, error) {
	plaintext, prefix, err := GenerateToken()
	if err != nil {
		return APIKey{}, Credential{}, err
	}
	clientKey, err := GenerateClientKey()
	if err != nil {
		return APIKey{}, Credential{}, err
	}
	key := APIKey{
		ID: uuid.NewString(), TeamID: teamID, UserID: userID, Name: name,
		Prefix: prefix, ClientKey: clientKey, TokenHash: HashToken(plaintext), CreatedAt: now,
	}
	return key, Credential{ClientKey: clientKey, Secret: plaintext}, nil
}

// CreateWorkspaceForUser provisions an additional workspace (team) owned by an
// existing user on a given plan, with a fresh API key. This is the path behind
// "one user, several workspaces across plans": a user can run a couple of
// personal workspaces and one or more enterprise ones, each its own isolated
// tenant (separate Qdrant collection) priced by its plan. Returns the tenant
// and the one-time credential.
func (r *Repo) CreateWorkspaceForUser(ctx context.Context, userID, name, slug, kind, planID string) (Tenant, Credential, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var pid *string
	if planID != "" {
		pid = &planID
	}
	team := Team{ID: uuid.NewString(), Name: name, Slug: slug, Kind: kind, PlanID: pid, CreatedAt: now}
	key, cred, err := newAPIKey(team.ID, userID, "default", now)
	if err != nil {
		return Tenant{}, Credential{}, err
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
		return tx.Create(&key).Error
	})
	if err != nil {
		return Tenant{}, Credential{}, err
	}
	return Tenant{TeamID: team.ID, UserID: userID, Role: RoleAdmin}, cred, nil
}

// CreateAPIKey mints an additional credential for a user within a workspace they
// belong to. A user may hold many keys per workspace — e.g. one per agent or CI
// job — each independently revocable.
func (r *Repo) CreateAPIKey(ctx context.Context, teamID, userID, name string) (Credential, error) {
	key, cred, err := newAPIKey(teamID, userID, name, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return Credential{}, err
	}
	if err := r.db.WithContext(ctx).Create(&key).Error; err != nil {
		return Credential{}, err
	}
	return cred, nil
}

// --- web auth (local user + password; goth OAuth providers added later) ---

// ErrEmailTaken is returned when registering an email that already exists.
var ErrEmailTaken = errors.New("tenant: email already registered")

// ErrInvalidCredentials is returned for a bad email/password pair. It is
// deliberately the same for "no such user" and "wrong password" so the login
// form cannot be used to enumerate registered emails.
var ErrInvalidCredentials = errors.New("tenant: invalid email or password")

// normalizeEmail lower-cases and trims an email so lookups are case-insensitive.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// CreateUserWithPassword registers a new human account with a bcrypt-hashed
// password. The cost is bcrypt's default; the plaintext is never stored.
func (r *Repo) CreateUserWithPassword(ctx context.Context, email, password, displayName string) (User, error) {
	email = normalizeEmail(email)
	var existing int64
	if err := r.db.WithContext(ctx).Model(&User{}).Where("email = ?", email).Count(&existing).Error; err != nil {
		return User{}, err
	}
	if existing > 0 {
		return User{}, ErrEmailTaken
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	if displayName == "" {
		displayName = email
	}
	u := User{
		ID: uuid.NewString(), Email: email, PasswordHash: string(hash),
		DisplayName: displayName, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return u, r.db.WithContext(ctx).Create(&u).Error
}

// Authenticate verifies an email/password pair and returns the user. The bcrypt
// comparison runs even on a missing user (against a throwaway hash) to keep the
// timing similar and avoid leaking which emails exist.
func (r *Repo) Authenticate(ctx context.Context, email, password string) (User, error) {
	var u User
	err := r.db.WithContext(ctx).Where("email = ?", normalizeEmail(email)).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || u.PasswordHash == "" {
		// Spend a comparison anyway so response time doesn't reveal the miss.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinv"), []byte(password))
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

// GetUserByID loads a user by id (used to rehydrate the session each request).
func (r *Repo) GetUserByID(ctx context.Context, id string) (User, error) {
	var u User
	return u, r.db.WithContext(ctx).Where("id = ?", id).First(&u).Error
}

// UpsertOAuthUser finds or creates a user by email for a social (goth) login.
// OAuth users have no password hash; they authenticate only via their provider.
func (r *Repo) UpsertOAuthUser(ctx context.Context, email, displayName string) (User, error) {
	email = normalizeEmail(email)
	var u User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&u).Error
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return User{}, err
	}
	if displayName == "" {
		displayName = email
	}
	u = User{
		ID: uuid.NewString(), Email: email, PasswordHash: "",
		DisplayName: displayName, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return u, r.db.WithContext(ctx).Create(&u).Error
}

// ListWorkspacesForUser returns every workspace (team) the user belongs to,
// newest first, so the dashboard can render their projects.
func (r *Repo) ListWorkspacesForUser(ctx context.Context, userID string) ([]Team, error) {
	var teamIDs []string
	if err := r.db.WithContext(ctx).Model(&Membership{}).
		Where("user_id = ?", userID).Pluck("team_id", &teamIDs).Error; err != nil {
		return nil, err
	}
	if len(teamIDs) == 0 {
		return nil, nil
	}
	var teams []Team
	err := r.db.WithContext(ctx).Where("id IN ?", teamIDs).
		Order("created_at DESC").Find(&teams).Error
	return teams, err
}

// PlanForTeam resolves the plan a workspace is subscribed to (e.g. to read its
// monthly request cap). A workspace with no plan attached yields ErrNoPlan.
func (r *Repo) PlanForTeam(ctx context.Context, teamID string) (Plan, error) {
	var team Team
	if err := r.db.WithContext(ctx).Where("id = ?", teamID).First(&team).Error; err != nil {
		return Plan{}, err
	}
	if team.PlanID == nil {
		return Plan{}, ErrNoPlan
	}
	var plan Plan
	return plan, r.db.WithContext(ctx).Where("id = ?", *team.PlanID).First(&plan).Error
}

// ErrNoPlan is returned when a workspace has no plan attached.
var ErrNoPlan = errors.New("tenant: workspace has no plan")

// MonthlyCap returns the workspace's monthly request cap (0 = no plan / treat as
// unlimited by the caller). It satisfies the usage package's CapLookup so the
// metering layer can read the cap without importing tenant's models.
func (r *Repo) MonthlyCap(ctx context.Context, teamID string) (int, error) {
	plan, err := r.PlanForTeam(ctx, teamID)
	if errors.Is(err, ErrNoPlan) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return plan.MonthlyRequestCap, nil
}
