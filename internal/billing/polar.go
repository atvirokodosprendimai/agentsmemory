package billing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// polarProvider is the Polar implementation of the billing seams. Polar is a
// Merchant of Record — it handles EU VAT/sales-tax for us — which is why it exists
// alongside Stripe (decision 2026-07-02). Its surface here is tiny: one POST to
// open a hosted checkout, and Standard-Webhooks verification of the callbacks. A
// thin net/http client (rather than the full generated SDK) keeps the dependency
// footprint and the compiled surface small for those two calls.
type polarProvider struct {
	accessToken   string // Organization Access Token — the API bearer credential
	webhookSecret string // Standard-Webhooks signing secret from the Polar dashboard
	baseURL       string // sandbox or production API host
	httpc         *http.Client
}

// newPolarProvider builds the provider when Polar is configured, returning nil
// when the access token is unset so Service reports Enabled()==false and the
// dashboard shows no upgrade button. The API host defaults to sandbox so an
// unset/unknown POLAR_SERVER can never accidentally hit live billing.
func newPolarProvider(cfg Config) *polarProvider {
	if cfg.PolarAccessToken == "" {
		return nil
	}
	return &polarProvider{
		accessToken:   cfg.PolarAccessToken,
		webhookSecret: cfg.PolarWebhookSecret,
		baseURL:       polarBaseURL(cfg.PolarServer),
		httpc:         &http.Client{Timeout: 15 * time.Second},
	}
}

// polarBaseURL maps our POLAR_SERVER selector to Polar's API host. Only the
// explicit "production" value reaches live billing; everything else (including the
// empty default) stays on the sandbox host.
func polarBaseURL(server string) string {
	if server == "production" {
		return "https://api.polar.sh"
	}
	return "https://sandbox-api.polar.sh"
}

