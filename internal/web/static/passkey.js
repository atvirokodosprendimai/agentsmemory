// Passkey bridge — the ONLY bespoke client JS in the app.
//
// datastar owns transport, state and the DOM; this file only crosses the boundary
// datastar cannot: the browser's WebAuthn API (navigator.credentials). The server
// patches the ceremony options into a signal, a `data-effect` calls one of these
// functions, and when the browser prompt resolves we report back by dispatching a
// window CustomEvent that a `data-on:` handler turns into the finish @post.
//
// We rely on the modern WebAuthn JSON helpers (parseCreationOptionsFromJSON /
// parseRequestOptionsFromJSON and PublicKeyCredential#toJSON), so there is no
// base64url plumbing here. The UI gates these buttons on that support, so a
// browser without them simply never calls in.
(function () {
  "use strict";

  // friendly turns a raw WebAuthn error into a short, human message. A dismissed
  // or timed-out prompt is the common, non-alarming case.
  function friendly(e) {
    if (e && (e.name === "NotAllowedError" || e.name === "AbortError")) {
      return "Passkey prompt was dismissed. Please try again.";
    }
    if (e && e.name === "InvalidStateError") {
      return "This device already has a passkey for your account.";
    }
    return "Passkey step failed. Please try again.";
  }

  // register runs the create ceremony (enrolment) and emits passkey-created with
  // the attestation as WebAuthn JSON on success.
  async function register(options) {
    try {
      const publicKey = PublicKeyCredential.parseCreationOptionsFromJSON(options);
      const cred = await navigator.credentials.create({ publicKey });
      window.dispatchEvent(new CustomEvent("passkey-created", { detail: cred.toJSON() }));
    } catch (e) {
      window.dispatchEvent(new CustomEvent("passkey-failed", { detail: friendly(e) }));
    }
  }

  // authenticate runs the get ceremony (login/second factor) and emits
  // passkey-asserted with the assertion as WebAuthn JSON on success.
  async function authenticate(options) {
    try {
      const publicKey = PublicKeyCredential.parseRequestOptionsFromJSON(options);
      const cred = await navigator.credentials.get({ publicKey });
      window.dispatchEvent(new CustomEvent("passkey-asserted", { detail: cred.toJSON() }));
    } catch (e) {
      window.dispatchEvent(new CustomEvent("passkey-failed", { detail: friendly(e) }));
    }
  }

  window.pkRegister = register;
  window.pkAuthenticate = authenticate;
})();
