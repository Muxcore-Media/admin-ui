package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	acquisitionDialTimeout = 3 * time.Second
	acquisitionReadTimeout = 5 * time.Second
	acquisitionPageTimeout = acquisitionDialTimeout + 2*acquisitionDialTimeout + 2*acquisitionReadTimeout + time.Second
)

func (h *Handler) AcquisitionHealthPage(w http.ResponseWriter, r *http.Request) {
	data := templates.AcquisitionHealthPageData{}

	if h.Core == nil {
		data.SoftNote = true
		h.renderAcquisitionHealth(w, r, data)
		return
	}

	pageCtx, pageCancel := context.WithTimeout(r.Context(), acquisitionPageTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, acquisitionDialTimeout)
	indexerMods, indexerErr := h.Core.Discovery.FindByCapability(dialCtx, capIndexer)
	dialCancel()
	if indexerErr != nil {
		slog.Warn("acquisition-health: indexer discovery failed", "error", indexerErr)
	}

	dialCtx, dialCancel = context.WithTimeout(pageCtx, acquisitionDialTimeout)
	downloaderMods, downloaderErr := h.Core.Discovery.FindByCapability(dialCtx, capDownloader)
	dialCancel()
	if downloaderErr != nil {
		slog.Warn("acquisition-health: downloader discovery failed", "error", downloaderErr)
	}

	if indexerErr != nil && downloaderErr != nil {
		data.SoftNote = true
		data.Error = acquisitionHealthResolveErr(indexerErr)
		h.renderAcquisitionHealth(w, r, data)
		return
	}

	data.Indexers = acquisitionPeerCards(pageCtx, h, indexerMods, "indexer")
	data.Downloaders = acquisitionPeerCards(pageCtx, h, downloaderMods, "downloader")
	data.Summary = acquisitionHealthSummary(data.Indexers, data.Downloaders)

	if len(data.Indexers) == 0 && len(data.Downloaders) == 0 {
		data.SoftNote = true
	}

	h.renderAcquisitionHealth(w, r, data)
}

func acquisitionHealthResolveErr(err error) string {
	if err == nil {
		return ""
	}
	if err == context.DeadlineExceeded {
		return fmt.Sprintf("acquisition peer discovery timed out after %s", acquisitionDialTimeout)
	}
	return err.Error()
}

func acquisitionPeerCards(ctx context.Context, h *Handler, mods []*discoveryv1.ModuleInfoProto, kind string) []templates.AcquisitionPeerCard {
	if len(mods) == 0 {
		return nil
	}
	out := make([]templates.AcquisitionPeerCard, 0, len(mods))
	for _, mod := range mods {
		card := templates.AcquisitionPeerCard{
			ID:           mod.GetId(),
			Name:         firstNonEmpty(mod.GetName(), mod.GetId()),
			Kind:         kind,
			Type:         acquisitionPeerType(mod, kind),
			Capabilities: mod.GetCapabilities(),
			Healthy:      mod.GetHealthError() == "",
			HealthDetail: mod.GetHealthError(),
		}

		readCtx, readCancel := context.WithTimeout(ctx, acquisitionReadTimeout)
		healthResp, healthErr := h.Core.Health.Check(readCtx, mod.GetId())
		readCancel()
		if healthErr != nil {
			slog.Warn("acquisition-health: Health.Check failed", "module", mod.GetId(), "error", healthErr)
			card.Healthy = false
			if card.HealthDetail == "" {
				card.HealthDetail = healthErr.Error()
			}
		} else if healthResp != nil {
			if healthResp.GetStatus() != healthv1.HealthCheckResponse_STATUS_HEALTHY {
				card.Healthy = false
			}
			if lastOk := acquisitionLastOkFromHealth(healthResp); lastOk != "" {
				card.LastOk = lastOk
			}
		}

		out = append(out, card)
	}
	return out
}

func acquisitionPeerType(mod *discoveryv1.ModuleInfoProto, kind string) string {
	for _, cap := range mod.GetCapabilities() {
		if cap != kind && cap != capIndexer && cap != capDownloader && !strings.HasPrefix(cap, "settings") {
			return cap
		}
	}
	for _, role := range mod.GetRoles() {
		if role != "" {
			return role
		}
	}
	return kind
}

func acquisitionLastOkFromHealth(resp *healthv1.HealthCheckResponse) string {
	load := resp.GetLoad()
	for _, key := range []string{"last_ok_at", "last_success_at", "last_ok", "last_success"} {
		if v, ok := load[key]; ok && v > 0 {
			return formatScannerUnixTime(int64(v))
		}
	}
	return ""
}

func acquisitionHealthSummary(indexers, downloaders []templates.AcquisitionPeerCard) templates.AcquisitionHealthSummary {
	sum := templates.AcquisitionHealthSummary{
		IndexerCount:    len(indexers),
		DownloaderCount: len(downloaders),
	}
	for _, p := range indexers {
		if p.Healthy {
			sum.HealthyIndexers++
		}
	}
	for _, p := range downloaders {
		if p.Healthy {
			sum.HealthyDownloaders++
		}
	}
	return sum
}

func (h *Handler) renderAcquisitionHealth(w http.ResponseWriter, r *http.Request, data templates.AcquisitionHealthPageData) {
	content := templates.AcquisitionHealthPage(data)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Acquisition Health", nav, content))
}
