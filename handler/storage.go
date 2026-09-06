package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	storageDialTimeout = 3 * time.Second
	storageReadTimeout = 5 * time.Second
	storagePageTimeout = storageDialTimeout + 2*storageReadTimeout + time.Second
)

func (h *Handler) StoragePage(w http.ResponseWriter, r *http.Request) {
	data := templates.StoragePageData{}

	if h.Core == nil {
		data.SoftNote = true
		h.renderStorage(w, r, data)
		return
	}

	pageCtx, pageCancel := context.WithTimeout(r.Context(), storagePageTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, storageDialTimeout)
	modules, discoverErr := h.Core.Discovery.FindByCapability(dialCtx, "storage")
	dialCancel()
	if discoverErr != nil {
		slog.Warn("storage: FindByCapability failed", "error", discoverErr)
	}
	if discoverErr != nil || len(modules) == 0 {
		data.SoftNote = true
		if discoverErr != nil {
			data.Error = discoverErr.Error()
		}
		h.renderStorage(w, r, data)
		return
	}

	var capsResp *storagev1.CapabilitiesResponse
	readCtx, readCancel := context.WithTimeout(pageCtx, storageReadTimeout)
	capsResp, capsErr := h.Core.Storage.Raw().Capabilities(readCtx, &storagev1.CapabilitiesRequest{})
	readCancel()
	if capsErr != nil {
		slog.Warn("storage: Capabilities call failed", "error", capsErr)
	} else if capsResp != nil {
		data.GlobalCaps = capsResp.GetCapabilities()
		agg := storageCapacityFromProto(capsResp.ProtoReflect())
		if agg.HasData {
			data.AggregatedCapacity = templates.StorageCapacityView{
				Used:  formatStorageCapacityBytes(agg.UsedBytes),
				Free:  formatStorageCapacityBytes(agg.FreeBytes),
				Total: formatStorageCapacityBytes(agg.TotalBytes),
			}
		}
	}

	providerCaps := map[string]storageByteCapacity{}
	if capsResp != nil {
		providerCaps = storageProviderCapacitiesFromProto(capsResp.ProtoReflect())
	}

	for _, mod := range modules {
		card := templates.StorageProviderCard{
			Name:         mod.GetName(),
			ID:           mod.GetId(),
			Capabilities: mod.GetCapabilities(),
			Healthy:      mod.GetHealthError() == "",
		}
		if cap, ok := providerCaps[mod.GetId()]; ok && cap.HasData {
			card.UsedBytes = formatStorageCapacityBytes(cap.UsedBytes)
			card.FreeBytes = formatStorageCapacityBytes(cap.FreeBytes)
			card.TotalBytes = formatStorageCapacityBytes(cap.TotalBytes)
			card.HasCapacity = true
		}

		readCtx, readCancel := context.WithTimeout(pageCtx, storageReadTimeout)
		healthResp, healthErr := h.Core.Health.Check(readCtx, mod.GetId())
		readCancel()
		if healthErr != nil {
			slog.Warn("storage: Health.Check failed", "module", mod.GetId(), "error", healthErr)
		} else if healthResp != nil {
			if healthResp.GetStatus() != healthv1.HealthCheckResponse_STATUS_HEALTHY {
				card.Healthy = false
			}
			if !card.HasCapacity {
				cap := storageCapacityFromHealth(healthResp)
				if cap.HasData {
					card.UsedBytes = formatStorageCapacityBytes(cap.UsedBytes)
					card.FreeBytes = formatStorageCapacityBytes(cap.FreeBytes)
					card.TotalBytes = formatStorageCapacityBytes(cap.TotalBytes)
					card.HasCapacity = true
				}
			}
		}

		data.Providers = append(data.Providers, card)
	}

	h.renderStorage(w, r, data)
}

func (h *Handler) renderStorage(w http.ResponseWriter, r *http.Request, data templates.StoragePageData) {
	content := templates.StoragePage(data)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Storage", nav, content)
	h.render(w, r, component)
}
