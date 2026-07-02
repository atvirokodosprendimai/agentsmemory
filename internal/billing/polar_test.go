package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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
	sig := polarSign(secret, msgID, ts, payload)
	h := http.Header{}
	h.Set("webhook-id", msgID)
	h.Set("webhook-timestamp", strconv.FormatInt(ts, 10))
	h.Set("webhook-signature", "v1,"+sig)
	return h
}

// polarSign computes the base64 Standard-Webhooks signature for a payload using the
// spec's base64-decoded key — one of the derivations verifyStandardWebhook accepts.
func polarSign(secret, msgID string, ts int64, payload []byte) string {
	return signWith(base64Key(secret), msgID, ts, payload)
}

// signWith computes a v1 base64 HMAC signature with an explicit key.
func signWith(key []byte, msgID string, ts int64, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(msgID + "." + strconv.FormatInt(ts, 10) + "."))
	mac.Write(payload)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// base64Key mirrors the strict Standard-Webhooks key derivation for test secrets.
func base64Key(secret string) []byte {
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		panic(err) // test base64 secrets are always valid
	}
	return b
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

func TestVerifyStandardWebhook_RejectsEmptyWrongSecretAndBadVersion(t *testing.T) {
	payload := []byte(`{"ok":true}`)
	now := time.Now().Unix()
	valid := polarSignedHeaders(polarTestSecret, "m", now, payload)

	// Empty secret must fail closed inside the verifier itself (defense in depth).
	if err := verifyStandardWebhook("", valid, payload); err == nil {
		t.Fatal("empty secret accepted")
	}
	// A non-base64 secret no longer errors (Polar signs some endpoints with the raw
	// secret string), but a WRONG secret must still fail to match.
	if err := verifyStandardWebhook("polar_raw_secret_/*not*/base64", valid, payload); err == nil {
		t.Fatal("wrong secret accepted")
	}
	// A correct HMAC carried under a non-"v1" version label must not be accepted.
	h := http.Header{}
	h.Set("webhook-id", "m")
	h.Set("webhook-timestamp", strconv.FormatInt(now, 10))
	h.Set("webhook-signature", "v2,"+polarSign(polarTestSecret, "m", now, payload))
	if err := verifyStandardWebhook(polarTestSecret, h, payload); err == nil {
		t.Fatal("non-v1 version label accepted")
	}
}

func TestVerifyStandardWebhook_AcceptsPolarRawSecretKey(t *testing.T) {
	// Polar signs some endpoints with the raw secret STRING as the HMAC key (a
	// deviation from the base64-decoded Standard Webhooks spec). A non-base64 secret
	// signed that way must verify — this is the case that was 400-ing in the field.
	secret := "polar_raw_webhook_secret_not_base64_@@"
	payload := []byte(`{"type":"checkout.updated","data":{"status":"succeeded"}}`)
	now := time.Now().Unix()
	h := http.Header{}
	h.Set("webhook-id", "mid")
	h.Set("webhook-timestamp", strconv.FormatInt(now, 10))
	h.Set("webhook-signature", "v1,"+signWith([]byte(secret), "mid", now, payload))
	if err := verifyStandardWebhook(secret, h, payload); err != nil {
		t.Fatalf("raw-secret Polar signature rejected: %v", err)
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
