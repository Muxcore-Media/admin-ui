package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) ManualImportPage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), scannerPageTimeout)
	defer cancel()
	data := templates.ManualImportPageData{
		Flash: r.URL.Query().Get("status"),
		Error: r.URL.Query().Get("error"),
	}
	client, closer, err := h.withScannerClient(ctx)
	if err != nil {
		data.Error = err.Error()
	} else {
		defer closer()
		resp, err := client.ListImportCandidates(ctx, &scannerv1.ListImportCandidatesRequest{Limit: 100})
		if err != nil {
			data.Error = err.Error()
		} else {
			for _, c := range resp.GetCandidates() {
				data.Candidates = append(data.Candidates, templates.ImportCandidateRow{
					Path: c.GetPath(), Name: c.GetName(), Size: c.GetSize(),
				})
			}
		}
	}
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Manual import", nav, templates.ManualImportPage(data)))
}

func (h *Handler) ManualImportPost(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), scannerActionTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/import", http.StatusSeeOther)
		return
	}
	path := r.FormValue("path")
	client, closer, err := h.withScannerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/import?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: path})
	if err != nil {
		slog.Warn("manual import failed", "path", path, "error", err)
		http.Redirect(w, r, "/import?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("imported=%d skipped=%d found=%d", resp.GetFilesImported(), resp.GetFilesSkipped(), resp.GetFilesFound())
	http.Redirect(w, r, "/import?status="+url.QueryEscape(msg), http.StatusSeeOther)
}
