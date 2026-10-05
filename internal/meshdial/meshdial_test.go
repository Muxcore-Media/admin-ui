package meshdial

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{EnvInsecure, EnvInsecureLegacy, EnvAdminInsecure, EnvTLSCA, EnvTLSCert, EnvTLSKey} {
		t.Setenv(k, "")
	}
}

func TestConfigFromEnvDefaultsToTLS(t *testing.T) {
	clearEnv(t)
	cfg := ConfigFromEnv()
	if cfg.Insecure {
		t.Fatal("expected TLS by default")
	}
	creds, err := TransportCredentials(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := creds.Info().SecurityProtocol; got != "tls" {
		t.Fatalf("SecurityProtocol = %q, want tls", got)
	}
}

func TestConfigFromEnvInsecureFlag(t *testing.T) {
	for _, k := range []string{EnvInsecure, EnvInsecureLegacy, EnvAdminInsecure} {
		t.Run(k, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(k, "true")
			cfg := ConfigFromEnv()
			if !cfg.Insecure || cfg.InsecureSource != k {
				t.Fatalf("cfg = %+v, want insecure from %s", cfg, k)
			}
			creds, err := TransportCredentials(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := creds.Info().SecurityProtocol; got != "insecure" {
				t.Fatalf("SecurityProtocol = %q, want insecure", got)
			}
		})
	}
	t.Run("false", func(t *testing.T) {
		clearEnv(t)
		t.Setenv(EnvInsecure, "false")
		if ConfigFromEnv().Insecure {
			t.Fatal("false must not enable insecure")
		}
	})
}

func TestLogModeInsecureWarnsLoudly(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	if err := logMode(logger, Config{Insecure: true, InsecureSource: EnvInsecure}, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "INSECURE") {
		t.Fatalf("expected loud warning, got %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("expected a single warning line, got %q", out)
	}
}

func TestTransportCredentialsErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := TransportCredentials(Config{CertFile: "x"}); err == nil {
		t.Fatal("expected error for cert without key")
	}
	if _, err := TransportCredentials(Config{CAFile: filepath.Join(dir, "missing.pem")}); err == nil {
		t.Fatal("expected error for missing CA")
	}
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := TransportCredentials(Config{CAFile: bad}); err == nil {
		t.Fatal("expected error for invalid CA PEM")
	}
}

type testPKI struct {
	caPEM      []byte
	serverCert tls.Certificate
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "muxcore test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return testPKI{
		caPEM:      pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		serverCert: tls.Certificate{Certificate: [][]byte{srvDER}, PrivateKey: srvKey},
	}
}

func startTLSHealthServer(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func healthCheck(t *testing.T, creds credentials.TransportCredentials, addr string) error {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}

func TestTLSWithTestCA(t *testing.T) {
	pki := newTestPKI(t)
	addr := startTLSHealthServer(t, pki.serverCert)

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pki.caPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	creds, err := TransportCredentials(Config{CAFile: caFile})
	if err != nil {
		t.Fatal(err)
	}
	if err := healthCheck(t, creds, addr); err != nil {
		t.Fatalf("TLS dial with test CA failed: %v", err)
	}

	// Without the CA the server certificate must be rejected (system roots).
	sysCreds, err := TransportCredentials(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := healthCheck(t, sysCreds, addr); err == nil {
		t.Fatal("expected verification failure without the test CA")
	}
}
