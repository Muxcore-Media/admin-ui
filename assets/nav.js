// Mobile sidebar toggle + preserve sidebar scroll on HTMX tab navigation.
(function() {
  var ACTIVE_CLASSES = ['bg-[var(--accent-color)]', 'text-black'];
  var INACTIVE_CLASSES = [
    'text-[var(--text-secondary)]',
    'hover:bg-[var(--bg-elevated-2)]',
    'hover:text-[var(--text-primary)]',
  ];

  function syncSidebarActive() {
    var path = window.location.pathname;
    document.querySelectorAll('#sidebar a[data-nav-link]').forEach(function(link) {
      var active = link.getAttribute('href') === path;
      ACTIVE_CLASSES.forEach(function(cls) {
        link.classList.toggle(cls, active);
      });
      INACTIVE_CLASSES.forEach(function(cls) {
        link.classList.toggle(cls, !active);
      });
      if (active) {
        link.setAttribute('aria-current', 'page');
      } else {
        link.removeAttribute('aria-current');
      }
    });
  }

  function init() {
    var sidebar = document.getElementById('sidebar');
    var overlay = document.getElementById('sidebar-overlay');
    if (!sidebar || !overlay) return;

    // Create hamburger button
    var btn = document.createElement('button');
    btn.className = 'fixed top-4 left-4 z-50 lg:hidden text-gray-300 hover:text-white p-2 rounded-full bg-gray-900 border border-gray-700 shadow-lg';
    btn.setAttribute('aria-label', 'Toggle navigation');
    btn.innerHTML = '<svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16"/></svg>';
    document.body.appendChild(btn);

    btn.addEventListener('click', function() {
      sidebar.classList.toggle('-translate-x-full');
      overlay.classList.toggle('hidden');
    });

    overlay.addEventListener('click', function() {
      sidebar.classList.add('-translate-x-full');
      overlay.classList.add('hidden');
    });

    document.addEventListener('htmx:afterSettle', function(evt) {
      var target = evt.detail && evt.detail.target;
      if (target && target.id === 'main-content') {
        syncSidebarActive();
      }

      // Close sidebar on mobile after navigation
      if (window.innerWidth < 1024) {
        sidebar.classList.add('-translate-x-full');
        overlay.classList.add('hidden');
      }
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
