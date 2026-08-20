package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	scannerv1 "github.com/Muxcore-Media/media-scanner/proto/scannerv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capMediaScanner = "media.scanner"

func (h *Handler) scannerModuleAddr(ctx context.Context) (string, error) {
	if h.Core == nil {
		return "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaScanner)
	if err != nil {
		return "", err
	}
	if len(mods) == 0 {
		return "", fmt.Errorf("no module with capability %s", capMediaScanner)
	}
	addr := normalizeDialAddr(mods[0].GetId(), mods[0].GetHttpAddr())
	if addr == "" {
		return "", fmt.Errorf("scanner module has no dial address")
	}
	return addr, nil
}

func (h *Handler) withScannerClient(ctx context.Context) (scannerv1.ScannerServiceClient, func(), error) {
	addr, err := h.scannerModuleAddr(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return scannerv1.NewScannerServiceClient(conn), func() { _ = conn.Close() }, nil
}

func (h *Handler) ManualImportPage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
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
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
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
