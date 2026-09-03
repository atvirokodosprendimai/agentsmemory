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
	// ⚠ AN ABSENT Host IS REFUSED, AND THE FIRST VERSION LET IT THROUGH. That
	// version read `r.Host != "" && !addresses(...)`, so a request with no Host at
	// all skipped the check entirely — measured 2026-09-03 against the running
	// container, where a raw `POST /mcp` with an empty Host header answered 200
	// while the same request naming evil.example.com answered 403. Nothing
	// browser-driven reaches that state (HTTP/1.1 requires Host and Go answers
	// 400 without one), so this was never the rebinding vector — it is a security
	// check whose one job is to refuse what it cannot vouch for, written to fail
	// open. Every real client sends a Host, so failing closed costs nothing that
	// was working.
	if !addressesThisMachine(r.Host) {
		return "Host " + r.Host
	}
	return ""
}

// addressesThisMachine reports whether an HTTP authority names the local machine
// — "localhost" or any loopback literal — at any port.
//
// The port is deliberately not checked. A caller that reached a loopback address
// reached this process through the operating system's own loopback interface, so
// the port it used adds nothing: rebinding turns on the NAME resolving to
// 127.0.0.1, which is what this rejects. Requiring a specific port would break
// every test server on an ephemeral one, for no gain.
//
// One trailing dot is trimmed because "localhost." is the fully-qualified
// spelling of the same name and resolves identically; refusing it would turn a
// correct URL into a 403 for no security gain, since the label it qualifies is
// still compared literally.
//
// ⚠ THERE IS DELIBERATELY NO SPECIAL CASE FOR THE UNIX-SOCKET CLIENT, AND TWO
// EARLIER VERSIONS HAD ONE. A socket dial has no network host, so the proxy must
// put SOMETHING in the URL; it used to be the literal "unix", which this function
// then had to exempt. The first version forgot to, and refused every socket
// client. The second exempted it only when a socket was being served, which was
// sound but still carried the question of whether a resolvable single-label name
// could reach a TCP listener. The answer was to stop minting the odd authority:
// socketURL now says "localhost", which is true of a Unix socket in every sense
// that matters here and needs no exemption at all. A special case you can delete
// beats a special case you have to reason about.
func addressesThisMachine(authority string) bool {
	host := authority
	if h, _, err := net.SplitHostPort(authority); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	host = strings.TrimSuffix(host, ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
