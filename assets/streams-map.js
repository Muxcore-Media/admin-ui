(function () {
  var mapInstance = null;

  function destroyMap() {
    if (mapInstance) {
      mapInstance.remove();
      mapInstance = null;
    }
  }

  function loadLeaflet(cb) {
    if (window.L) {
      cb();
      return;
    }
    var link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = "https://unpkg.com/leaflet@1.9.4/dist/leaflet.css";
    document.head.appendChild(link);
    var script = document.createElement("script");
    script.src = "https://unpkg.com/leaflet@1.9.4/dist/leaflet.js";
    script.onload = cb;
    document.head.appendChild(script);
  }

  function popupHtml(pin) {
    var loc = [pin.city, pin.country].filter(Boolean).join(", ");
    return (
      "<strong>" +
      escapeHtml(pin.user || "Unknown") +
      "</strong><br>" +
      escapeHtml(pin.title || "Untitled") +
      "<br><span style=\"opacity:0.8\">" +
      escapeHtml(pin.state || "") +
      (loc ? " · " + escapeHtml(loc) : "") +
      "</span>"
    );
  }

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function initStreamMap() {
    var root = document.getElementById("stream-map-root");
    if (!root) {
      return;
    }
    var mapEl = document.getElementById("stream-map");
    if (!mapEl) {
      return;
    }

    loadLeaflet(function () {
      fetch("/streams/map/data", { credentials: "same-origin" })
        .then(function (res) {
          if (!res.ok) {
            throw new Error("map data unavailable");
          }
          return res.json();
        })
        .then(function (body) {
          var pins = (body && body.pins) || [];
          var status = document.getElementById("stream-map-status");
          if (status) {
            status.textContent =
              pins.length === 0
                ? "No geolocated active streams. Enable PLAYBACK_MONITOR_GEOIP=1 on playback-monitor."
                : pins.length + " active stream(s) on map";
          }
          destroyMap();
          if (pins.length === 0) {
            mapEl.innerHTML =
              '<div class="flex h-full items-center justify-center text-sm text-gray-500">Waiting for geolocated sessions…</div>';
            return;
          }

          var bounds = [];
          mapInstance = L.map(mapEl, { zoomControl: true }).setView([20, 0], 2);
          L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
            attribution: "&copy; OpenStreetMap",
            maxZoom: 18,
          }).addTo(mapInstance);

          pins.forEach(function (pin) {
            if (typeof pin.lat !== "number" || typeof pin.lon !== "number") {
              return;
            }
            var marker = L.circleMarker([pin.lat, pin.lon], {
              radius: 8,
              color: "#818cf8",
              fillColor: "#6366f1",
              fillOpacity: 0.85,
              weight: 2,
            }).addTo(mapInstance);
            marker.bindPopup(popupHtml(pin));
            bounds.push([pin.lat, pin.lon]);
          });

          if (bounds.length === 1) {
            mapInstance.setView(bounds[0], 5);
          } else if (bounds.length > 1) {
            mapInstance.fitBounds(bounds, { padding: [40, 40] });
          }
        })
        .catch(function () {
          var status = document.getElementById("stream-map-status");
          if (status) {
            status.textContent = "Could not load map data.";
          }
        });
    });
  }

  document.addEventListener("DOMContentLoaded", initStreamMap);
  document.body.addEventListener("htmx:afterSettle", initStreamMap);
})();
