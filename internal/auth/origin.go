package auth

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// OffMachineAddressing returns the header that proves an inbound request was
// addressed to something other than this machine, or "" when nothing does.
//
// It exists because LOOPBACK IS NOT A BOUNDARY A BROWSER RESPECTS. Local mode
// runs credential-free by design and LocalTenant's own comment names the listen
// address as the only thing between a caller and the whole database — which is
// exactly the assumption DNS rebinding defeats. An attacker page on evil.example
// with a one-second TTL re-resolves that name to 127.0.0.1; the browser then
// treats http://evil.example:8080 as same-origin, sends no preflight, and CORS
// never runs. Measured 2026-09-03 against the shipped container: a POST carrying
// Host and Origin of evil.example.com returned a full cross-wing am_search page
// from every project in the palace. The MCP Streamable HTTP transport makes
// validating Origin a MUST for this reason; ADR-049 records the decision.
//
// Host is checked as well as Origin because the no-Origin variant of the same
// attack exists: a form post or an <img> to a rebound name carries no Origin at
// all, and only the Host header still names where the request was addressed. A
// request that names this machine in both is indistinguishable from a local
// agent, which is the whole population local mode serves.
func OffMachineAddressing(r *http.Request) string {
	// An Origin present at all means a browser sent this. "null" — a sandboxed
	// iframe or a file:// page — is not this machine and must not read as absent.
	if o := strings.TrimSpace(r.Header.Get("Origin")); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host == "" || !addressesThisMachine(u.Host) {
			return "Origin " + o
		}
	}
	if r.Host != "" && !addressesThisMachine(r.Host) {
		return "Host " + r.Host
	}
	return ""
}

// SocketAuthority is the synthetic HTTP host a Unix-socket client sends.
//
// A socket dial has no network host, but net/http still requires a syntactically
// valid absolute URL, so the proxy mints `http://unix/mcp` — see socketURL in
// cmd/server/stdio.go, which TestTheSocketAuthorityIsTheOneTheProxySends pins to
// this constant so a rename there cannot silently start answering 403 here.
const SocketAuthority = "unix"

// addressesThisMachine reports whether an HTTP authority names the local machine
// — "localhost", any loopback literal, or the synthetic Unix-socket host — at any
// port.
//
// The port is deliberately not checked. A caller that reached a loopback address
// reached this process through the operating system's own loopback interface, so
// the port it used adds nothing: rebinding turns on the NAME resolving to
// 127.0.0.1, which is what this rejects. Requiring a specific port would break
// every test server on an ephemeral one, for no gain.
//
// ⚠ THE SOCKET CASE IS WHY THIS FUNCTION HAS A THIRD BRANCH, AND THE FIRST
// VERSION SHIPPED WITHOUT IT. That version's comment claimed checking the port
// "would break a --socket deployment", which named the right deployment and the
// wrong header: the socket client's PORT is absent, and its HOST is the literal
// "unix". Every socket client would have got a 403 — a supported path, broken by
// a guard whose own task file made "the guard refuses a real registered client"
// its Stop Condition. Awareness of a case is not coverage of it.
//
// Accepting it costs nothing a browser can spend. Reaching a Unix socket needs
// filesystem access no page has, and "unix" is a single-label name that resolves
// nowhere public, so no rebind can produce this authority against a TCP listener.
func addressesThisMachine(authority string) bool {
	host := authority
	if h, _, err := net.SplitHostPort(authority); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") || strings.EqualFold(host, SocketAuthority) {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
