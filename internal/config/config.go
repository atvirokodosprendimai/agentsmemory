// Package config loads the agentsmemory server configuration from CLI flags and
// environment variables. It is intentionally tiny: the SaaS state lives in the
// database, so config only carries process-level wiring (listen address, the
// SQLite path, and the Qdrant/Ollama endpoints decided as day-one defaults).
package config

import (
	"strings"
	"time"
)

// Vector backend selection. SQLite is always the durable source of truth; this
// chooses what answers searches (decision 2026-06-26: "sqlite as source of
// truth", "qdrant for search").
const (
	// VectorBackendSQLite makes the SQLite source of truth also serve
	// brute-force search — zero external services, ideal for a dev box.
	VectorBackendSQLite = "sqlite"
	// VectorBackendQdrant keeps SQLite as the source of truth and adds Qdrant
	// as the search index (the production path).
	VectorBackendQdrant = "qdrant"
)

// Config holds the resolved runtime settings for the MCP server process.
//
// Defaults mirror the Python tool's conventions (Ollama bge-m3 at :11434,
// Qdrant at :6333) so a local dev box works with zero flags, while production
// overrides everything via flags or env.
type Config struct {
	// Addr is the host:port the HTTP/MCP server listens on.
	Addr string

	// DBPath is the SQLite database file (the relational and vector source of
	// truth).
	DBPath string

	// VectorBackend selects the search index: VectorBackendSQLite (the source of
	// truth serves search too) or VectorBackendQdrant (SQLite source of truth +
	// Qdrant index). SQLite is written either way.
	VectorBackend string

	// QdrantURL is the base URL of the Qdrant vector store (no trailing slash).
	QdrantURL string

	// QdrantAPIKey is an optional Qdrant API key; empty for unauthenticated dev.
	QdrantAPIKey string

	// OllamaURL is the base URL of the Ollama server used for embeddings.
	OllamaURL string

	// OllamaEmbedModel is the embedding model name; bge-m3 yields 1024-dim
	// vectors, matching the frozen Python palace so data stays comparable.
	OllamaEmbedModel string

	// HTTPTimeout bounds outbound calls to Qdrant and Ollama.
	HTTPTimeout time.Duration

	// Debug turns on verbose logging: per-request HTTP access logs (chi) and
	// gorm SQL logging. Off by default so production stays quiet; set APP_DEBUG=true
	// (or --debug) to see traffic and queries during development.
	Debug bool

	// SuperAdminEmails is the platform-superadmin allowlist: users whose email is
	// in this set may edit the GLOBAL skillset (the am_skillset wakeup playbook)
	// that every tenant shares. It is a deploy-time decision carried as process
	// config (env SUPERADMIN_EMAILS, comma-separated), NOT a database row or a
	// per-team role — mirroring how the sibling forumchat project gates its
	// god-mode surface. Empty means no superadmin: the global skillset can be
	// seeded on a fresh database but not edited from the dashboard.
	SuperAdminEmails []string
}

// ParseSuperAdminEmails splits a comma-separated SUPERADMIN_EMAILS value into a
// normalized allowlist: each address lower-cased and trimmed, blanks dropped. It
// is the single normalization point so the stored allowlist and the email a
// session is checked against are compared on the same footing.
func ParseSuperAdminEmails(raw string) []string {
	var out []string
	for e := range strings.SplitSeq(raw, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// Default returns a Config populated with the day-one development defaults.
// Flag and env resolution in cmd/server overlays user-supplied values on top.
func Default() Config {
	return Config{
		Addr:             ":8080",
		DBPath:           "agentsmemory.db",
		VectorBackend:    VectorBackendSQLite,
		QdrantURL:        "http://localhost:6333",
		OllamaURL:        "http://localhost:11434",
		OllamaEmbedModel: "bge-m3",
		HTTPTimeout:      30 * time.Second,
		Debug:            false,
	}
}
