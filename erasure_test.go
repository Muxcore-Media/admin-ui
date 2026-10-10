package adminui

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"

	"github.com/Muxcore-Media/admin-ui/session"
)

type nopFinder struct{}

func (nopFinder) FindByCapability(context.Context, *discoveryv1.FindByCapabilityRequest, ...grpc.CallOption) (*discoveryv1.FindByCapabilityResponse, error) {
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func TestErasureHouseholdWithoutCoreAddressRefusesStartup(t *testing.T) {
	for _, profile := range []string{"household", "staging", " Household "} {
		t.Run(profile, func(t *testing.T) {
			rec, err := newErasureReconciler(erasureSetup{
				Getenv:   envMap(map[string]string{"MUXCORE_PROFILE": profile}),
				ModuleID: "admin-ui",
				Sessions: session.NewStore(time.Hour),
				Finder:   nil,
			})
			if err == nil || rec != nil {
				t.Fatalf("household without a core connection must refuse startup, got rec=%v err=%v", rec, err)
			}
			if !strings.Contains(err.Error(), "core connection") {
				t.Errorf("error does not name the missing core connection: %v", err)
			}
		})
	}
}

func TestErasureHouseholdWithoutCoreClientRefusesStartupEvenWithAddress(t *testing.T) {
	// The address is configured but the core client could not be built.
	rec, err := newErasureReconciler(erasureSetup{
		Getenv:   envMap(map[string]string{"MUXCORE_PROFILE": "household", "MUXCORE_GRPC_ADDR": "core:9090"}),
		ModuleID: "admin-ui",
		Sessions: session.NewStore(time.Hour),
	})
	if err == nil || rec != nil {
		t.Fatalf("expected refusal, got rec=%v err=%v", rec, err)
	}
}

func TestErasureHouseholdWithoutAddressRefusesEvenWithFinder(t *testing.T) {
	// A finder alone is not a configuration: no address, no reconciler.
	rec, err := newErasureReconciler(erasureSetup{
		Getenv:   envMap(map[string]string{"MUXCORE_PROFILE": "household"}),
		ModuleID: "admin-ui",
		Sessions: session.NewStore(time.Hour),
		Finder:   nopFinder{},
	})
	if err == nil || rec != nil {
		t.Fatalf("expected refusal, got rec=%v err=%v", rec, err)
	}
}

func TestErasureNonHouseholdWithoutCoreWarnsAndDisables(t *testing.T) {
	for _, profile := range []string{"", "dev"} {
		rec, err := newErasureReconciler(erasureSetup{
			Getenv:   envMap(map[string]string{"MUXCORE_PROFILE": profile}),
			ModuleID: "admin-ui",
			Sessions: session.NewStore(time.Hour),
		})
		if err != nil || rec != nil {
			t.Fatalf("profile %q: want disabled without error, got rec=%v err=%v", profile, rec, err)
		}
	}
}

func TestErasureCoreAddrAcceptsEitherVariable(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"admin-ui":  {"ADMIN_UI_CORE_ADDR": "core:9090"},
		"mesh-wide": {"MUXCORE_GRPC_ADDR": "core:9090"},
	} {
		if got := erasureCoreAddr(envMap(env)); got != "core:9090" {
			t.Errorf("%s: erasureCoreAddr = %q", name, got)
		}
	}
	if got := erasureCoreAddr(envMap(map[string]string{"ADMIN_UI_CORE_ADDR": "  "})); got != "" {
		t.Errorf("blank address treated as configured: %q", got)
	}
}

func TestMeshModuleIDDefaultsAndOverrides(t *testing.T) {
	if got := meshModuleID(envMap(nil)); got != "admin-ui" {
		t.Errorf("default = %q", got)
	}
	if got := meshModuleID(envMap(map[string]string{"MUXCORE_MODULE_ID": " admin-ui-2 "})); got != "admin-ui-2" {
		t.Errorf("override = %q", got)
	}
}

// ledgerAuth is a fake identity provider: it serves the ledger, the user
// directory, and records acknowledgements and the caller identity header.
type ledgerAuth struct {
	authv1.UnimplementedAuthServiceServer
	mu        sync.Mutex
	listed    int
	acks      []*authv1.AckUserErasureRequest
	acked     map[string]bool
	callerIDs []string
}

func (l *ledgerAuth) note(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	l.callerIDs = append(l.callerIDs, md.Get("x-caller-id")...)
}

func (l *ledgerAuth) ListUserErasures(ctx context.Context, _ *authv1.ListUserErasuresRequest) (*authv1.ListUserErasuresResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.note(ctx)
	l.listed++
	return &authv1.ListUserErasuresResponse{Erasures: []*authv1.UserErasure{{
		ErasureId: "er-e2e", UserId: "u-victim", DeletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		AcknowledgedByCaller: l.acked["er-e2e"],
	}}}, nil
}

func (l *ledgerAuth) AckUserErasure(ctx context.Context, req *authv1.AckUserErasureRequest) (*authv1.AckUserErasureResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.note(ctx)
	l.acks = append(l.acks, req)
	if l.acked == nil {
		l.acked = map[string]bool{}
	}
	l.acked[req.GetErasureId()] = true
	return &authv1.AckUserErasureResponse{}, nil
}

