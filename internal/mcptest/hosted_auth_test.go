package mcptest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// TestHostedMCPAuthenticationAndIsolation crosses the same hosted boundary as
// production: bearer -> OAuth gate -> HTTP MCP -> Bridge -> admission -> role
// guard -> tenant-scoped handler. The ordinary scenario harness intentionally
// substitutes only the first step, so it cannot make any of these claims.
func TestHostedMCPAuthenticationAndIsolation(t *testing.T) {
	hosted := mcptest.NewHosted(t)
	ctx := context.Background()

	adminA, credA, err := hosted.Tenants.SeedTeamWithKey(ctx, "Alpha", "alpha-auth", "alpha-admin@example.test")
	if err != nil {
		t.Fatalf("seed alpha: %v", err)
	}
	adminB, credB, err := hosted.Tenants.SeedTeamWithKey(ctx, "Beta", "beta-auth", "beta-admin@example.test")
	if err != nil {
		t.Fatalf("seed beta: %v", err)
	}
	memberToken := addHostedMember(t, hosted, adminA.TeamID, "alpha-member@example.test", tenant.RoleMember)
	writerToken := addHostedMember(t, hosted, adminA.TeamID, "alpha-writer@example.test", tenant.RoleWriter)
	backupAdminToken := addHostedMember(t, hosted, adminA.TeamID, "alpha-backup-admin@example.test", tenant.RoleAdmin)

	for name, bearer := range map[string]string{"missing": "", "invalid": "not-a-real-token"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, hosted.URL+"/mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s bearer request: %v", name, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s bearer status = %d, want 401", name, resp.StatusCode)
		}
		wantChallenge := `Bearer resource_metadata="` + hosted.URL + `/.well-known/oauth-protected-resource"`
		if challenge := resp.Header.Get("WWW-Authenticate"); challenge != wantChallenge {
			t.Errorf("%s bearer challenge = %q, want %q", name, challenge, wantChallenge)
		}
	}
	for _, teamID := range []string{adminA.TeamID, adminB.TeamID} {
		if snap, err := hosted.Usage.Snapshot(ctx, teamID); err != nil {
			t.Fatalf("usage snapshot: %v", err)
		} else if snap.Used != 0 {
			t.Errorf("unauthenticated traffic was metered to %s: used=%d", teamID, snap.Used)
		}
	}

	// Alpha uses a real OAuth access token; the other clients use raw project
	// tokens. AuthServer.Gate deliberately converges both credential kinds.
	oauthAdmin := hosted.Client(t, "wing_shared", oauthAccessToken(t, hosted.URL, credA))
	admin := hosted.Client(t, "wing_shared", backupAdminToken)
	member := hosted.Client(t, "wing_shared", memberToken)
	writer := hosted.Client(t, "wing_shared", writerToken)
	beta := hosted.Client(t, "wing_shared", credB.Secret)

	definitions, err := admin.ListToolDefinitions(t)
	if err != nil {
		t.Fatalf("list live hosted tools: %v", err)
	}
	var writeTools []string
	for _, tool := range definitions {
		if tool.Annotations.ReadOnlyHint == nil {
			t.Fatalf("live tool %s has no readOnlyHint, so hosted policy cannot classify it", tool.Name)
		}
		if !*tool.Annotations.ReadOnlyHint {
			writeTools = append(writeTools, tool.Name)
		}
	}
	if len(writeTools) == 0 {
		t.Fatal("live hosted catalogue exposed no write tools; the authorization class would pass vacuously")
	}

	// Authentication and write authorization are properties of the whole live
	// write class, not three examples chosen beside the registrar. Discover the
	// class from tools/list so a newly registered write joins this gate without
	// somebody remembering to update a second policy table.
	for _, toolName := range writeTools {
		toolName := toolName
		t.Run("unauthenticated/"+toolName, func(t *testing.T) {
			beforeAlpha := usageUsed(t, hosted, adminA.TeamID)
			beforeBeta := usageUsed(t, hosted, adminB.TeamID)
			if beforeAlpha != 0 || beforeBeta != 0 {
				t.Fatalf("fixture was metered before unauthenticated write probe: alpha=%d beta=%d", beforeAlpha, beforeBeta)
			}

			status, body := unauthenticatedToolCall(t, hosted.URL, toolName)
			if status != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body=%s", status, body)
			}
			if after := usageUsed(t, hosted, adminA.TeamID); after != beforeAlpha {
				t.Errorf("unauthenticated %s was metered to alpha: used %d -> %d", toolName, beforeAlpha, after)
			}
			if after := usageUsed(t, hosted, adminB.TeamID); after != beforeBeta {
				t.Errorf("unauthenticated %s was metered to beta: used %d -> %d", toolName, beforeBeta, after)
			}
		})

		t.Run("member/"+toolName, func(t *testing.T) {
			beforeState, err := hosted.DurableStateDigest(ctx)
			if err != nil {
				t.Fatalf("state before member refusal: %v", err)
			}
			beforeUsage := usageUsed(t, hosted, adminA.TeamID)

			out := member.MustRefuse(t, toolName, map[string]any{})
			if !contains(out, "changes stored memory") || !contains(out, "read-only") {
				t.Errorf("%s was not refused by the production write guard:\n%s", toolName, out)
			}

			afterState, err := hosted.DurableStateDigest(ctx)
			if err != nil {
				t.Fatalf("state after member refusal: %v", err)
			}
			if afterState != beforeState {
				t.Errorf("member-refused %s changed durable hosted state: %x -> %x", toolName, beforeState, afterState)
			}
			if after := usageUsed(t, hosted, adminA.TeamID); after != beforeUsage {
				t.Errorf("member-refused %s reached admission/metering: used %d -> %d", toolName, beforeUsage, after)
			}
		})
	}

	beforeOAuth := usageUsed(t, hosted, adminA.TeamID)
	if got := oauthAdmin.MustCall(t, "am_status", map[string]any{}); !contains(got, `"role":"admin"`) {
		t.Errorf("OAuth admin did not receive its database-backed role:\n%s", got)
	}
	if after := usageUsed(t, hosted, adminA.TeamID); after != beforeOAuth+1 {
		t.Errorf("accepted OAuth call did not cross admission exactly once: used %d -> %d", beforeOAuth, after)
	}
	beforeRaw := usageUsed(t, hosted, adminB.TeamID)
	if got := beta.MustCall(t, "am_status", map[string]any{}); !contains(got, `"role":"admin"`) {
		t.Errorf("raw beta admin did not receive its database-backed role:\n%s", got)
	}
	if after := usageUsed(t, hosted, adminB.TeamID); after != beforeRaw+1 {
		t.Errorf("accepted raw-token call did not cross admission exactly once: used %d -> %d", beforeRaw, after)
	}

	for label, call := range map[string]struct {
		h    *mcptest.Harness
		role string
	}{
		"raw admin":  {admin, "admin"},
		"raw member": {member, "member"},
		"raw writer": {writer, "writer"},
	} {
		if got := call.h.MustCall(t, "am_status", map[string]any{}); !contains(got, `"role":"`+call.role+`"`) {
			t.Errorf("%s did not receive its database-backed role:\n%s", label, got)
		}
	}

	// A sealed access token is not authority frozen at mint time. Demote its
	// user in the database and the SAME token must immediately report member and
	// lose write authority when Gate revalidates its client key.
	if err := hosted.Tenants.SetMemberRole(ctx, adminA.TeamID, adminA.UserID, tenant.RoleMember); err != nil {
		t.Fatalf("demote oauth admin: %v", err)
	}
	if got := oauthAdmin.MustCall(t, "am_status", map[string]any{}); !contains(got, `"role":"member"`) {
		t.Errorf("the OAuth access token kept its minted admin role after database demotion:\n%s", got)
	}
	beforeOAuthRefusal, err := hosted.Usage.Snapshot(ctx, adminA.TeamID)
	if err != nil {
		t.Fatalf("usage before OAuth refusal: %v", err)
	}
	oauthAdmin.MustRefuse(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "STALE-OAUTH-ROLE-WRITE must never be stored",
	})
	afterOAuthRefusal, err := hosted.Usage.Snapshot(ctx, adminA.TeamID)
	if err != nil {
		t.Fatalf("usage after OAuth refusal: %v", err)
	}
	if afterOAuthRefusal.Used != beforeOAuthRefusal.Used {
		t.Errorf("role-refused OAuth write reached admission/metering: used %d -> %d",
			beforeOAuthRefusal.Used, afterOAuthRefusal.Used)
	}

	beforeAcceptedWrite, err := hosted.DurableStateDigest(ctx)
	if err != nil {
		t.Fatalf("state before accepted admin write: %v", err)
	}
	created := admin.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "ALPHA-PRIVATE-MARKER the signing key stays in alpha's vault",
	})
	afterAcceptedWrite, err := hosted.DurableStateDigest(ctx)
	if err != nil {
		t.Fatalf("state after accepted admin write: %v", err)
	}
	if afterAcceptedWrite == beforeAcceptedWrite {
		t.Fatal("durable state digest did not observe an accepted drawer write; refusal checks would be vacuous")
	}
	id := firstDrawerID(t, admin, created)
	if got := member.MustCall(t, "am_search", map[string]any{
		"query": "where does alpha keep the signing key", "limit": 10,
	}); !contains(got, "ALPHA-PRIVATE-MARKER") {
		t.Errorf("a read-only member could not read its own workspace:\n%s", got)
	}

	if got := admin.MustCall(t, "am_list_drawers", map[string]any{"limit": 20}); contains(got, "STALE-OAUTH-ROLE-WRITE") {
		t.Errorf("the demoted OAuth token's refused write changed state anyway:\n%s", got)
	}

	writer.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "WRITER-MARKER a writer may file shared memory",
	})
	if got := admin.MustCall(t, "am_list_drawers", map[string]any{"limit": 20}); !contains(got, "WRITER-MARKER") {
		t.Errorf("the writer's accepted write did not reach its workspace:\n%s", got)
	}

	for tool, args := range map[string]map[string]any{
		"am_get_drawer":        {"id": id},
		"am_invalidate_drawer": {"id": id, "reason": "a cross-tenant retraction must not land"},
	} {
		beta.MustRefuse(t, tool, args)
	}
	if got := beta.MustCall(t, "am_list_drawers", map[string]any{"wing": "*", "limit": 50}); contains(got, "ALPHA-PRIVATE-MARKER") || contains(got, "WRITER-MARKER") {
		t.Errorf("beta enumerated alpha's opaque workspace data:\n%s", got)
	}
	if got := beta.MustCall(t, "am_search", map[string]any{
		"query": "alpha signing key vault", "wing": "*", "limit": 20,
	}); contains(got, "ALPHA-PRIVATE-MARKER") {
		t.Errorf("beta recalled alpha's data through the vector path:\n%s", got)
	}
	if got := admin.MustCall(t, "am_get_drawer", map[string]any{"id": id}); !contains(got, "ALPHA-PRIVATE-MARKER") {
		t.Errorf("beta's cross-tenant retraction attempt affected the owner:\n%s", got)
	}

	beta.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "BETA-PRIVATE-MARKER must stay out of alpha",
	})
	if got := admin.MustCall(t, "am_list_drawers", map[string]any{"wing": "*", "limit": 50}); contains(got, "BETA-PRIVATE-MARKER") {
		t.Errorf("alpha enumerated beta's memory despite sharing the same wing name:\n%s", got)
	}
}

