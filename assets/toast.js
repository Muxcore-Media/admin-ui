// Toast notification system.
// Listens for HX-Trigger "show-toast" events from server responses
// and renders auto-dismissing toast notifications.
(function() {
  var TOAST_DURATION = 4000; // ms

  function createToast(type, message) {
    var container = document.getElementById('toast-container');
    if (!container) return;

    var colors = {
      success: { bg: 'bg-green-900/90', border: 'border-green-700', text: 'text-green-200', icon: '✓' },
      error:   { bg: 'bg-red-900/90',   border: 'border-red-700',   text: 'text-red-200',   icon: '✕' },
      warning: { bg: 'bg-yellow-900/90', border: 'border-yellow-700', text: 'text-yellow-200', icon: '!' },
      info:    { bg: 'bg-blue-900/90',  border: 'border-blue-700',  text: 'text-blue-200',  icon: 'i' },
    };

    var c = colors[type] || colors.info;
    var el = document.createElement('div');
    el.className = 'pointer-events-auto flex items-center gap-3 rounded-lg border ' + c.border + ' ' + c.bg + ' px-4 py-3 shadow-lg text-sm ' + c.text + ' transition-all duration-300 opacity-0 translate-x-4';
    el.innerHTML = '<span class="flex-shrink-0 w-5 h-5 rounded-full flex items-center justify-center text-xs font-bold ' + c.bg.replace('/90', '/50') + '">' + c.icon + '</span><span class="flex-1">' + escapeHtml(message) + '</span>';

    container.appendChild(el);

    // Animate in
    requestAnimationFrame(function() {
      el.classList.remove('opacity-0', 'translate-x-4');
    });

    // Auto-dismiss
    setTimeout(function() {
      el.classList.add('opacity-0', 'translate-x-4');
      setTimeout(function() { el.remove(); }, 300);
    }, TOAST_DURATION);
  }

  function escapeHtml(str) {
    var div = document.createElement('div');
    div.appendChild(document.createTextNode(str));
    return div.innerHTML;
  }

  // Listen for HTMX events
  document.addEventListener('htmx:beforeSwap', function(evt) {
    var header = evt.detail.xhr.getResponseHeader('HX-Trigger');
    if (!header) return;
    try {
      var data = JSON.parse(header);
      if (data['show-toast']) {
        var toast = data['show-toast'];
        createToast(toast.type || 'info', toast.message || '');
      }
    } catch(e) {}
  });
})();
