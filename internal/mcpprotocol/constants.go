// Package mcpprotocol owns wire constants shared by every agentsmemory MCP
// producer and consumer. It deliberately has no server or client dependencies.
package mcpprotocol

const (
	// ToolPrefix namespaces agentsmemory tools when several MCPs share a client.
	ToolPrefix = "am_"
	// WingHeader binds an MCP registration to its project wing.
	WingHeader = "X-Agentsmemory-Wing"
	// OriginHeader names WHO is calling — `hook:<script>` for the kit's automatic
	// recalls, absent for a person or an agent acting on its own judgement — so a
	// search's row can record it (ADR-054). It rides the same route as the wing
	// and, like it, is set by the kit rather than by anything an agent types.
	OriginHeader = "X-Agentsmemory-Origin"
	// OriginEnvVar is the process-level origin a hook exports before calling
	// `aiagentmemory mcp`; the kit turns it into OriginHeader, and the server's own
	// in-process `mcp` path reads it directly.
	OriginEnvVar = "AGENTSMEMORY_ORIGIN"
	// TokenEnvVar is the workspace bearer every MCP client presents. The server
	// CLI, the stdio proxy, and the installer all read this one name.
	TokenEnvVar = "AGENTSMEMORY_TOKEN"
	// LocalTokenEnvVar is --local's shared bearer. It is deliberately not
	// TokenEnvVar: a developer with a hosted workspace key exported would
	// otherwise find their local server silently demanding it. The installer
	// reads this same variable, so exporting it once configures both halves.
	LocalTokenEnvVar = "AGENTSMEMORY_LOCAL_TOKEN"
	// WingEnvVar is the process-level default wing a registration or stdio
	// proxy forwards when the caller did not pass one on the tool call.
	WingEnvVar = "AGENTSMEMORY_WING"
	// LocalEnvVar is the self-hosted single-workspace switch. The server CLI,
	// doctor, and the installer/extension all read this one name.
	LocalEnvVar = "AGENTSMEMORY_LOCAL"
	// SocketEnvVar is the Unix socket the server listens on and the stdio
	// proxy / installer dial. One name, both halves.
	SocketEnvVar = "AGENTSMEMORY_SOCKET"
	// MCPURLEnvVar is the MCP endpoint a client or bridge talks to.
	MCPURLEnvVar = "AGENTSMEMORY_MCP_URL"
	// ProxyURLEnvVar is the HTTP origin the stdio proxy dials when it is not
	// using a socket. It is not MCPURLEnvVar: the proxy speaks to the server's
	// listen address, which may not be the public MCP URL a remote client uses.
	ProxyURLEnvVar = "AGENTSMEMORY_URL"
	// HostedOrigin is the public production origin (dashboard, canonical URLs,
	// passkeys).
	HostedOrigin = "https://aiagentmemory.dev"
	// HostedMCPURL is the Streamable HTTP MCP endpoint on HostedOrigin. The
	// path is derived so a client cannot point at /mcp on a different site
	// than the one the landing page advertises.
	HostedMCPURL = HostedOrigin + "/mcp"
	// StarScopeSchemaExtension marks an optional wing property whose "*" value
	// deliberately widens a registration-scoped read to every visible wing.
	// Contract-axis adapters discover the class from tools/list through this
	// extension instead of maintaining a second list of handlers.
	StarScopeSchemaExtension = "x-agentsmemory-star-scope"
)

// StarScopeProperty adds the machine-readable star-scope contract to an MCP
// string property's JSON Schema. Its plain function signature is assignable to
// mcp.PropertyOption without making this wire-constant package depend on the
// server or client implementation.
func StarScopeProperty(schema map[string]any) {
	schema[StarScopeSchemaExtension] = true
}