// polarCheckoutReq is the create-checkout request body. Polar sells *products*
// (each recurring product carries its own interval), so a plan maps to one product
// id in `products`. metadata is our trusted attribution channel: Polar echoes it
// back on the checkout webhook, the same role client_reference_id plays for Stripe.
type polarCheckoutReq struct {
	Products      []string          `json:"products"`
	SuccessURL    string            `json:"success_url,omitempty"`
	CustomerEmail string            `json:"customer_email,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// polarCheckoutResp is the slice of the 201 response we use: the hosted URL to
// redirect the customer to.
type polarCheckoutResp struct {
	URL string `json:"url"`
}

// createCheckout opens a Polar hosted checkout for the plan's product and returns
// its URL. Polar has no cancel-url concept, so in.CancelURL is intentionally
// unused here (the customer simply navigates away). The trailing slash on the path
// is required — Polar 307-redirects the un-slashed form, which would drop the POST
// body.
func (p *polarProvider) createCheckout(ctx context.Context, in checkoutInput) (string, error) {
	body, err := json.Marshal(polarCheckoutReq{
		Products:      []string{in.PriceID},
		SuccessURL:    in.SuccessURL,
		CustomerEmail: in.CustomerEmail,
		Metadata:      map[string]string{"team_id": in.TeamID, "plan_code": in.PlanCode},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/checkouts/", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+p.accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	// Cap the read: a checkout response is small, and this is a network boundary.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("billing: polar checkout: status %d: %s", resp.StatusCode, raw)
	}
	var out polarCheckoutResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("billing: decode polar checkout: %w", err)
	}
	if out.URL == "" {
		return "", fmt.Errorf("billing: polar checkout returned no url")
	}
	return out.URL, nil
}

// polarWebhookEnvelope is Polar's webhook shape: an event type plus the affected
// object as data. Only the fields billing acts on are decoded.
type polarWebhookEnvelope struct {
	Type string           `json:"type"`
	Data polarWebhookData `json:"data"`
}

// polarWebhookData is the union of the checkout and subscription object fields we
// read. metadata is decoded as map[string]any because Polar allows string, number
// and bool values; our own keys are strings and are coerced back with metaString.
type polarWebhookData struct {
	ID             string         `json:"id"`              // subscription id (subscription events)
	Status         string         `json:"status"`          // checkout status: open|...|succeeded
	CustomerID     string         `json:"customer_id"`     // checkout: the resulting customer
	SubscriptionID string         `json:"subscription_id"` // checkout: the resulting subscription
	Metadata       map[string]any `json:"metadata"`        // our team_id/plan_code, echoed back
}

// parseWebhook verifies a Polar webhook (Standard-Webhooks signature) and
// normalizes it. It activates on a *succeeded* checkout — the point the payment is
// final and the object carries our metadata plus the new subscription id — and
// downgrades on subscription.revoked (access actually ends). subscription.canceled
// is deliberately NOT a downgrade: in Polar it means "won't renew" while the
// workspace stays Pro until the period end, so acting on it would revoke access a
// customer already paid for.
func (p *polarProvider) parseWebhook(payload []byte, headers http.Header) (providerEvent, error) {
	if p.webhookSecret == "" {
		// Fail closed: without a secret we cannot verify, so we must reject.
		return providerEvent{}, fmt.Errorf("billing: polar webhook secret not configured")
	}
	if err := verifyStandardWebhook(p.webhookSecret, headers, payload); err != nil {
		return providerEvent{}, fmt.Errorf("billing: polar webhook signature: %w", err)
	}
	var env polarWebhookEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return providerEvent{}, fmt.Errorf("billing: decode polar webhook: %w", err)
	}
	metaString := func(k string) string {
		if v, ok := env.Data.Metadata[k].(string); ok {
			return v
		}
		return ""
	}
	switch env.Type {
	case "checkout.updated":
		// Only a finalized checkout upgrades the workspace; earlier statuses (open,
		// confirmed) are interim and ignored.
		if env.Data.Status != "succeeded" {
			return providerEvent{}, nil
		}
		return providerEvent{
			kind:           eventActivated,
			teamID:         metaString("team_id"),
			planCode:       metaString("plan_code"),
			customerID:     env.Data.CustomerID,
			subscriptionID: env.Data.SubscriptionID,
		}, nil
	case "subscription.revoked":
		// The data object is the subscription; its id is the key we recorded at
		// activation, so applyCanceled can find the workspace to downgrade.
		return providerEvent{kind: eventCanceled, subscriptionID: env.Data.ID}, nil
	default:
		return providerEvent{}, nil
	}
}

// verifyStandardWebhook validates a payload against the Standard Webhooks spec that
// Polar follows (https://www.standardwebhooks.com). The signed content is
// "{id}.{timestamp}.{body}", HMAC-SHA256'd with the decoded secret and base64'd;
// the webhook-signature header is a space-separated list of "v<ver>,<sig>" entries
// and a match against any is a pass. The timestamp is checked within a tolerance so
// a captured payload cannot be replayed indefinitely. Comparison is constant-time.
func verifyStandardWebhook(secret string, headers http.Header, payload []byte) error {
	// Fail closed on an unconfigured secret: an empty secret decodes to an empty HMAC
	// key, which an attacker who knows the payload shape could forge. The live Polar
	// path also guards this before calling here; repeating it makes the verifier safe
	// for any caller (defense in depth).
	if secret == "" {
		return fmt.Errorf("webhook secret not configured")
	}
	msgID := headers.Get("webhook-id")
	tsStr := headers.Get("webhook-timestamp")
	sigHeader := headers.Get("webhook-signature")
	if msgID == "" || tsStr == "" || sigHeader == "" {
		return fmt.Errorf("missing Standard-Webhooks headers")
	}
	// Reject stale/forward-dated deliveries to bound replay (5-minute window, the
	// spec's recommended tolerance).
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return fmt.Errorf("bad webhook-timestamp: %w", err)
	}
	const toleranceSec = 5 * 60
	if diff := time.Now().Unix() - ts; diff > toleranceSec || diff < -toleranceSec {
		return fmt.Errorf("webhook timestamp outside tolerance")
	}

	// Polar deviates from strict Standard Webhooks: depending on how the endpoint's
	// secret was created, Polar's HMAC key is either the spec's base64-DECODED secret
	// or the raw secret STRING itself. We therefore verify against every deterministic
	// key derivation of the SAME secret and accept the first match. This tolerates the
	// provider's format without weakening anything — a forger who does not know the
	// secret cannot match any derivation. (Requiring strict base64 here previously
	// rejected real Polar secrets with "not valid base64".)
	signedPrefix := []byte(msgID + "." + tsStr + ".")
	keys := standardWebhookKeys(secret)

	// The header may list multiple signatures (e.g. during a secret rotation); accept
	// if any entry matches under any candidate key. Each entry is
	// "<version>,<base64-signature>"; only the "v1" symmetric-HMAC scheme is valid.
	for _, entry := range strings.Fields(sigHeader) {
		version, sig, found := strings.Cut(entry, ",")
		if !found || version != "v1" {
			continue
		}
		for _, key := range keys {
			mac := hmac.New(sha256.New, key)
			mac.Write(signedPrefix)
			mac.Write(payload)
			expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
			if hmac.Equal([]byte(sig), []byte(expected)) {
				return nil
			}
		}
	}
	return fmt.Errorf("no matching signature")
}

// standardWebhookKeys returns the candidate HMAC keys to try for a configured secret.
// Standard Webhooks specifies a base64 secret (optionally "whsec_"-prefixed) whose
// DECODED bytes are the key; Polar, however, signs some endpoints with the raw secret
// STRING as the key. To interoperate with both, we return the raw secret as given,
// the raw secret with an optional "whsec_" prefix stripped, and — when the stripped
// secret is valid base64 — its decoded bytes. The duplicate raw form (when there is
// no prefix) is collapsed.
func standardWebhookKeys(secret string) [][]byte {
	stripped := strings.TrimPrefix(secret, "whsec_")
	keys := [][]byte{[]byte(secret)}
	if stripped != secret {
		keys = append(keys, []byte(stripped))
	}
	if b, err := base64.StdEncoding.DecodeString(stripped); err == nil {
		keys = append(keys, b)
	}
	return keys
}
