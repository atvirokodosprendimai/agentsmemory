// Package config loads the agentsmemory server configuration from CLI flags and
// environment variables. It is intentionally tiny: the SaaS state lives in the
// database, so config only carries process-level wiring (listen address, the
// SQLite path, and the Qdrant/Ollama endpoints decided as day-one defaults).
package config

import "time"

// Config holds the resolved runtime settings for the MCP server process.
//
// Defaults mirror the Python tool's conventions (Ollama bge-m3 at :11434,
// Qdrant at :6333) so a local dev box works with zero flags, while production
// overrides everything via flags or env.
type Config struct {
	// Addr is the host:port the HTTP/MCP server listens on.
	Addr string

	// DBPath is the SQLite database file (the relational source of truth).
	DBPath string

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
}

// Default returns a Config populated with the day-one development defaults.
// Flag and env resolution in cmd/server overlays user-supplied values on top.
func Default() Config {
	return Config{
		Addr:             ":8080",
		DBPath:           "agentsmemory.db",
		QdrantURL:        "http://localhost:6333",
		OllamaURL:        "http://localhost:11434",
		OllamaEmbedModel: "bge-m3",
		HTTPTimeout:      30 * time.Second,
	}
}
