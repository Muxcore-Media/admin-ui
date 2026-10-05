package adminui

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/meshid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"

	"github.com/Muxcore-Media/admin-ui/internal/meshdial"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestEnsureMeshIdentity_PassesConfig(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		wantID       string
		wantInsecure bool
	}{
		{"default id", map[string]string{}, "admin-ui", false},
		{"explicit id", map[string]string{"MUXCORE_MODULE_ID": "admin-ui-2"}, "admin-ui-2", false},
		{"dev insecure", map[string]string{"MUXCORE_INSECURE_DISABLE_TLS": "true"}, "admin-ui", true},
		{"legacy admin flag", map[string]string{"ADMIN_UI_INSECURE": "true"}, "admin-ui", true},
		{"legacy dev skip", map[string]string{"MUXCORE_DEV_TLS_SKIP": "1"}, "admin-ui", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got meshid.Config
			fake := func(_ context.Context, cfg meshid.Config) (meshid.Paths, error) {
				got = cfg
				return meshid.Paths{Cert: "/id/module.crt"}, nil
			}
			if _, err := ensureMeshIdentity(context.Background(), fake, envMap(tc.env), " core:9090 "); err != nil {
				t.Fatalf("ensureMeshIdentity: %v", err)
			}
			if got.ModuleID != tc.wantID || got.Insecure != tc.wantInsecure || got.GRPCAddr != "core:9090" {
				t.Errorf("config ModuleID=%q Insecure=%v GRPCAddr=%q, want %q %v core:9090",
					got.ModuleID, got.Insecure, got.GRPCAddr, tc.wantID, tc.wantInsecure)
			}
		})
	}
}

func TestEnsureMeshIdentity_ErrorNamesModule(t *testing.T) {
	fake := func(context.Context, meshid.Config) (meshid.Paths, error) {
		return meshid.Paths{}, meshid.ErrNoIdentity
	}
	_, err := ensureMeshIdentity(context.Background(), fake, envMap(nil), "core:9090")
	if !errors.Is(err, meshid.ErrNoIdentity) || !strings.Contains(err.Error(), `"admin-ui"`) {
		t.Fatalf("err = %v, want ErrNoIdentity naming admin-ui", err)
	}
}

// Household end to end with a fake core: the real meshid.Ensure enrolls over
// BootstrapRegister at the admin-ui core address, and meshdial's credentials
// (from the exported MUXCORE_TLS_*) present the enrolled certificate to a
// peer that requires one.
func TestEnsureMeshIdentity_EnrollsAndMeshdialUsesIt(t *testing.T) {
	ca := newTestCA(t)
	const token = "mct_2_admin-ui_fixture"
	coreAddr := startFakeCore(t, ca, token)

	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.crt")
	writeFile(t, caFile, ca.certPEM)
	idDir := filepath.Join(dir, "mesh-id")
	for k, v := range map[string]string{
		"MUXCORE_PROFILE":              "household",
		"MUXCORE_INSECURE_DISABLE_TLS": "",
		"MUXCORE_DEV_TLS_SKIP":         "",
		"ADMIN_UI_INSECURE":            "",
		"MUXCORE_MODULE_ID":            "admin-ui",
		"MUXCORE_GRPC_ADDR":            "", // admin-ui passes ADMIN_UI_CORE_ADDR
		"MUXCORE_BOOTSTRAP_TOKEN":      token,
		"MUXCORE_TLS_CA":               caFile,
		"MUXCORE_TLS_DIR":              idDir,
		"MUXCORE_TLS_CERT":             "",
		"MUXCORE_TLS_KEY":              "",
		"MUXCORE_TLS_SERVER_NAME":      "",
		"MUXCORE_ENROLL_DNS_NAMES":     "",
	} {
		t.Setenv(k, v)
	}

	p, err := ensureMeshIdentity(context.Background(), meshid.Ensure, os.Getenv, coreAddr)
	if err != nil {
		t.Fatalf("ensureMeshIdentity: %v", err)
	}
	if !p.Enrolled || p.Cert != filepath.Join(idDir, "module.crt") {
		t.Fatalf("paths = %+v, want enrolled identity in %s", p, idDir)
	}

	cfg := meshdial.ConfigFromEnv()
	if cfg.Insecure || cfg.CertFile != p.Cert || cfg.KeyFile != p.Key || cfg.CAFile != caFile {
		t.Fatalf("meshdial config %+v does not use the enrolled identity %+v", cfg, p)
	}
	creds, err := meshdial.TransportCredentials(cfg)
	if err != nil {
		t.Fatalf("TransportCredentials: %v", err)
	}
	peerAddr, seenCN := startTLSPeer(t, ca)
	conn, err := grpc.NewClient(peerAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("health check over mesh TLS: %v", err)
	}
	if cn := <-seenCN; cn != "admin-ui" {
		t.Fatalf("peer saw client CN %q, want admin-ui", cn)
	}
}