func unauthenticatedToolCall(t *testing.T, baseURL, toolName string) (int, string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      toolName,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("encode unauthenticated %s call: %v", toolName, err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build unauthenticated %s call: %v", toolName, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send unauthenticated %s call: %v", toolName, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read unauthenticated %s response: %v", toolName, err)
	}
	return resp.StatusCode, string(body)
}

func addHostedMember(t *testing.T, hosted *mcptest.Hosted, teamID, email string, role tenant.Role) string {
	t.Helper()
	ctx := context.Background()
	user, err := hosted.Tenants.CreateUserWithPassword(ctx, email, "mcptest-password", email)
	if err != nil {
		t.Fatalf("create %s: %v", role, err)
	}
	if _, err := hosted.Tenants.AddMemberByEmail(ctx, teamID, email, role); err != nil {
		t.Fatalf("add %s: %v", role, err)
	}
	token, err := hosted.Tenants.RevealToken(ctx, teamID, user.ID)
	if err != nil {
		t.Fatalf("reveal %s token: %v", role, err)
	}
	return token
}

func usageUsed(t *testing.T, hosted *mcptest.Hosted, teamID string) int {
	t.Helper()
	snapshot, err := hosted.Usage.Snapshot(context.Background(), teamID)
	if err != nil {
		t.Fatalf("usage snapshot: %v", err)
	}
	return snapshot.Used
}

