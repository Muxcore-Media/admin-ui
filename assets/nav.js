// Mobile sidebar toggle with backdrop overlay
(function() {
  function init() {
    var sidebar = document.getElementById('sidebar');
    var overlay = document.getElementById('sidebar-overlay');
    if (!sidebar || !overlay) return;

    // Create hamburger button
    var btn = document.createElement('button');
    btn.className = 'fixed top-4 left-4 z-50 lg:hidden text-gray-400 hover:text-white p-2 rounded-lg bg-gray-900 border border-gray-800';
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

    // Close sidebar on HTMX navigation (after settle)
    document.addEventListener('htmx:afterSettle', function() {
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
