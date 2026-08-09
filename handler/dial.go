package handler

import (
	"net"
	"os"
	"strings"
)

// normalizeDialAddr maps discovery HttpAddr (:port) to a dialable target.
// When MUXCORE_MESH_DIAL_LOCAL=true (host MVP), bare ports become 127.0.0.1:port.
// Explicit hosts are preserved. Otherwise bare ports use the module id as hostname (compose DNS).
func normalizeDialAddr(moduleID, httpAddr string) string {
	httpAddr = strings.TrimSpace(httpAddr)
	if httpAddr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(httpAddr)
	if err != nil {
		if strings.HasPrefix(httpAddr, ":") {
			port = strings.TrimPrefix(httpAddr, ":")
			host = ""
		} else {
			return httpAddr
		}
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return net.JoinHostPort(host, port)
	}
	if os.Getenv("MUXCORE_MESH_DIAL_LOCAL") == "true" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	if moduleID != "" {
		return net.JoinHostPort(moduleID, port)
	}
	return net.JoinHostPort("127.0.0.1", port)
}
