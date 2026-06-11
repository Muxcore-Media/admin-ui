package handler

import (
	"log/slog"
	"net/http"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) StoragePage(w http.ResponseWriter, r *http.Request) {
	modules, err := h.Core.Discovery.FindByCapability(r.Context(), "storage")
	if err != nil {
		slog.Warn("storage: FindByCapability failed", "error", err)
	}

	caps, err := h.Core.Storage.Capabilities(r.Context())
	if err != nil {
		slog.Warn("storage: Capabilities call failed", "error", err)
	}

	_ = caps // used for storage-level capabilities if needed

	var providers []templates.StorageProviderCard
	for _, mod := range modules {
		providers = append(providers, templates.StorageProviderCard{
			Name:         mod.GetName(),
			ID:           mod.GetId(),
			Capabilities: mod.GetCapabilities(),
			Healthy:      true,
		})
	}

	content := templates.StoragePage(providers)
	nav := templates.Nav(navLinks, "/storage")
	component := templates.Layout("Storage", nav, content)
	h.render(w, r, component)
}
