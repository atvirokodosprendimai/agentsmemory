package passkey

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Service runs the WebAuthn ceremonies over the credential Repo. It is the only
// type that touches go-webauthn; its methods speak JSON at the edges (options out
// to the browser, the opaque ceremony session, the browser's response in) so the
// web layer stays free of WebAuthn types. The ceremony session is returned to the
// caller to stash (we keep it in a short-lived signed cookie), not held here — so
// the service is stateless and horizontal-scale-friendly like the rest of the app.
type Service struct {
	w    *webauthn.WebAuthn
	repo *Repo
}

// NewService builds the WebAuthn instance from the RP config. Every credential is
// registered as a resident (discoverable) key with user verification, so one
// passkey serves both the passwordless flow (the authenticator can find it with
// no username) and the second-factor flow.
func NewService(cfg Config, repo *Repo) (*Service, error) {
	w, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.RPID,
		RPDisplayName: cfg.RPDisplayName,
		RPOrigins:     cfg.RPOrigins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationPreferred,
		},
		AttestationPreference: protocol.PreferNoAttestation, // we don't need attestation statements
	})
	if err != nil {
		return nil, fmt.Errorf("passkey: webauthn init: %w", err)
	}
	return &Service{w: w, repo: repo}, nil
}

// Repo exposes the credential store for the account UI (list/delete/count).
func (s *Service) Repo() *Repo { return s.repo }

// BeginRegistration starts enrolment for a known (logged-in) user. It returns the
// creation options as JSON for the browser's parseCreationOptionsFromJSON, plus
// the opaque ceremony session for the caller to stash until FinishRegistration.
func (s *Service) BeginRegistration(ctx context.Context, id, name, display string) (options, session []byte, err error) {
	creds, err := s.repo.credentialsFor(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	u := &user{id: id, name: name, display: display, creds: creds}
	creation, sess, err := s.w.BeginRegistration(u)
	if err != nil {
		return nil, nil, err
	}
	return marshalPair(creation.Response, sess)
}

// FinishRegistration verifies the browser's attestation response against the
// stashed session and stores the new credential under the user with a label.
func (s *Service) FinishRegistration(ctx context.Context, id, name, display, label string, session, response []byte) error {
	sess, err := unmarshalSession(session)
	if err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return err
	}
	u := &user{id: id, name: name, display: display}
	cred, err := s.w.CreateCredential(u, *sess, parsed)
	if err != nil {
		return err
	}
	return s.repo.add(ctx, id, label, cred)
}

// BeginDiscoverableLogin starts a passwordless login: no username is supplied, so
// the authenticator offers whatever resident credentials it holds for this RP.
func (s *Service) BeginDiscoverableLogin() (options, session []byte, err error) {
	assertion, sess, err := s.w.BeginDiscoverableLogin()
	if err != nil {
		return nil, nil, err
	}
	return marshalPair(assertion.Response, sess)
}

// FinishDiscoverableLogin verifies a passwordless assertion and returns the id of
// the user it authenticated. The handler maps the authenticator-returned user
// handle back to that user's stored credentials; validation then confirms the
// signature was made by one of them. The credential's post-login state (sign
// count) is persisted for clone detection.
func (s *Service) FinishDiscoverableLogin(ctx context.Context, session, response []byte) (userID string, err error) {
	sess, err := unmarshalSession(session)
	if err != nil {
		return "", err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return "", err
	}
	handler := func(_, userHandle []byte) (webauthn.User, error) {
		uid := string(userHandle)
		creds, err := s.repo.credentialsFor(ctx, uid)
		if err != nil {
			return nil, err
		}
		if len(creds) == 0 {
			return nil, ErrNoCredential
		}
		return &user{id: uid, creds: creds}, nil
	}
	u, cred, err := s.w.ValidatePasskeyLogin(handler, *sess, parsed)
	if err != nil {
		return "", err
	}
	if err := s.repo.updateAfterLogin(ctx, cred); err != nil {
		return "", err
	}
	return string(u.WebAuthnID()), nil
}

// BeginLoginForUser starts a login scoped to a known user (the second-factor flow,
// after a password). It restricts the assertion to that user's credentials.
func (s *Service) BeginLoginForUser(ctx context.Context, id string) (options, session []byte, err error) {
	creds, err := s.repo.credentialsFor(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if len(creds) == 0 {
		return nil, nil, ErrNoCredential
	}
	assertion, sess, err := s.w.BeginLogin(&user{id: id, creds: creds})
	if err != nil {
		return nil, nil, err
	}
	return marshalPair(assertion.Response, sess)
}

// FinishLoginForUser verifies a second-factor assertion for a known user.
func (s *Service) FinishLoginForUser(ctx context.Context, id string, session, response []byte) error {
	sess, err := unmarshalSession(session)
	if err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return err
	}
	creds, err := s.repo.credentialsFor(ctx, id)
	if err != nil {
		return err
	}
	cred, err := s.w.ValidateLogin(&user{id: id, creds: creds}, *sess, parsed)
	if err != nil {
		return err
	}
	return s.repo.updateAfterLogin(ctx, cred)
}

// marshalPair marshals the WebAuthn options (the inner publicKey object the
// browser's parse*OptionsFromJSON expects) and the ceremony session to JSON.
func marshalPair(options any, session *webauthn.SessionData) (opt, sess []byte, err error) {
	opt, err = json.Marshal(options)
	if err != nil {
		return nil, nil, err
	}
	sess, err = json.Marshal(session)
	if err != nil {
		return nil, nil, err
	}
	return opt, sess, nil
}

// unmarshalSession restores a ceremony session the caller stashed.
func unmarshalSession(b []byte) (*webauthn.SessionData, error) {
	var s webauthn.SessionData
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
