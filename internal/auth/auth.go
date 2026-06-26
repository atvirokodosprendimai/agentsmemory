// Package auth is the boundary that turns an inbound HTTP credential into a
// resolved tenant on the request context. It is the bridge between the network
// edge (a Bearer token on the MCP HTTP connection) and the rest of the system,
// which only ever sees a *tenant.Tenant — never a raw token.
//
// Phase 1 is bearer tokens (per-agent API keys). The seam is deliberately a
// single context-injection function so OAuth 2.1 can be added later behind the
// same boundary without touching any tool handler.
package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// ctxKey is an unexported context key type so no other package can collide with
// or overwrite the stored tenant.
type ctxKey struct{}

// tenantKey is the single context key under which the resolved tenant is stored.
var tenantKey = ctxKey{}

// Resolver resolves a plaintext bearer token to a tenant. *tenant.Repo
// satisfies it; defining the interface here (consumer side) keeps auth
// decoupled from gorm.
type Resolver interface {
	ResolveToken(ctx context.Context, plaintext string) (tenant.Tenant, error)
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header, returning "" when absent or malformed.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// HTTPContextFunc returns a function matching mark3labs/mcp-go's HTTPContextFunc
// signature. It runs once per MCP HTTP request: it reads the bearer token,
// resolves the tenant, and stashes it on the context the tool handlers receive.
//
// A failed or missing token is NOT rejected here — it simply leaves no tenant
// on the context, and each tool calls TenantFrom, which fails closed. Centralis-
// ing the rejection in the tools keeps the transport layer dumb and uniform.
func HTTPContextFunc(res Resolver) func(ctx context.Context, r *http.Request) context.Context {
	return func(ctx context.Context, r *http.Request) context.Context {
		token := bearerToken(r)
		if token == "" {
			return ctx
		}
		t, err := res.ResolveToken(ctx, token)
		if err != nil {
			return ctx // unresolved — tools will fail closed
		}
		return context.WithValue(ctx, tenantKey, t)
	}
}

// TenantFrom returns the resolved tenant on the context. ok is false when the
// request was unauthenticated or the token did not resolve, so every tool can
// fail closed with a single check.
func TenantFrom(ctx context.Context) (tenant.Tenant, bool) {
	t, ok := ctx.Value(tenantKey).(tenant.Tenant)
	return t, ok
}

// WithTenant returns a context carrying the given tenant. Exists for tests and
// for any in-process caller that has already authenticated out of band.
func WithTenant(ctx context.Context, t tenant.Tenant) context.Context {
	return context.WithValue(ctx, tenantKey, t)
}

// Bridge is an mcp-go HTTPContextFunc that carries a tenant already resolved by
// an upstream middleware (the OAuth gate) on the HTTP request into the context
// the MCP tools receive. When the gate fronts /mcp, resolution happens once
// there; this just forwards the result so the tools can read it via TenantFrom.
func Bridge(ctx context.Context, r *http.Request) context.Context {
	if t, ok := TenantFrom(r.Context()); ok {
		return WithTenant(ctx, t)
	}
	return ctx
}
