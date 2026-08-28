package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ocOrdersQuery asks for the collective's incoming contributions. It is one
// package-level constant rather than a string built at the call site so a schema
// change has exactly one place to be fixed.
//
// Every field here appears in the PUBLISHED schema, checked by introspection on
// 2026-08-28 against both api.opencollective.com and api-staging.opencollective.com.
// That is a stricter bar than "the query validates", and it was chosen after the
// looser one nearly shipped a latent break:
//
// ⚠ `Order.legacyId` and `Tier.legacyId` both WORK and are both ABSENT from
// introspection on both environments. A field the schema does not publish carries no
// contract and can be withdrawn without a deprecation cycle — so keying a durable
// subscription id on one was a dependency with no promise behind it. The first draft
// did exactly that. `publicId` (e.g. "or_3P8Gkau6N93wF4XwejU71") and `tier.slug` are
// published, so this query now uses only those.
//
// filter: INCOMING selects contributions TO this account, excluding anything it
// contributed elsewhere. Paging is by offset; the API reports totalCount, but the
// caller pages until a short page arrives rather than trusting a count it did not
// re-read.
const ocOrdersQuery = `query ($slug: String!, $limit: Int!, $offset: Int!) {
  account(slug: $slug) {
    orders(limit: $limit, offset: $offset, filter: INCOMING) {
      totalCount
      nodes {
        publicId
        status
        frequency
        createdAt
        nextChargeDate
        amount { value currency }
        tier { slug }
        fromAccount { slug name type ... on Individual { email } }
        tags
      }
    }
  }
}`

// DefaultOpenCollectiveAPIURL is the public GraphQL v2 endpoint, verified live on
// 2026-08-28. It is exported so the composition root can default the knob rather
// than duplicating the literal, and it is overridable so a test or a staging
// environment can point elsewhere.
const DefaultOpenCollectiveAPIURL = "https://api.opencollective.com/graphql/v2"

// DefaultReconcileInterval is how often orders are re-read when the operator sets
// no period. Fifteen minutes is chosen against the measured authenticated rate
// limit of 100 requests/minute — one pass costs a handful of requests, so this is
// three orders of magnitude inside the budget — and against the product: on a
// EUR 50/month plan, minutes of activation latency cost nothing, while a tighter
// loop would buy latency nobody asked for at the price of more API traffic.
const DefaultReconcileInterval = 15 * time.Minute

// ocPageSize is how many orders are requested per call. Small enough that one page
// covers any realistic day's contributions on this project, large enough that a
// backfill does not spend many round trips. One reconcile pass costs a handful of
// requests against a measured limit of 100/min authenticated.
const ocPageSize = 100

// ocMaxPages bounds a single pass so a paging bug cannot spin forever. When it
// bites, the caller is told rather than silently handed a truncated set — a
// truncation that reads as "that is all of them" is how a reconciler quietly stops
// activating people.
const ocMaxPages = 20

// providerOrder is one contribution, in billing's own vocabulary, with Open
// Collective's object model decoded away. It is the read-side counterpart to
// providerEvent: this says what the provider reports, providerEvent says what we
// decided it means.
type providerOrder struct {
	ID               string   // the order's publicId ("or_…") — the stable lifecycle key
	Status           string   // Open Collective's OrderStatus, mapped by the reconciler
	Frequency        string   // ONETIME | MONTHLY | YEARLY
	TierSlug         string   // e.g. "pro-monthly"; empty when the contribution names no tier
	AmountValue      float64  // in Currency units, not cents
	Currency         string   //
	Tags             []string // carries our attribution tag when the checkout set one
	FromAccountSlug  string   //
	FromAccountEmail string   // empty unless the token may read it
	NextChargeDate   string   // RFC3339; empty for a one-off or a cancelled order
	CreatedAt        string   // RFC3339
}

// orderSource reads contributions from the payment provider. It is the fourth seam
// beside checkoutAPI, webhookParser and portalAPI, and the only READ one: the other
// three either write or verify. It exists because OpenCollective sends no signed
// webhook, so the only trustworthy way to learn about a payment is to ask for it
// over an authenticated channel (ADR-042).
type orderSource interface {
	listOrders(ctx context.Context) ([]providerOrder, error)
}

// ocOrderSource is the Open Collective GraphQL implementation of orderSource. It is
// strictly read-only: no mutation is ever sent.
type ocOrderSource struct {
	client *http.Client
	apiURL string
	token  string
	slug   string
}

// NewOCOrderSource builds the order source for the composition root. The
// http.Client is injected so the caller owns the timeout — this runs on a
// background loop and must never be able to hang it forever.
//
// Exported (unlike the three write seams, which are constructed inside NewService)
// because reconciliation is assembled in cmd/server: the loop, its client and its
// lifecycle context belong to the process, not to the billing service.
func NewOCOrderSource(client *http.Client, apiURL, token, slug string) *ocOrderSource {
	if client == nil {
		client = http.DefaultClient
	}
	return &ocOrderSource{client: client, apiURL: apiURL, token: token, slug: slug}
}

