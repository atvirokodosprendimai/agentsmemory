// openCollectiveProvider is the OpenCollective implementation of the billing
// seams (replacing Polar, decision 2026-08-17). OpenCollective is a donations
// platform, not a merchant of record: it hosts the contribution checkout and
// takes the money, but its "webhooks" feature is an outgoing Slack/Discord-style
// notification channel — there is no signed inbound webhook to verify, and no
// per-customer portal API. So this provider's surface is deliberately small:
// each plan maps to a static hosted contribution-checkout URL, and plan
// activation after payment is an operator action (the set-plan CLI) rather than
// a webhook-driven flip. The webhook seam fails closed on every call so nothing
// can ever be activated from an unsigned, unverifiable event.
package billing

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// newOpenCollectiveProvider builds the provider when at least one plan has a
// configured checkout URL, returning nil otherwise so Service reports
// Enabled()==false and the dashboard shows no upgrade button. No credentials are
// needed — the checkout is a public contribution page — so the URL map is the
// whole configuration surface.
func newOpenCollectiveProvider(cfg Config) *openCollectiveProvider {
	if len(cfg.PriceByPlanCode) == 0 {
		return nil
	}
	return &openCollectiveProvider{
		checkoutURLs: cfg.PriceByPlanCode,
		projectURL:   cfg.OpenCollectiveProjectURL,
	}
}

// openCollectiveProvider maps our plan codes to OpenCollective contribution
// checkouts. checkoutURLs is keyed by plan code (pro_monthly, pro_annual); each
// value is the tier's hosted checkout URL, e.g.
//
//	https://opencollective.com/<org>/projects/<project>/contribute/<tier>/checkout
type openCollectiveProvider struct {
	checkoutURLs map[string]string // plan code -> hosted contribution-checkout URL
	projectURL   string            // stable project page (manage/cancel surface)
}

// createCheckout returns the hosted OpenCollective contribution-checkout URL for
// the plan. The URL IS the provider's "price id" here: billing.Config maps each
// sellable plan code to its tier's checkout page, so there is nothing to create —
// no API call, no request. Unknown plan codes were already refused by
// Service.StartCheckout (ErrUnknownPlan); the guard keeps the provider safe for
// direct callers too.
func (p *openCollectiveProvider) createCheckout(_ context.Context, in checkoutInput) (string, error) {
	raw := p.checkoutURLs[in.PlanCode]
	if raw == "" {
		return "", fmt.Errorf("billing: no opencollective checkout for plan %q", in.PlanCode)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("billing: opencollective checkout URL for plan %q is not a URL: %w", in.PlanCode, err)
	}
	// Carry who is buying, so the contribution can be attributed when it is read
	// back from the API (ADR-042). These are Open Collective's own contribution-flow
	// parameters, read 2026-08-28 from opencollective-frontend's contribution flow,
	// which accepts amount, interval, contributeAs, email, name, legalName,
	// paymentMethod, tags and redirect.
	//
	// The existing query is preserved rather than replaced: an operator may have
	// pinned an interval or an amount on the configured tier URL, and clobbering it
	// would silently change what the contributor is asked to pay.
	q := u.Query()
	if in.TeamID != "" {
		q.Set("tags", intentTag(in.TeamID))
	}
	if in.CustomerEmail != "" {
		q.Set("email", in.CustomerEmail)
	}
	// `redirect` is validated by Open Collective before it is followed; if it
	// rejects ours the contributor simply stays on their site, which is the
	// behaviour before this parameter existed.
	if in.SuccessURL != "" {
		q.Set("redirect", in.SuccessURL)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// createPortalSession has no provider API to call: OpenCollective has no
// pre-authenticated customer portal. Recurring contributions are managed inside
// OpenCollective's own UI from the contributor's profile, reachable via the
// project page, so that stable URL is the closest manage/cancel surface we can
// hand the admin. Configuring OPENCOLLECTIVE_PROJECT_URL is therefore the
// provider's only optional wiring.
func (p *openCollectiveProvider) createPortalSession(_ context.Context, _, _ string) (string, error) {
	if p.projectURL == "" {
		return "", fmt.Errorf("billing: opencollective project URL not configured")
	}
	return p.projectURL, nil
}

// parseWebhook fails closed on every call: OpenCollective sends no signed
// webhook channel, so there is no payload we can trust enough to flip a plan
// from. Activation after a payment is an operator action — the superadmin
// set-plan CLI — never an automated webhook. The endpoint for this provider is
// not even registered (see cmd/server/main.go); this keeps the seam honest and
// any stray POST to a registered webhook route rejected.
func (p *openCollectiveProvider) parseWebhook(_ []byte, _ http.Header) (providerEvent, error) {
	return providerEvent{}, fmt.Errorf("billing: opencollective sends no signed webhook; activate with the set-plan CLI")
}
