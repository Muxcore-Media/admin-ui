package handler

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	"google.golang.org/grpc"
)

type capDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	mods map[string]*discoveryv1.ModuleInfoProto
}

func (d capDiscovery) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if mod, ok := d.mods[req.GetCapability()]; ok {
		return &discoveryv1.FindByCapabilityResponse{Modules: []*discoveryv1.ModuleInfoProto{mod}}, nil
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

type usersAuthStub struct {
	authv1.UnimplementedAuthServiceServer
	created  string
	password bool
	roles    []string
}

func (s *usersAuthStub) CreateUser(_ context.Context, req *authv1.CreateUserRequest) (*authv1.CreateUserResponse, error) {
	s.created = req.GetUsername()
	return &authv1.CreateUserResponse{UserId: "user_new"}, nil
}

func (s *usersAuthStub) SetPassword(_ context.Context, req *authv1.SetPasswordRequest) (*authv1.SetPasswordResponse, error) {
	if req.GetPassword() != "" {
		s.password = true
	}
	return &authv1.SetPasswordResponse{}, nil
}

func (s *usersAuthStub) SetRoles(_ context.Context, req *authv1.SetRolesRequest) (*authv1.SetRolesResponse, error) {
	s.roles = append([]string(nil), req.GetRoles()...)
	return &authv1.SetRolesResponse{}, nil
}

func setupUsersHandler(t *testing.T, stub *usersAuthStub) *Handler {
	t.Helper()
	authLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	authSrv := grpc.NewServer()
	authv1.RegisterAuthServiceServer(authSrv, stub)
	go func() { _ = authSrv.Serve(authLis) }()
	t.Cleanup(func() {
		authSrv.Stop()
		_ = authLis.Close()
	})

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, capDiscovery{
		mods: map[string]*discoveryv1.ModuleInfoProto{
			"auth": {Id: "auth-local", HttpAddr: authLis.Addr().String()},
		},
	})
	go func() { _ = discSrv.Serve(discLis) }()
	t.Cleanup(func() {
		discSrv.Stop()
		_ = discLis.Close()
	})

	core, err := client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)
	h.Core = core
	return h
}

func TestUsersCreateCallsAuth(t *testing.T) {
	stub := &usersAuthStub{}
	h := setupUsersHandler(t, stub)

	body := strings.NewReader("username=bob&password=secret")
	r := httptest.NewRequest(http.MethodPost, "/users/create", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.UsersCreate(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if stub.created != "bob" {
		t.Fatalf("expected create bob, got %q", stub.created)
	}
	if w.Header().Get("HX-Redirect") != "/users" {
		t.Fatalf("expected redirect header, got %q", w.Header().Get("HX-Redirect"))
	}
}

func TestUsersSetPasswordCallsAuth(t *testing.T) {
	stub := &usersAuthStub{}
	h := setupUsersHandler(t, stub)

	body := strings.NewReader("password=newpass")
	r := httptest.NewRequest(http.MethodPost, "/users/u1/password", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", "u1")
	w := httptest.NewRecorder()
	h.UsersSetPassword(w, r)

	if !stub.password {
		t.Fatal("expected SetPassword called")
	}
}

func TestUsersSetRolesCallsAuth(t *testing.T) {
	stub := &usersAuthStub{}
	h := setupUsersHandler(t, stub)

	body := strings.NewReader("roles=admin&roles=viewer")
	r := httptest.NewRequest(http.MethodPost, "/users/u1/roles", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", "u1")
	w := httptest.NewRecorder()
	h.UsersSetRoles(w, r)

	if len(stub.roles) != 2 || stub.roles[0] != "admin" {
		t.Fatalf("roles=%v", stub.roles)
	}
}

func TestNetworkingSaveAppliesRuntime(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	body := strings.NewReader("public_url=https://admin.example&trusted_proxies=10.0.0.0/8&published_hosts=&http_port=&https_port=")
	r := httptest.NewRequest(http.MethodPost, "/networking", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.NetworkingSave(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if h.PublicURL != "https://admin.example" {
		t.Fatalf("PublicURL=%q", h.PublicURL)
	}
	if len(h.TrustedProxies) == 0 {
		t.Fatal("expected trusted proxies applied")
	}
}

func TestBrandingSavePersistsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	body := strings.NewReader("server_name=Household&login_banner=hi&custom_css=&splash_url=")
	r := httptest.NewRequest(http.MethodPost, "/branding", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.BrandingSave(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	b := loadBranding()
	if b.ServerName != "Household" {
		t.Fatalf("branding=%+v", b)
	}
}

func TestSyncParentalToUserdata(t *testing.T) {
	var gotPut bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(userdataBlob{Progress: map[string]json.RawMessage{}, Favorites: map[string]json.RawMessage{}})
		case http.MethodPut:
			gotPut = true
			var blob userdataBlob
			_ = json.NewDecoder(r.Body).Decode(&blob)
			var prefs map[string]json.RawMessage
			_ = json.Unmarshal(blob.Prefs, &prefs)
			var p parentalSettings
			_ = json.Unmarshal(prefs["parental"], &p)
			if p.BlockedTags != "horror" {
				t.Fatalf("parental=%+v", p)
			}
			_ = json.NewEncoder(w).Encode(blob)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.UserdataURL = srv.URL

	err := h.syncParentalToUserdata(context.Background(), "kid1", parentalSettings{BlockedTags: "horror"})
	if err != nil {
		t.Fatal(err)
	}
	if !gotPut {
		t.Fatal("expected userdata PUT")
	}
}