func (l *ledgerAuth) ListUsers(ctx context.Context, _ *authv1.ListUsersRequest) (*authv1.ListUsersResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.note(ctx)
	return &authv1.ListUsersResponse{Users: []*authv1.UserInfo{{Id: "u-admin", Username: "admin"}, {Id: "u-bob", Username: "bob"}}}, nil
}

type identityDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	addr string
}

func (d identityDiscovery) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() != "identity" {
		return &discoveryv1.FindByCapabilityResponse{}, nil
	}
	return &discoveryv1.FindByCapabilityResponse{Modules: []*discoveryv1.ModuleInfoProto{{Id: "auth-local", HttpAddr: d.addr}}}, nil
}

func serve(t *testing.T, register func(*grpc.Server)) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	return lis.Addr().String()
}

// TestErasureReconcilerAppliesLedgerThroughIdentityProvider drives the real
// SDK reconciler end to end against a fake identity provider: the tombstone
// comes only from the ledger of the discovered `identity` provider, admin-ui's
// stores are erased, and the acknowledgement carries the counts.
func TestErasureReconcilerAppliesLedgerThroughIdentityProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)
	for _, k := range []string{"ADMIN_UI_SESSION_FILE", "ADMIN_UI_PARENTAL_FILE", "ADMIN_UI_PASSWORD_RESET_FILE", "ADMIN_UI_ERASURE_APPLIED_FILE"} {
		t.Setenv(k, "")
	}
	// Plaintext mesh is only possible with the explicit dev flag and outside
	// the household profile (the SDK dialer enforces both).
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	t.Setenv("MUXCORE_PROFILE", "")

	sessions, err := session.NewFileStoreWithKey(filepath.Join(dir, "sessions.json"), time.Hour, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Create("u-victim", "victim", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Create("u-bob", "bob", nil, nil); err != nil {
		t.Fatal(err)
	}
	write := func(name string, v any) {
		raw, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("password-resets.json", map[string]any{"requests": []map[string]any{
		{"id": "r1", "username": "old-name", "user_id": "u-victim", "status": "pending"},
		{"id": "r2", "username": "bob", "status": "pending"},
	}})
	write("parental.json", map[string]any{"u-victim": map[string]any{"kids_mode": true}, "u-bob": map[string]any{}})

	ledger := &ledgerAuth{}
	authAddr := serve(t, func(s *grpc.Server) { authv1.RegisterAuthServiceServer(s, ledger) })
	discAddr := serve(t, func(s *grpc.Server) { discoveryv1.RegisterDiscoveryServiceServer(s, identityDiscovery{addr: authAddr}) })
	t.Setenv("ADMIN_UI_CORE_ADDR", discAddr)
	coreConn, err := grpc.NewClient(discAddr, grpc.WithTransportCredentials(insecureCreds()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coreConn.Close() })

	rec, err := newErasureReconciler(erasureSetup{
		Getenv:   os.Getenv,
		ModuleID: "admin-ui",
		Sessions: sessions,
		Finder:   discoveryv1.NewDiscoveryServiceClient(coreConn),
		Interval: time.Minute,
	})
	if err != nil || rec == nil {
		t.Fatalf("newErasureReconciler: rec=%v err=%v", rec, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := rec.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v (%+v)", err, res)
	}
	if res.Applied != 1 || res.Acked != 1 || res.Failed != 0 {
		t.Fatalf("sweep result = %+v", res)
	}

	ledger.mu.Lock()
	acks, callers := ledger.acks, ledger.callerIDs
	ledger.mu.Unlock()
	if len(acks) != 1 || acks[0].GetErasureId() != "er-e2e" || acks[0].GetOutcome() != authv1.ErasureOutcome_ERASURE_OUTCOME_OK {
		t.Fatalf("acks = %v", acks)
	}
	if c := acks[0].GetCounts(); c["sessions"] != 1 || c["password_resets"] != 1 || c["parental_entries"] != 1 {
		t.Errorf("ack counts = %v", c)
	}
	for _, id := range callers {
		if id != "admin-ui" {
			t.Errorf("provider saw caller %q, want admin-ui", id)
		}
	}

	if sessions.CountUser("u-victim", "") != 0 || sessions.CountUser("u-bob", "") != 1 {
		t.Errorf("sessions victim=%d bob=%d", sessions.CountUser("u-victim", ""), sessions.CountUser("u-bob", ""))
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "password-resets.json"))
	if strings.Contains(string(raw), "u-victim") || !strings.Contains(string(raw), `"r2"`) {
		t.Errorf("password-resets.json = %s", raw)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "parental.json"))
	if strings.Contains(string(raw), "u-victim") || !strings.Contains(string(raw), "u-bob") {
		t.Errorf("parental.json = %s", raw)
	}

	// A second sweep acknowledges nothing new and applies nothing.
	res2, err := rec.SweepOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Applied != 0 || res2.Skipped != 1 {
		t.Errorf("second sweep = %+v, want the erasure skipped", res2)
	}
	ledger.mu.Lock()
	n := len(ledger.acks)
	ledger.mu.Unlock()
	if n != 1 {
		t.Errorf("acknowledged %d times, want 1", n)
	}
}

func insecureCreds() credentials.TransportCredentials { return insecure.NewCredentials() }
