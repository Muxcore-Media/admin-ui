package handler

import (
	"log/slog"
	"net"
	"os"
	"strings"
)

func parseTrustedProxiesCSV(csv string) []net.IPNet {
	if strings.TrimSpace(csv) == "" {
		return parseTrustedProxies(nil)
	}
	return parseTrustedProxies(strings.Split(csv, ","))
}

func parseTrustedProxies(cidrs []string) []net.IPNet {
	loopback := []net.IPNet{
		{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
		{IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)},
	}
	if len(cidrs) == 0 {
		return loopback
	}
	parsed := make([]net.IPNet, 0, len(cidrs)+1)
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			slog.Warn("ignoring invalid trusted proxy CIDR", "cidr", c, "error", err)
			continue
		}
		parsed = append(parsed, *n)
	}
	if len(parsed) == 0 {
		return loopback
	}
	parsed = append(parsed, net.IPNet{IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)})
	return parsed
}

// ApplyNetworkingRuntime updates in-process public URL and trusted proxies from saved settings.
func (h *Handler) ApplyNetworkingRuntime(publicURL, trustedCSV string) {
	h.PublicURL = strings.TrimRight(strings.TrimSpace(publicURL), "/")
	h.TrustedProxies = parseTrustedProxiesCSV(trustedCSV)
}

// HydrateNetworkingFromFile loads networking.json when process env did not set overrides.
func (h *Handler) HydrateNetworkingFromFile() {
	n := loadNetworking()
	if os.Getenv("ADMIN_UI_PUBLIC_URL") == "" && h.PublicURL == "" && n.PublicURL != "" {
		h.PublicURL = strings.TrimRight(strings.TrimSpace(n.PublicURL), "/")
	}
	if os.Getenv("ADMIN_UI_TRUSTED_PROXIES") == "" && n.TrustedProxies != "" {
		h.TrustedProxies = parseTrustedProxiesCSV(n.TrustedProxies)
	}
}
