package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	storageDialTimeout = 3 * time.Second
	storageReadTimeout = 5 * time.Second
	storagePageTimeout = storageDialTimeout + 2*storageReadTimeout + time.Second
)

func (h *Handler) StoragePage(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), storagePageTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, storageDialTimeout)
	modules, err := h.Core.Discovery.FindByCapability(dialCtx, "storage")
	dialCancel()
	if err != nil {
		slog.Warn("storage: FindByCapability failed", "error", err)
	}

	readCtx, readCancel := context.WithTimeout(pageCtx, storageReadTimeout)
	caps, err := h.Core.Storage.Capabilities(readCtx)
	readCancel()
	if err != nil {
		slog.Warn("storage: Capabilities call failed", "error", err)
	}

	var providers []templates.StorageProviderCard
	for _, mod := range modules {
		providers = append(providers, templates.StorageProviderCard{
			Name:         mod.GetName(),
			ID:           mod.GetId(),
			Capabilities: mod.GetCapabilities(),
			Healthy:      true,
		})
	}

	content := templates.StoragePage(providers, caps)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Storage", nav, content)
	h.render(w, r, component)
}
