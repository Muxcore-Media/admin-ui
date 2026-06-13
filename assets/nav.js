// Mobile sidebar toggle with backdrop overlay
// Uses event delegation on body so handlers survive HTMX body swaps.
(function() {
  function init() {
    var body = document.body;

    body.addEventListener('click', function(e) {
      var btn = e.target.closest('#sidebar-toggle');
      if (btn) {
        var sidebar = document.getElementById('sidebar');
        var overlay = document.getElementById('sidebar-overlay');
        if (sidebar && overlay) {
          sidebar.classList.toggle('-translate-x-full');
          overlay.classList.toggle('hidden');
        }
        return;
      }

      if (e.target.closest('#sidebar-overlay')) {
        var sidebar = document.getElementById('sidebar');
        var overlay = document.getElementById('sidebar-overlay');
        if (sidebar && overlay) {
          sidebar.classList.add('-translate-x-full');
          overlay.classList.add('hidden');
        }
      }
    });
  }

  // Search results visibility
  function initSearch() {
    var body = document.body;

    body.addEventListener('focusin', function(e) {
      var input = e.target.closest('#search-container input[name="q"]');
      if (!input) return;
      var results = document.getElementById('search-results');
      if (results && results.children.length > 0 && results.textContent.trim() !== '') {
        results.classList.remove('hidden');
      }
    });

    body.addEventListener('click', function(e) {
      var container = document.getElementById('search-container');
      var results = document.getElementById('search-results');
      if (container && results && !container.contains(e.target)) {
        results.classList.add('hidden');
      }
    });
  }

  // Watch for HTMX swaps to manage search results visibility
  function watchSearchResults() {
    var body = document.body;
    body.addEventListener('htmx:afterSettle', function() {
      var results = document.getElementById('search-results');
      var input = document.querySelector('#search-container input[name="q"]');
      if (results && input) {
        if (input.value.length >= 2 && results.children.length > 0 && results.textContent.trim() !== '') {
          results.classList.remove('hidden');
        }
      }
    });
  }

  function onReady() {
    init();
    initSearch();
    watchSearchResults();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', onReady);
  } else {
    onReady();
  }
})();
