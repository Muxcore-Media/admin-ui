// Package meshdial builds gRPC transport credentials for every admin-ui dial
// (core and peer modules) using the MuxCore mesh TLS environment conventions
// shared with core/sdk/go/module:
//
//   - MUXCORE_INSECURE_DISABLE_TLS=true (or 1) — plaintext; development only.
//     The deprecated aliases MUXCORE_DEV_TLS_SKIP and ADMIN_UI_INSECURE are
//     honoured with a deprecation warning.
//   - MUXCORE_TLS_CA   — PEM CA bundle used to verify the peer (system roots
//     when unset).
//   - MUXCORE_TLS_CERT / MUXCORE_TLS_KEY — optional client certificate for
//     mTLS; both or neither.
//
// TLS is the default (NFR-SEC-003): no environment means TLS with system roots.
package meshdial

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Environment variable names (shared with core/sdk/go/module).
const (
	EnvInsecure       = "MUXCORE_INSECURE_DISABLE_TLS"
	EnvInsecureLegacy = "MUXCORE_DEV_TLS_SKIP"
	EnvAdminInsecure  = "ADMIN_UI_INSECURE"
	EnvTLSCA          = "MUXCORE_TLS_CA"
	EnvTLSCert        = "MUXCORE_TLS_CERT"
	EnvTLSKey         = "MUXCORE_TLS_KEY"
)

// Config selects the transport security for mesh dials.
type Config struct {
	Insecure bool
	// InsecureSource names the env var that enabled insecure mode (for logs).
	InsecureSource string
	CAFile         string
	CertFile       string
	KeyFile        string
}

func truthy(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	return v == "true" || v == "1"
}

// ConfigFromEnv reads the mesh TLS configuration from the process environment.
func ConfigFromEnv() Config {
	cfg := Config{
		CAFile:   strings.TrimSpace(os.Getenv(EnvTLSCA)),
		CertFile: strings.TrimSpace(os.Getenv(EnvTLSCert)),
		KeyFile:  strings.TrimSpace(os.Getenv(EnvTLSKey)),
	}
	for _, k := range []string{EnvInsecure, EnvInsecureLegacy, EnvAdminInsecure} {
		if truthy(os.Getenv(k)) {
			cfg.Insecure = true
			cfg.InsecureSource = k
			break
		}
	}
	return cfg
}

// TransportCredentials returns the gRPC transport credentials for cfg.
func TransportCredentials(cfg Config) (credentials.TransportCredentials, error) {
	if cfg.Insecure {
		return insecure.NewCredentials(), nil
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return nil, fmt.Errorf("meshdial: %s and %s must be set together", EnvTLSCert, EnvTLSKey)
	}
	if cfg.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("meshdial: load client cert/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	if cfg.CAFile != "" {
		pemBytes, err := os.ReadFile(cfg.CAFile) //nolint:gosec // operator-configured path
		if err != nil {
			return nil, fmt.Errorf("meshdial: read %s: %w", EnvTLSCA, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("meshdial: no certificates found in %s=%q", EnvTLSCA, cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	return credentials.NewTLS(tlsCfg), nil
}

var (
	defaultOnce  sync.Once
	defaultCfg   Config
	defaultCreds credentials.TransportCredentials
	defaultErr   error
)

func loadDefault() {
	defaultOnce.Do(func() {
		defaultCfg = ConfigFromEnv()
		defaultCreds, defaultErr = TransportCredentials(defaultCfg)
	})
}

// Default returns the process-wide credentials derived from the environment
// (computed once).
func Default() (credentials.TransportCredentials, error) {
	loadDefault()
	return defaultCreds, defaultErr
}

// DialOption returns grpc.WithTransportCredentials for the process-wide config.
func DialOption() (grpc.DialOption, error) {
	creds, err := Default()
	if err != nil {
		return nil, err
	}
	return grpc.WithTransportCredentials(creds), nil
}

// NewClient is grpc.NewClient with the mesh transport credentials applied.
func NewClient(addr string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	opt, err := DialOption()
	if err != nil {
		return nil, err
	}
	return grpc.NewClient(addr, append([]grpc.DialOption{opt}, opts...)...)
}

// LogStartup logs the effective mesh transport mode once at startup. Insecure
// mode emits a single loud warning (NFR-SEC-003: dev profile only).
func LogStartup(logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	loadDefault()
	return logMode(logger, defaultCfg, defaultErr)
}

func logMode(logger *slog.Logger, cfg Config, err error) error {
	if err != nil {
		logger.Error("mesh TLS misconfigured; gRPC dials will fail", "error", err)
		return err
	}
	if cfg.Insecure {
		if cfg.InsecureSource != EnvInsecure {
			logger.Warn("deprecated insecure flag; use "+EnvInsecure+"=true", "env", cfg.InsecureSource)
		}
		logger.Warn("!!! INSECURE: mesh gRPC TLS is DISABLED — plaintext traffic, development profile only; never use in production !!!",
			"env", cfg.InsecureSource)
		return nil
	}
	logger.Info("mesh gRPC TLS enabled",
		"ca", orDefault(cfg.CAFile, "system roots"),
		"client_cert", cfg.CertFile != "")
	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
