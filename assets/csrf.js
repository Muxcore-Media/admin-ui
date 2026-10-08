// CSRF token injection for HTMX (and opt-in swapping of 4xx validation bodies).
// Reads the csrf-token cookie set by the server and adds it as
// the X-CSRF-Token header on every HTMX AJAX request.
(function() {
  function getCookie(name) {
    var match = document.cookie.match(new RegExp('(^| )' + name + '=([^;]+)'));
    return match ? decodeURIComponent(match[2]) : null;
  }

  function csrfToken() {
    return getCookie('csrf-token');
  }

  // htmx does not swap 4xx bodies by default. A handler that wants its error
  // body shown (for example an invalid form submission) opts in with this
  // response header; every other error response keeps the default behaviour.
  document.addEventListener('htmx:beforeSwap', function(evt) {
    var xhr = evt.detail.xhr;
    if (xhr && xhr.status >= 400 && xhr.status < 500 && xhr.getResponseHeader('X-Admin-Swap-Error') === '1') {
      evt.detail.shouldSwap = true;
      evt.detail.isError = false;
    }
  });

  document.addEventListener('htmx:configRequest', function(evt) {
    var token = csrfToken();
    if (token) {
      evt.detail.headers['X-CSRF-Token'] = token;
    }
  });
})();
