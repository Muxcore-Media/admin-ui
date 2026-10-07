// Keep navigation usable with a keyboard, including HTMX page changes.
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

    var desktop = window.matchMedia('(min-width: 1024px)');
    var open = false;
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'fixed top-4 left-4 z-50 lg:hidden text-gray-300 hover:text-white p-2 rounded-full bg-gray-900 border border-gray-700 shadow-lg';
    btn.setAttribute('aria-controls', 'sidebar');
    btn.innerHTML = '<svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16"/></svg>';
    document.body.appendChild(btn);

    function sidebarControls() {
      return Array.from(sidebar.querySelectorAll('a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex="0"]'))
        .filter(function(el) { return !el.closest('[hidden], [inert], [aria-hidden="true"]'); });
    }

    function syncMenu() {
      var visible = desktop.matches || open;
      sidebar.classList.toggle('-translate-x-full', !open);
      sidebar.toggleAttribute('inert', !visible);
      sidebar.setAttribute('aria-hidden', visible ? 'false' : 'true');
      overlay.classList.toggle('hidden', !open);
      // The overlay is decorative; the named button is the keyboard control.
      overlay.setAttribute('aria-hidden', 'true');
      var main = document.getElementById('main-content');
      if (main) main.toggleAttribute('inert', open);
      btn.setAttribute('aria-expanded', open ? 'true' : 'false');
      btn.setAttribute('aria-label', open ? 'Close navigation menu' : 'Open navigation menu');
    }

    function closeMenu(restoreFocus) {
      open = false;
      syncMenu();
      if (restoreFocus) btn.focus();
    }

    btn.addEventListener('click', function() {
      if (open) {
        closeMenu(true);
      } else {
        open = true;
        syncMenu();
        var first = sidebarControls()[0];
        if (first) first.focus();
      }
    });

    overlay.addEventListener('click', function() { closeMenu(true); });

    document.addEventListener('keydown', function(evt) {
      if (!open) return;
      if (evt.key === 'Escape') {
        evt.preventDefault();
        closeMenu(true);
      } else if (evt.key === 'Tab') {
        // The toggle follows the sidebar in DOM order, and stays reachable.
        var controls = sidebarControls().concat(btn);
        var first = controls[0];
        var last = controls[controls.length - 1];
        var focused = document.activeElement;
        if (evt.shiftKey && (focused === first || !controls.includes(focused))) {
          evt.preventDefault();
          last.focus();
        } else if (!evt.shiftKey && (focused === last || !controls.includes(focused))) {
          evt.preventDefault();
          first.focus();
        }
      }
    });

    desktop.addEventListener('change', function() {
      var focused = document.activeElement;
      open = false;
      syncMenu();
      if (!desktop.matches && sidebar.contains(focused)) {
        btn.focus();
      } else if (desktop.matches && focused === btn) {
        var current = sidebar.querySelector('[aria-current="page"]') || sidebarControls()[0];
        if (current) current.focus();
      }
    });

    function focusPage() {
      closeMenu(false);
      syncSidebarActive();
      var main = document.getElementById('main-content');
      if (main) main.focus();
    }

    document.addEventListener('htmx:afterSettle', function(evt) {
      var target = evt.detail && evt.detail.target;
      // Polling and form fragments must not close the menu or steal focus.
      if (target && target.id === 'main-content') focusPage();
    });
    document.addEventListener('htmx:historyRestore', focusPage);
    syncMenu();
    syncSidebarActive();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
