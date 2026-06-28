package tenant

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// ErrTokenUnavailable is returned by RevealToken when a token cannot be shown:
// reveal is disabled (no key configured), there is no active key, the key
// predates the reveal feature (empty seal), or the seal cannot be opened (the
// token key changed since the key was minted). It is deliberately one opaque
// error so the dashboard shows a single "can't reveal — rotate" message.
var ErrTokenUnavailable = errors.New("tenant: token cannot be revealed")

// sealer encrypts and authenticates a plaintext bearer token with AES-256-GCM so
// the dashboard can reveal it after creation, while a database-only leak stays
// useless without the key. The 32-byte key is SHA-256 of a secret string, so any
// non-empty AGENTSMEMORY_TOKEN_KEY works and is exactly the right length. This
// mirrors the oauth package's Sealer but lives here to avoid an import cycle
// (oauth imports tenant), and seals raw tokens rather than JSON claim payloads.
type sealer struct{ gcm cipher.AEAD }

// newSealer derives an AES-256-GCM AEAD from a secret string. It only errors on a
// bad cipher construction, which cannot happen for a fixed 32-byte key.
func newSealer(secret string) (*sealer, error) {
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{gcm: gcm}, nil
}

// seal returns base64url(nonce || ciphertext): an opaque, tamper-evident blob
// safe to store in a text column. A fresh random nonce per call is required for
// GCM, so the same token seals to a different blob each time.
func (s *sealer) seal(plain string) (string, error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := s.gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.RawURLEncoding.EncodeToString(ct), nil
}

// open reverses seal, returning ErrTokenUnavailable on any decode or
// authentication failure so a tampered or undecryptable blob never leaks why.
func (s *sealer) open(blob string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(blob)
	if err != nil {
		return "", ErrTokenUnavailable
	}
	ns := s.gcm.NonceSize()
	if len(raw) < ns {
		return "", ErrTokenUnavailable
	}
	pt, err := s.gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", ErrTokenUnavailable
	}
	return string(pt), nil
}