// ocOrdersResponse mirrors the GraphQL envelope. Optional legs are pointers so a
// null tier or a null email decodes to a zero value instead of failing: a
// contribution outside any tier is ordinary on a donations platform, and it is
// precisely what an unattributable payment looks like.
type ocOrdersResponse struct {
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Data struct {
		Account *struct {
			Orders struct {
				TotalCount int `json:"totalCount"`
				Nodes      []struct {
					PublicID       string  `json:"publicId"`
					Status         string  `json:"status"`
					Frequency      string  `json:"frequency"`
					CreatedAt      string  `json:"createdAt"`
					NextChargeDate *string `json:"nextChargeDate"`
					Amount         struct {
						Value    float64 `json:"value"`
						Currency string  `json:"currency"`
					} `json:"amount"`
					Tier *struct {
						Slug string `json:"slug"`
					} `json:"tier"`
					FromAccount *struct {
						Slug  string  `json:"slug"`
						Name  string  `json:"name"`
						Type  string  `json:"type"`
						Email *string `json:"email"`
					} `json:"fromAccount"`
					Tags []string `json:"tags"`
				} `json:"nodes"`
			} `json:"orders"`
		} `json:"account"`
	} `json:"data"`
}

// listOrders returns every incoming contribution the token may see, paging until a
// short page arrives.
//
// ⚠ An empty result and a failed call are deliberately different values. A GraphQL
// refusal arrives as HTTP 200 carrying `errors` AND a data block whose account is
// null, so a decoder that reads straight through to nodes sees an empty list and
// reports success — and a polling reconciler would then treat a permissions failure
// as "nobody has paid", forever, without a single error in the log.
func (s *ocOrderSource) listOrders(ctx context.Context) ([]providerOrder, error) {
	var out []providerOrder
	for page := 0; page < ocMaxPages; page++ {
		batch, err := s.fetchPage(ctx, page*ocPageSize)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < ocPageSize {
			return out, nil
		}
	}
	return out, fmt.Errorf("billing: opencollective orders exceeded %d pages of %d; refusing to report a truncated set as complete", ocMaxPages, ocPageSize)
}

// fetchPage performs one authenticated request and decodes it.
func (s *ocOrderSource) fetchPage(ctx context.Context, offset int) ([]providerOrder, error) {
	body, err := json.Marshal(map[string]any{
		"query": ocOrdersQuery,
		"variables": map[string]any{
			"slug": s.slug, "limit": ocPageSize, "offset": offset,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("billing: encode opencollective query: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("billing: build opencollective request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Open Collective authenticates the GraphQL API with this header (verified
	// 2026-08-28). Anonymous calls are capped at 10 req/min and cannot read
	// contributor detail.
	req.Header.Set("Personal-Token", s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		// ⚠ Never wrap the raw error blindly into a message containing the token: the
		// URL is safe, the header is not, and this string is logged every pass.
		return nil, fmt.Errorf("billing: opencollective request failed: %w", err)
	}
	defer resp.Body.Close()

	var decoded ocOrdersResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("billing: decode opencollective response (HTTP %d): %w", resp.StatusCode, err)
	}
	// errors[] is checked BEFORE the status and before the data block, because it is
	// the case that otherwise looks like success.
	if len(decoded.Errors) > 0 {
		msgs := make([]string, 0, len(decoded.Errors))
		for _, e := range decoded.Errors {
			msgs = append(msgs, e.Message)
		}
		return nil, fmt.Errorf("billing: opencollective refused the orders query: %s", strings.Join(msgs, "; "))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("billing: opencollective returned HTTP %d", resp.StatusCode)
	}
	if decoded.Data.Account == nil {
		return nil, fmt.Errorf("billing: opencollective returned no account for slug %q", s.slug)
	}

	nodes := decoded.Data.Account.Orders.Nodes
	out := make([]providerOrder, 0, len(nodes))
	for _, n := range nodes {
		o := providerOrder{
			ID:          n.PublicID,
			Status:      n.Status,
			Frequency:   n.Frequency,
			AmountValue: n.Amount.Value,
			Currency:    n.Amount.Currency,
			Tags:        n.Tags,
			CreatedAt:   n.CreatedAt,
		}
		if n.Tier != nil {
			o.TierSlug = n.Tier.Slug
		}
		if n.FromAccount != nil {
			o.FromAccountSlug = n.FromAccount.Slug
			if n.FromAccount.Email != nil {
				o.FromAccountEmail = *n.FromAccount.Email
			}
		}
		if n.NextChargeDate != nil {
			o.NextChargeDate = *n.NextChargeDate
		}
		out = append(out, o)
	}
	return out, nil
}
