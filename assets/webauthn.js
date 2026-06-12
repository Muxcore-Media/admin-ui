// WebAuthn / passkey registration helper.
// Called by HTMX-triggered UI elements. Reads creation options from a
// dedicated JSON element, calls navigator.credentials.create(), then POSTs
// the resulting credential back to the server to complete registration.
(function() {
  document.addEventListener('webauthn:register', function(evt) {
    var container = evt.detail.container || document.body;
    var optsEl = container.querySelector('[data-webauthn-options]');
    if (!optsEl) return;

    try {
      var opts = JSON.parse(optsEl.getAttribute('data-webauthn-options'));
    } catch(e) {
      showError(container, 'Invalid registration options');
      return;
    }

    var completeUrl = optsEl.getAttribute('data-webauthn-complete-url');
    if (!completeUrl) {
      showError(container, 'Missing complete URL');
      return;
    }

    opts.publicKey.challenge = base64urlToArray(opts.publicKey.challenge);
    opts.publicKey.user.id = base64urlToArray(opts.publicKey.user.id);
    if (opts.publicKey.excludeCredentials) {
      opts.publicKey.excludeCredentials.forEach(function(c) {
        c.id = base64urlToArray(c.id);
      });
    }

    navigator.credentials.create(opts)
      .then(function(cred) {
        var body = JSON.stringify({
          id: cred.id,
          rawId: arrayToBase64url(new Uint8Array(cred.rawId)),
          type: cred.type,
          response: {
            clientDataJSON: arrayToBase64url(new Uint8Array(cred.response.clientDataJSON)),
            attestationObject: arrayToBase64url(new Uint8Array(cred.response.attestationObject)),
          },
        });

        fetch(completeUrl, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: body,
        })
        .then(function(r) { return r.json(); })
        .then(function(data) {
          if (data.status === 'registered') {
            // Trigger HTMX to refresh the passkey list
            var list = document.querySelector('[data-passkey-list]');
            if (list) list.innerHTML = '<span class="text-xs text-green-400">Passkey registered</span>';
            setTimeout(function() { htmx.trigger(list, 'load'); }, 500);
          } else {
            showError(container, data.error || 'Registration failed');
          }
        })
        .catch(function() { showError(container, 'Network error'); });
      })
      .catch(function(err) { showError(container, err.message || 'Browser passkey cancelled'); });
  });

  function showError(container, msg) {
    var el = container.querySelector('[data-webauthn-error]');
    if (el) el.textContent = msg;
  }

  function base64urlToArray(str) {
    str = str.replace(/-/g, '+').replace(/_/g, '/');
    while (str.length % 4) str += '=';
    return Uint8Array.from(atob(str), function(c) { return c.charCodeAt(0); }).buffer;
  }

  function arrayToBase64url(arr) {
    var binary = '';
    arr.forEach(function(b) { binary += String.fromCharCode(b); });
    return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
})();
