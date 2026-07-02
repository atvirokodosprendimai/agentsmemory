package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// polarTestSecret is a Standard-Webhooks secret ("whsec_" + base64) used to both
// sign test payloads and configure the provider, so verification runs for real.
var polarTestSecret = "whsec_" + base64.StdEncoding.EncodeToString([]byte("polar-webhook-key-32bytes-long!!"))

// polarSignedHeaders signs payload exactly as Polar/Standard-Webhooks does, so the
// production verifier accepts it. It deliberately reuses standardWebhookKey — the
// same key derivation the verifier uses — so the test proves the round trip, not a
// re-implementation.
func polarSignedHeaders(secret, msgID string, ts int64, payload []byte) http.Header {
	mac := hmac.New(sha256.New, standardWebhookKey(secret))
	mac.Write([]byte(msgID + "." + strconv.FormatInt(ts, 10) + "."))
	mac.Write(payload)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	h := http.Header{}
	h.Set("webhook-id", msgID)
	h.Set("webhook-timestamp", strconv.FormatInt(ts, 10))
	h.Set("webhook-signature", "v1,"+sig)
	return h
}

func TestVerifyStandardWebhook_AcceptsValidRejectsTampered(t *testing.T) {
	payload := []byte(`{"type":"checkout.updated","data":{"status":"succeeded"}}`)
	now := time.Now().Unix()
	headers := polarSignedHeaders(polarTestSecret, "msg_1", now, payload)

	if err := verifyStandardWebhook(polarTestSecret, headers, payload); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	// Any tamper to the body invalidates the signature.
	if err := verifyStandardWebhook(polarTestSecret, headers, append(payload, '!')); err == nil {
		t.Fatal("tampered payload accepted")
	}
	// A different secret must not verify.
	if err := verifyStandardWebhook("whsec_"+base64.StdEncoding.EncodeToString([]byte("other-key")), headers, payload); err == nil {
		t.Fatal("wrong secret accepted")
	}
}

func TestVerifyStandardWebhook_RejectsStaleAndMissing(t *testing.T) {
	payload := []byte(`{"ok":true}`)
	// A timestamp well outside the tolerance window is a replay and must be rejected.
	stale := time.Now().Unix() - 10*60
	if err := verifyStandardWebhook(polarTestSecret, polarSignedHeaders(polarTestSecret, "m", stale, payload), payload); err == nil {
		t.Fatal("stale timestamp accepted")
	}
	// Missing Standard-Webhooks headers must be rejected, never treated as unsigned-ok.
	if err := verifyStandardWebhook(polarTestSecret, http.Header{}, payload); err == nil {
		t.Fatal("missing headers accepted")
	}
}

// polarEvent marshals a Polar webhook envelope for tests.
func polarEvent(t *testing.T, eventType string, data map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"type": eventType, "data": data})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return b
}

func TestPolarParseWebhook_CheckoutSucceeded_Activated(t *testing.T) {
	p := &polarProvider{webhookSecret: polarTestSecret}
	payload := polarEvent(t, "checkout.updated", map[string]any{
		"status":          "succeeded",
		"customer_id":     "cus_polar",
		"subscription_id": "sub_polar",
		"metadata":        map[string]any{"team_id": "team_1", "plan_code": "pro_annual"},
	})
	evt, err := p.parseWebhook(payload, polarSignedHeaders(polarTestSecret, "m1", time.Now().Unix(), payload))
	if err != nil {
		t.Fatalf("parseWebhook: %v", err)
	}
	if evt.kind != eventActivated {
		t.Fatalf("kind = %v, want eventActivated", evt.kind)
	}
	if evt.teamID != "team_1" || evt.planCode != "pro_annual" ||
		evt.customerID != "cus_polar" || evt.subscriptionID != "sub_polar" {
		t.Fatalf("event not mapped: %+v", evt)
	}
}

func TestPolarParseWebhook_CheckoutInterim_Ignored(t *testing.T) {
	p := &polarProvider{webhookSecret: polarTestSecret}
	// A checkout that has not succeeded yet must not upgrade anyone.
	payload := polarEvent(t, "checkout.updated", map[string]any{"status": "open"})
	evt, err := p.parseWebhook(payload, polarSignedHeaders(polarTestSecret, "m2", time.Now().Unix(), payload))
	if err != nil {
		t.Fatalf("parseWebhook: %v", err)
	}
	if evt.kind != eventIgnored {
		t.Fatalf("interim checkout not ignored: %+v", evt)
	}
}

func TestPolarParseWebhook_SubscriptionRevoked_Canceled(t *testing.T) {
	p := &polarProvider{webhookSecret: polarTestSecret}
	payload := polarEvent(t, "subscription.revoked", map[string]any{"id": "sub_polar", "status": "revoked"})
	evt, err := p.parseWebhook(payload, polarSignedHeaders(polarTestSecret, "m3", time.Now().Unix(), payload))
	if err != nil {
		t.Fatalf("parseWebhook: %v", err)
	}
	if evt.kind != eventCanceled || evt.subscriptionID != "sub_polar" {
		t.Fatalf("revoked not mapped to canceled: %+v", evt)
	}
}

func TestPolarParseWebhook_SubscriptionCanceled_Ignored(t *testing.T) {
	p := &polarProvider{webhookSecret: polarTestSecret}
	// "canceled" means won't-renew but still active until period end — NOT a downgrade.
	payload := polarEvent(t, "subscription.canceled", map[string]any{"id": "sub_polar"})
	evt, err := p.parseWebhook(payload, polarSignedHeaders(polarTestSecret, "m4", time.Now().Unix(), payload))
	if err != nil {
		t.Fatalf("parseWebhook: %v", err)
	}
	if evt.kind != eventIgnored {
		t.Fatalf("canceled (will-not-renew) should be ignored, got: %+v", evt)
	}
}

func TestPolarParseWebhook_BadSignature_Rejected(t *testing.T) {
	p := &polarProvider{webhookSecret: polarTestSecret}
	payload := polarEvent(t, "checkout.updated", map[string]any{"status": "succeeded"})
	bad := http.Header{}
	bad.Set("webhook-id", "m5")
	bad.Set("webhook-timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	bad.Set("webhook-signature", "v1,deadbeef")
	if _, err := p.parseWebhook(payload, bad); err == nil {
		t.Fatal("forged signature accepted")
	}
}

func TestPolarParseWebhook_NoSecret_FailsClosed(t *testing.T) {
	p := &polarProvider{} // POLAR_WEBHOOK_SECRET unset
	payload := polarEvent(t, "checkout.updated", map[string]any{"status": "succeeded"})
	if _, err := p.parseWebhook(payload, polarSignedHeaders(polarTestSecret, "m6", time.Now().Unix(), payload)); err == nil {
		t.Fatal("expected fail-closed rejection when webhook secret is unset")
	}
}
