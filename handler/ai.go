package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capAIRuntime   = "ai.runtime"
	capAISubtitles = "ai.subtitles"
	capAIRecommend = "ai.recommend"
	capAITickets   = "ai.tickets"
	capAIFilter    = "ai.filter"
	capAILibrarian = "ai.librarian"
)

type aiModuleRow struct {
	Capability string
	Name       string
	Present    bool
	ID         string
}

func (h *Handler) AIPage(w http.ResponseWriter, r *http.Request) {
	rows := []aiModuleRow{
		{Capability: capAIRuntime, Name: "Runtime"},
		{Capability: capAISubtitles, Name: "Subtitles"},
		{Capability: capAIRecommend, Name: "Recommend"},
		{Capability: capAITickets, Name: "Tickets"},
		{Capability: capAIFilter, Name: "Filter"},
		{Capability: capAILibrarian, Name: "Librarian"},
	}
	if h.Core != nil && h.Core.Discovery != nil {
		ctx := r.Context()
		for i := range rows {
			mods, err := h.Core.Discovery.FindByCapability(ctx, rows[i].Capability)
			if err == nil && len(mods) > 0 {
				rows[i].Present = true
				rows[i].ID = mods[0].GetId()
			}
		}
	}
	var b strings.Builder
	b.WriteString(`<div class="space-y-6" data-testid="ai-page"><div><h1 class="text-2xl font-bold">AI modules</h1>`)
	b.WriteString(`<p class="text-gray-400 text-sm mt-1">Greenfield AI sidecars. Existing catalog/request/playback modules stay non-AI.</p></div>`)
	anyPresent := false
	for _, row := range rows {
		if row.Present {
			anyPresent = true
		}
	}
	if !anyPresent {
		b.WriteString(`<div class="rounded-xl border border-gray-800 bg-gray-900/50 p-4 text-sm text-gray-400" data-testid="ai-soft-empty">Soft OK without AI modules — enable MVP_ENABLE_AI=1 (or per-module flags) and register the sidecars.</div>`)
	}
	b.WriteString(`<ul class="rounded-xl border border-gray-800 divide-y divide-gray-800" data-testid="ai-module-list">`)
	for _, row := range rows {
		status := "missing"
		if row.Present {
			status = "registered"
		}
		fmt.Fprintf(&b, `<li class="px-4 py-3 text-sm flex justify-between"><span>%s <span class="text-gray-500">(%s)</span></span><span data-testid="ai-status-%s">%s</span></li>`, row.Name, row.Capability, row.Capability, status)
	}
	b.WriteString(`</ul></div>`)
	html := b.String()
	content := templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, html)
		return err
	})
	h.render(w, r, templates.Layout("AI modules", h.nav(r.URL.Path), content))
}
