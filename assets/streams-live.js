(function () {
  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function renderActiveSessions(sessions) {
    var root = document.getElementById("streams-active-panel");
    if (!root) {
      return;
    }
    if (!sessions || sessions.length === 0) {
      root.innerHTML =
        '<p class="text-gray-500 text-sm">No active streams.</p>';
      return;
    }
    var rows = sessions
      .map(function (row) {
        return (
          "<tr class=\"hover:bg-gray-900/40\">" +
          "<td class=\"px-3 py-2\">" + escapeHtml(row.user) + "</td>" +
          "<td class=\"px-3 py-2\">" + escapeHtml(row.title) + "</td>" +
          "<td class=\"px-3 py-2\">" + escapeHtml(row.state) + "</td>" +
          "<td class=\"px-3 py-2 font-mono text-xs\">" + escapeHtml(row.position) + "</td>" +
          "<td class=\"px-3 py-2 text-gray-400\">" + escapeHtml(row.platform) + " / " + escapeHtml(row.player) + "</td>" +
          "<td class=\"px-3 py-2 font-mono text-xs text-gray-400\">" + escapeHtml(row.ip) + "</td>" +
          "</tr>"
        );
      })
      .join("");
    root.innerHTML =
      '<div class="overflow-x-auto rounded-xl border border-gray-800">' +
      '<table class="min-w-full text-sm">' +
      '<thead class="bg-gray-900/80 text-gray-400 text-left"><tr>' +
      "<th class=\"px-3 py-2\">User</th><th class=\"px-3 py-2\">Title</th><th class=\"px-3 py-2\">State</th>" +
      "<th class=\"px-3 py-2\">Progress</th><th class=\"px-3 py-2\">Device</th><th class=\"px-3 py-2\">IP</th>" +
      "</tr></thead><tbody class=\"divide-y divide-gray-800\" id=\"streams-active-body\">" +
      rows +
      "</tbody></table></div>";
  }

  function refreshActive() {
    fetch("/streams/active.json", { credentials: "same-origin" })
      .then(function (res) {
        if (!res.ok) {
          throw new Error("active json unavailable");
        }
        return res.json();
      })
      .then(function (body) {
        renderActiveSessions((body && body.sessions) || []);
        var badge = document.getElementById("streams-live-badge");
        if (badge && typeof body.count === "number") {
          badge.textContent = "Live · " + body.count + " active";
        }
      })
      .catch(function () {
        /* ignore transient errors */
      });
  }

  function connectLive() {
    if (!document.getElementById("streams-live-root")) {
      return;
    }
    if (typeof EventSource === "undefined") {
      return;
    }
    var es = new EventSource("/streams/events");
    es.addEventListener("session", function () {
      refreshActive();
    });
    es.onerror = function () {
      /* browser reconnects automatically */
    };
    refreshActive();
  }

  document.addEventListener("DOMContentLoaded", connectLive);
  document.body.addEventListener("htmx:afterSettle", connectLive);
})();
