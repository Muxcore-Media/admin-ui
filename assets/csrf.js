// CSRF token injection for HTMX.
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

  document.addEventListener('htmx:configRequest', function(evt) {
    var token = csrfToken();
    if (token) {
      evt.detail.headers['X-CSRF-Token'] = token;
    }
  });
})();
