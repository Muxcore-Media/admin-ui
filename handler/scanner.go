package handler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Muxcore-Media/admin-ui/internal/meshdial"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
)

const (
	capMediaScanner = "media.scanner"

	scannerDialTimeout   = 3 * time.Second
	scannerReadTimeout   = 5 * time.Second
	scannerScanTimeout   = 60 * time.Second
	scannerPageTimeout   = scannerDialTimeout + scannerReadTimeout + time.Second
	scannerActionTimeout = scannerDialTimeout + scannerScanTimeout + time.Second
)

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
	conn, err := meshdial.NewClient(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return scannerv1.NewScannerServiceClient(conn), func() { _ = conn.Close() }, nil
}

func scannerDisplayStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "running":
		return "running"
	case "failed", "error":
		return "failed"
	case "completed", "idle", "":
		return "idle"
	default:
		return raw
	}
}

func formatScannerUnixTime(unix int64) string {
	if unix <= 0 {
		return "—"
	}
	return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04 UTC")
}
