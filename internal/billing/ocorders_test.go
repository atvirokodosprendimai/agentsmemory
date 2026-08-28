package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// ocFixtureServer serves a recorded response, so the decoder is exercised against
// the shapes the real API produces without touching the network. The auth header
// and the request body are asserted by TestOCOrderSourceSendsPersonalTokenHeader,
// which needs its own handler to capture them.
func ocFixtureServer(t *testing.T, fixture string, status int) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile("testdata/" + fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestOCQueryUsesOnlyPublishedFields keeps the order query off fields Open
// Collective does not publish in its schema.
//
// `Order.legacyId` and `Tier.legacyId` both RESOLVE and both are ABSENT from
// introspection, on production and staging alike (checked 2026-08-28). A field the
// schema does not publish carries no contract: it can be withdrawn without a
// deprecation cycle, and nothing would warn us. The first draft of this client keyed
// the durable subscription id on `Order.legacyId`, which would have worked right up
// until it didn't — and by then there would have been rows referring to it.
//
// The check is a substring scan of the query constant rather than a live
// introspection call, deliberately: it must run hermetically in CI, and the risk
// being guarded is someone reintroducing the convenient hidden field, not the schema
// changing under us. `publicId` and `tier.slug` are the published equivalents.
func TestOCQueryUsesOnlyPublishedFields(t *testing.T) {
	if strings.Contains(ocOrdersQuery, "legacyId") {
		t.Error("ocOrdersQuery selects legacyId, which resolves but is absent from Open Collective's published schema — use publicId (orders) and tier.slug (tiers), which are introspectable and therefore contracted")
	}
	// The published replacements must actually be the ones in use, or this gate
	// would pass on a query that selects neither and silently returns nothing.
	for _, want := range []string{"publicId", "slug"} {
		if !strings.Contains(ocOrdersQuery, want) {
			t.Errorf("ocOrdersQuery does not select %q: the published identifier is what the reconciler keys on", want)
		}
	}
}

// TestOCOrderSourceDecodesAPage pins the mapping from Open Collective's Order onto
// providerOrder. Every field named here was confirmed present on the live schema
// 2026-08-28 (an unknown field returns GRAPHQL_VALIDATION_FAILED, so the field list
// is a real schema read). The fixture deliberately includes the awkward shapes the
// API really produces: a null tier, a null email, null tags and a null
// nextChargeDate.
func TestOCOrderSourceDecodesAPage(t *testing.T) {
	srv := ocFixtureServer(t, "oc_orders_page.json", http.StatusOK)
	src := NewOCOrderSource(srv.Client(), srv.URL, "tok", "ai-agents-memory")

	got, err := src.listOrders(context.Background())
	if err != nil {
		t.Fatalf("listOrders: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 orders, got %d", len(got))
	}

	first := got[0]
	if first.ID != "or_3P8Gkau6N93wF4XwejU71" {
		t.Errorf("ID = %q, want the order's publicId — the PUBLISHED identifier, not the introspection-hidden legacyId", first.ID)
	}
	if first.Status != "ACTIVE" {
		t.Errorf("Status = %q", first.Status)
	}
	if first.TierSlug != "pro-monthly" {
		t.Errorf("TierSlug = %q, want the pro-monthly tier", first.TierSlug)
	}
	if first.AmountValue != 50 || first.Currency != "EUR" {
		t.Errorf("amount = %v %s", first.AmountValue, first.Currency)
	}
	if first.FromAccountEmail != "jane@example.com" {
		t.Errorf("FromAccountEmail = %q", first.FromAccountEmail)
	}
	if len(first.Tags) != 1 || first.Tags[0] != "am-7r7p2vqqufn7j6wb" {
		t.Errorf("Tags = %v, want the attribution tag", first.Tags)
	}
	if first.NextChargeDate != "2026-09-20T09:11:02Z" {
		t.Errorf("NextChargeDate = %q", first.NextChargeDate)
	}

	// A null tier, null email and null tags must decode to zero values rather than
	// panicking — an order made outside a tier is a real thing on a donations
	// platform, and it is exactly what an unattributable contribution looks like.
	third := got[2]
	if third.TierSlug != "" {
		t.Errorf("null tier decoded to %q, want empty", third.TierSlug)
	}
	if third.FromAccountEmail != "" || len(third.Tags) != 0 {
		t.Errorf("null email/tags decoded to %q/%v, want empty", third.FromAccountEmail, third.Tags)
	}
}

// TestOCOrderSourceDistinguishesEmptyFromError is the one that matters most for a
// polling loop: this repo's recurring defect is an empty result that reads as an
// answer. A collective with no contributions and a call that was refused must NOT
// produce the same value, or a permissions failure would look like "nobody paid"
// forever and no plan would ever be activated.
func TestOCOrderSourceDistinguishesEmptyFromError(t *testing.T) {
	t.Run("genuinely empty is a success", func(t *testing.T) {
		srv := ocFixtureServer(t, "oc_orders_empty.json", http.StatusOK)
		src := NewOCOrderSource(srv.Client(), srv.URL, "tok", "ai-agents-memory")
		got, err := src.listOrders(context.Background())
		if err != nil {
			t.Fatalf("an empty collective must not be an error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("want 0 orders, got %d", len(got))
		}
	})

	// A GraphQL failure arrives as HTTP 200 carrying `errors` AND a data block whose
	// account is null. A decoder that reads straight to data.account.orders.nodes
	// sees an empty list and reports success.
	t.Run("errors on a 200 is a failure, not an empty page", func(t *testing.T) {
		srv := ocFixtureServer(t, "oc_orders_errors.json", http.StatusOK)
		src := NewOCOrderSource(srv.Client(), srv.URL, "tok", "ai-agents-memory")
		got, err := src.listOrders(context.Background())
		if err == nil {
			t.Fatalf("a GraphQL errors[] payload returned %d orders and no error", len(got))
		}
		if !strings.Contains(err.Error(), "permission") {
			t.Errorf("error should carry the provider's message, got %v", err)
		}
	})

	t.Run("a non-200 is a failure", func(t *testing.T) {
		srv := ocFixtureServer(t, "oc_orders_empty.json", http.StatusInternalServerError)
		src := NewOCOrderSource(srv.Client(), srv.URL, "tok", "ai-agents-memory")
		if _, err := src.listOrders(context.Background()); err == nil {
			t.Fatal("HTTP 500 returned no error")
		}
	})
}

// TestOCOrderSourceSendsPersonalTokenHeader pins the auth contract: Open Collective
// authenticates the GraphQL API with a Personal-Token header (verified 2026-08-28),
// and without it the call is anonymous, rate-limited to 10/min and cannot see the
// contributor detail reconciliation needs.
func TestOCOrderSourceSendsPersonalTokenHeader(t *testing.T) {
	var gotToken, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("Personal-Token")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"account":{"orders":{"totalCount":0,"nodes":[]}}}}`))
	}))
	t.Cleanup(srv.Close)

	src := NewOCOrderSource(srv.Client(), srv.URL, "s3cret-token", "ai-agents-memory")
	if _, err := src.listOrders(context.Background()); err != nil {
		t.Fatalf("listOrders: %v", err)
	}
	if gotToken != "s3cret-token" {
		t.Fatalf("Personal-Token header = %q, want the configured token", gotToken)
	}
	if !strings.Contains(gotBody, "ai-agents-memory") {
		t.Fatalf("request body does not scope the query to the configured slug: %s", gotBody)
	}
}

// TestOCOrderSourceNeverLogsTheToken keeps a read-only financial credential out of
// error strings, which are logged every reconcile pass by the driver. A token in a
// log line is a token in every log aggregator downstream.
// Every failure path is exercised, not just the first one. An earlier version of
// this test used only a 401 carrying `errors[]`; because the errors block is
// checked before the status, the HTTP-status error path was never reached and a
// mutant that interpolated the token into it SURVIVED. A leak test that covers one
// of several error constructors proves nothing about the others.
func TestOCOrderSourceNeverLogsTheToken(t *testing.T) {
	const token = "s3cret-token-do-not-leak"

	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"graphql errors path", http.StatusUnauthorized, `{"errors":[{"message":"invalid token"}]}`},
		{"http status path", http.StatusForbidden, `{"data":{"account":null}}`},
		{"nil account path", http.StatusOK, `{"data":{"account":null}}`},
		{"undecodable body path", http.StatusOK, `not json at all`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			src := NewOCOrderSource(srv.Client(), srv.URL, token, "ai-agents-memory")
			_, err := src.listOrders(context.Background())
			if err == nil {
				t.Fatalf("%s returned no error", tc.name)
			}
			if strings.Contains(err.Error(), token) {
				t.Fatalf("the personal token leaked into an error string: %v", err)
			}
		})
	}
}
