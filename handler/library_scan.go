package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) LibraryScanPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), scannerPageTimeout)
	defer pageCancel()

	data := templates.LibraryScanPageData{
		Flash: r.URL.Query().Get("ok"),
		Error: r.URL.Query().Get("error"),
	}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, scannerDialTimeout)
	client, closer, err := h.withScannerClient(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderLibraryScan(w, r, data)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, scannerReadTimeout)
	stats, err := client.GetStats(readCtx, &scannerv1.GetStatsRequest{})
	readCancel()
	if err != nil {
		data.Error = err.Error()
	} else if stats != nil {
		data.Status = scannerDisplayStatus(stats.GetLastScanStatus())
		data.LastScanAt = formatScannerUnixTime(stats.GetLastScanAt())
		data.WatchDirs = int(stats.GetWatchDirs())
		data.TotalImported = int(stats.GetTotalImported())
		data.LastFound = int(stats.GetLastScanFilesFound())
		data.LastImported = int(stats.GetLastScanFilesImported())
		data.LastSkipped = int(stats.GetLastScanFilesSkipped())
		data.LastError = stats.GetLastError()
		if data.Status == "failed" && data.LastError == "" {
			data.LastError = stats.GetLastScanStatus()
		}
	}

	h.renderLibraryScan(w, r, data)
}

func (h *Handler) LibraryScanPost(w http.ResponseWriter, r *http.Request) {
	actionCtx, actionCancel := context.WithTimeout(r.Context(), scannerActionTimeout)
	defer actionCancel()

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/library-scan?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}

	dialCtx, dialCancel := context.WithTimeout(actionCtx, scannerDialTimeout)
	client, closer, err := h.withScannerClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/library-scan?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	scanCtx, scanCancel := context.WithTimeout(actionCtx, scannerScanTimeout)
	defer scanCancel()

	scanType := strings.TrimSpace(r.FormValue("scan_type"))
	var msg string
	switch scanType {
	case "library_roots":
		resp, err := client.ScanLibraryRoots(scanCtx, &scannerv1.ScanLibraryRootsRequest{})
		if err != nil {
			slog.Warn("library scan: ScanLibraryRoots failed", "error", err)
			http.Redirect(w, r, "/library-scan?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		msg = fmt.Sprintf("library roots scan complete — found=%d imported=%d skipped=%d",
			resp.GetFilesFound(), resp.GetFilesImported(), resp.GetFilesSkipped())
	default:
		resp, err := client.Scan(scanCtx, &scannerv1.ScanRequest{})
		if err != nil {
			slog.Warn("library scan: Scan failed", "error", err)
			http.Redirect(w, r, "/library-scan?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		msg = fmt.Sprintf("watch scan complete — found=%d imported=%d skipped=%d",
			resp.GetFilesFound(), resp.GetFilesImported(), resp.GetFilesSkipped())
	}

	http.Redirect(w, r, "/library-scan?ok="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) renderLibraryScan(w http.ResponseWriter, r *http.Request, data templates.LibraryScanPageData) {
	content := templates.LibraryScanPage(data)
	nav := h.nav("/library-scan")
	h.render(w, r, templates.Layout("Library Scan", nav, content))
}