func TestEnsureMeshIdentity_HouseholdRefusesInsecure(t *testing.T) {
	t.Setenv("MUXCORE_PROFILE", "household")
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	_, err := ensureMeshIdentity(context.Background(), meshid.Ensure, os.Getenv, "core:9090")
	if !errors.Is(err, meshid.ErrInsecureInHousehold) {
		t.Fatalf("err = %v, want ErrInsecureInHousehold", err)
	}
}

// ---- fakes ----

type testCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
	pool    *x509.CertPool
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test core CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue signs pub for cn with loopback SANs (client + server auth).
func (ca *testCA) issue(t *testing.T, cn string, pub any) []byte {
	t.Helper()
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", cn},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func (ca *testCA) serverCert(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(ca.issue(t, cn, &key.PublicKey),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

type fakeCore struct {
	modulev1.UnimplementedModuleRegistrationServer
	t     *testing.T
	ca    *testCA
	token string
}

func (f *fakeCore) BootstrapRegister(_ context.Context, req *modulev1.BootstrapRegisterRequest) (*modulev1.BootstrapRegisterResponse, error) {
	if req.GetToken() != f.token {
		return &modulev1.BootstrapRegisterResponse{Error: "bad token"}, nil
	}
	block, _ := pem.Decode([]byte(req.GetCsrPem()))
	if block == nil {
		return &modulev1.BootstrapRegisterResponse{Error: "no CSR"}, nil
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return &modulev1.BootstrapRegisterResponse{Error: "bad CSR"}, nil
	}
	return &modulev1.BootstrapRegisterResponse{
		Accepted:   true,
		SignedCert: string(f.ca.issue(f.t, req.GetModuleId(), csr.PublicKey)),
	}, nil
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return lis
}

func startFakeCore(t *testing.T, ca *testCA, token string) string {
	t.Helper()
	lis := listenLoopback(t)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{ca.serverCert(t, "muxcored")},
		MinVersion:   tls.VersionTLS12,
	})))
	modulev1.RegisterModuleRegistrationServer(srv, &fakeCore{t: t, ca: ca, token: token})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// startTLSPeer is a module gRPC server that requires a client certificate
// from the core CA and reports the client CN it saw.
func startTLSPeer(t *testing.T, ca *testCA) (string, <-chan string) {
	t.Helper()
	seen := make(chan string, 1)
	lis := listenLoopback(t)
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{ca.serverCert(t, "media-movies")},
			ClientCAs:    ca.pool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			MinVersion:   tls.VersionTLS12,
		})),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			if p, ok := peer.FromContext(ctx); ok {
				if ti, ok := p.AuthInfo.(credentials.TLSInfo); ok && len(ti.State.PeerCertificates) > 0 {
					select {
					case seen <- ti.State.PeerCertificates[0].Subject.CommonName:
					default:
					}
				}
			}
			return h(ctx, req)
		}),
	)
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	_, port, _ := net.SplitHostPort(lis.Addr().String())
	return net.JoinHostPort("localhost", port), seen
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