func oauthAccessToken(t *testing.T, baseURL string, cred tenant.Credential) string {
	t.Helper()
	resourceResp, err := http.Get(baseURL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("oauth protected-resource discovery: %v", err)
	}
	var resource struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.NewDecoder(resourceResp.Body).Decode(&resource); err != nil {
		_ = resourceResp.Body.Close()
		t.Fatalf("decode protected-resource metadata: %v", err)
	}
	_ = resourceResp.Body.Close()
	if resourceResp.StatusCode != http.StatusOK || resource.Resource != baseURL+"/mcp" ||
		len(resource.AuthorizationServers) != 1 || resource.AuthorizationServers[0] != baseURL {
		t.Fatalf("unusable protected-resource metadata: status=%d payload=%+v", resourceResp.StatusCode, resource)
	}

	issuer := resource.AuthorizationServers[0]
	authResp, err := http.Get(issuer + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatalf("oauth authorization-server discovery: %v", err)
	}
	var metadata struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(authResp.Body).Decode(&metadata); err != nil {
		_ = authResp.Body.Close()
		t.Fatalf("decode authorization-server metadata: %v", err)
	}
	_ = authResp.Body.Close()
	if authResp.StatusCode != http.StatusOK || metadata.AuthorizationEndpoint != baseURL+"/authorize" ||
		metadata.TokenEndpoint != baseURL+"/token" {
		t.Fatalf("unusable authorization-server metadata: status=%d payload=%+v", authResp.StatusCode, metadata)
	}

	verifier := strings.Repeat("v", 48)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	redirect := "https://client.example.test/callback"
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {cred.ClientKey},
		"redirect_uri":          {redirect},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(metadata.AuthorizationEndpoint + "?" + q.Encode())
	if err != nil {
		t.Fatalf("oauth authorize: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("oauth authorize status=%d", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("oauth redirect: %v", err)
	}
	code := location.Query().Get("code")
	if code == "" {
		t.Fatal("oauth authorize returned no code")
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"code_verifier": {verifier},
		"client_id":     {cred.ClientKey},
		"client_secret": {cred.Secret},
	}
	tokenResp, err := http.PostForm(metadata.TokenEndpoint, form)
	if err != nil {
		t.Fatalf("oauth token: %v", err)
	}
	defer tokenResp.Body.Close()
	if tokenResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(tokenResp.Body)
		t.Fatalf("oauth token status=%d body=%s", tokenResp.StatusCode, body)
	}
	var payload struct {
		Access string `json:"access_token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode oauth token: %v", err)
	}
	if payload.Access == "" {
		t.Fatal("oauth token response has no access token")
	}
	return payload.Access
}
