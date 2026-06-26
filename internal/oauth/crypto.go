// Package oauth implements the OAuth 2.1 surface that claude.ai's remote MCP
// connector requires, validating credentials against this app's own api_keys
// (the merged mcpapi + authcounterapi design). It is STATELESS: authorization
// codes and access/refresh tokens are self-contained AES-256-GCM sealed blobs,
// so there is no token database to keep or revoke against. Short access-token
// TTLs plus refresh-time re-validation bound the revocation window.
//
// crypto.go holds the sealer (confidentiality + integrity for those blobs) and
// the PKCE check. server.go holds the HTTP handlers and the request gate.
package oauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// errBadToken is returned for any malformed, tampered, or non-decryptable token.
// It is deliberately generic so it cannot be used to probe why a token failed.
var errBadToken = errors.New("oauth: invalid token")

// errExpired is returned when a structurally valid token is past its expiry.
var errExpired = errors.New("oauth: token expired")

// tokenKind tags each sealed artifact so an access token can never be replayed
// as an authorization code (or vice versa) even though they share a sealer.
type tokenKind string

const (
	kindCode    tokenKind = "code"
	kindAccess  tokenKind = "access"
	kindRefresh tokenKind = "refresh"
)

// payload is the sealed claim set. Field use varies by kind: a code carries the
// PKCE challenge + redirect_uri; access/refresh carry only the resolved tenant.
type payload struct {
	Kind          tokenKind `json:"k"`
	ID            string    `json:"i,omitempty"`  // code only: unique id for single-use enforcement
	TeamID        string    `json:"t"`
	UserID        string    `json:"u"`
	Role          string    `json:"r"`
	ClientKey     string    `json:"c"`            // the OAuth client_id that minted it
	RedirectURI   string    `json:"ru,omitempty"` // code only
	CodeChallenge string    `json:"cc,omitempty"` // code only (S256)
	Exp           int64     `json:"e"`            // unix seconds; 0 = no expiry
}

// Sealer encrypts and authenticates token payloads with AES-256-GCM. The key is
// derived from a secret string (SHA-256) so any non-empty OAUTH_SECRET_KEY works
// and is exactly 32 bytes. Changing the secret invalidates every live token.
type Sealer struct{ gcm cipher.AEAD }

// NewSealer builds a Sealer from a secret string.
func NewSealer(secret string) (*Sealer, error) {
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{gcm: gcm}, nil
}

// seal returns base64url(nonce || ciphertext) for an opaque, tamper-evident blob.
func (s *Sealer) seal(plain []byte) (string, error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := s.gcm.Seal(nonce, nonce, plain, nil)
	return base64.RawURLEncoding.EncodeToString(ct), nil
}

// open reverses seal, returning errBadToken on any decode/auth failure.
func (s *Sealer) open(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, errBadToken
	}
	ns := s.gcm.NonceSize()
	if len(raw) < ns {
		return nil, errBadToken
	}
	pt, err := s.gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return nil, errBadToken
	}
	return pt, nil
}

// sealPayload JSON-encodes and seals a payload into an opaque token string.
func (s *Sealer) sealPayload(p payload) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return s.seal(b)
}

// openPayload opens a token, asserts its kind, and enforces expiry against now
// (unix seconds). A kind mismatch or expiry is a validation failure, not a leak.
func (s *Sealer) openPayload(token string, want tokenKind, now int64) (payload, error) {
	b, err := s.open(token)
	if err != nil {
		return payload{}, err
	}
	var p payload
	if err := json.Unmarshal(b, &p); err != nil {
		return payload{}, errBadToken
	}
	if p.Kind != want {
		return payload{}, errBadToken
	}
	if p.Exp != 0 && now > p.Exp {
		return payload{}, errExpired
	}
	return p, nil
}

// verifyPKCE checks a code_verifier against an S256 code_challenge
// (RFC 7636): BASE64URL(SHA256(verifier)) == challenge. S256 is the only method
// offered, so a plain verifier never satisfies it. Compared in constant time.
func verifyPKCE(verifier, challenge string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}
