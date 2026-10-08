package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"github.com/Muxcore-Media/userdata-local/parental"
)

const (
	testAdminBearer = "admin-bearer"
	testTenant      = "tenant-1"
)

// capturedRequest is what the fake provider saw.
type capturedRequest struct {
	Method   string
	Path     string
	RawQuery string
	Auth     []string
	UserIDs  []string
	Header   http.Header
	Body     []byte
}

// fakePolicyProvider is an httptest stand-in for userdata-local v0.1.5. It
// validates PUT bodies with the provider's own parental.DecodeUpdate, enforces
// the revision compare-and-set, and also serves /api/userdata so a test can
// prove nothing is written to the writable blob.
type fakePolicyProvider struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	docs     map[string]parental.Document
	known    map[string]bool // accounts the provider can resolve; others get 404
	reqs     []capturedRequest
	force    map[string]int    // "GET u1" -> forced status
	raw      map[string]string // "GET u1" -> forced 200 body
	raceOnce map[string]bool   // "u1": next PUT loses a race (409 after another writer)
	blob     json.RawMessage   // last PUT /api/userdata body
	blobPuts int
	onPut    func() // called at the start of every policy PUT
}

func newFakePolicyProvider(t *testing.T, users ...string) *fakePolicyProvider {
	t.Helper()
	f := &fakePolicyProvider{
		t:        t,
		docs:     map[string]parental.Document{},
		known:    map[string]bool{},
		force:    map[string]int{},
		raw:      map[string]string{},
		raceOnce: map[string]bool{},
	}
	for _, u := range users {
		f.known[u] = true
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePolicyProvider) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == userdataHTTPPath {
		f.serveBlob(w, r, body)
		return
	}
	userIDs := r.Header.Values(muxcoreUserIDHeader)
	f.reqs = append(f.reqs, capturedRequest{
		Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
		Auth: r.Header.Values("Authorization"), UserIDs: userIDs, Header: r.Header.Clone(), Body: body,
	})
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path != parentalPolicyPath {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	user := ""
	if len(userIDs) == 1 {
		user = userIDs[0]
	}
	key := r.Method + " " + user
	if r.Method == http.MethodPut && f.onPut != nil {
		f.onPut()
	}
	if st := f.force[key]; st != 0 {
		w.WriteHeader(st)
		_, _ = w.Write([]byte(`{"code":"policy.forced"}`))
		return
	}
	if len(r.Header.Values("Authorization")) != 1 || r.Header.Get("Authorization") != "Bearer "+testAdminBearer {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"policy.unauthenticated"}`))
		return
	}
	if !f.known[user] {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"policy.account_not_found"}`))
		return
	}
	if raw, ok := f.raw[key]; ok {
		_, _ = w.Write([]byte(raw))
		return
	}
	doc, ok := f.docs[user]
	if !ok {
		doc = parental.Unconfigured(parental.Scope{UserID: user, TenantID: testTenant})
	}
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(doc)
	case http.MethodPut:
		update, err := parental.DecodeUpdate(body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"policy.invalid_body"}`))
			return
		}
		if f.raceOnce[user] {
			delete(f.raceOnce, user)
			other := parental.Policy{Version: 1, Mode: "unrestricted"}
			f.docs[user] = parental.Document{
				Scope: doc.Scope, State: "configured", Revision: doc.Revision + 1, Policy: &other,
			}
			doc = f.docs[user]
		}
		if update.ExpectedRevision != doc.Revision {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"policy.revision_conflict"}`))
			return
		}
		pol := update.Policy
		out := parental.Document{Scope: doc.Scope, State: "configured", Revision: doc.Revision + 1, Policy: &pol, UpdatedAt: "2026-10-08T00:00:00Z"}
		f.docs[user] = out
		_ = json.NewEncoder(w).Encode(out)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakePolicyProvider) serveBlob(w http.ResponseWriter, r *http.Request, body []byte) {
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(userdataBlob{Progress: map[string]json.RawMessage{}, Favorites: map[string]json.RawMessage{}})
	case http.MethodPut:
		f.blobPuts++
		f.blob = append(json.RawMessage(nil), body...)
		_, _ = w.Write(body)
	}
}

// seed stores a configured policy for user.
func (f *fakePolicyProvider) seed(user string, rev int64, p parental.Policy) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.known[user] = true
	norm, err := parental.Normalize(p)
	if err != nil {
		f.t.Fatal(err)
	}
	f.docs[user] = parental.Document{
		Scope: parental.Scope{UserID: user, TenantID: testTenant}, State: "configured", Revision: rev, Policy: &norm,
	}
}

func (f *fakePolicyProvider) doc(user string) parental.Document {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.docs[user]; ok {
		return d
	}
	return parental.Unconfigured(parental.Scope{UserID: user, TenantID: testTenant})
}

func (f *fakePolicyProvider) requests() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]capturedRequest(nil), f.reqs...)
}

func (f *fakePolicyProvider) count(method, user string) int {
	n := 0
	for _, r := range f.requests() {
		if (method == "" || r.Method == method) && (user == "" || (len(r.UserIDs) == 1 && r.UserIDs[0] == user)) {
			n++
		}
	}
	return n
}

func (f *fakePolicyProvider) blobState() (json.RawMessage, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.blob, f.blobPuts
}

// assertCleanRequests proves every request used the admin's bearer and the
// target selector only: one Authorization header, one user header, no query,
// no browser-supplied identity.
func (f *fakePolicyProvider) assertCleanRequests(t *testing.T) {
	t.Helper()
	for _, r := range f.requests() {
		if len(r.Auth) != 1 || r.Auth[0] != "Bearer "+testAdminBearer {
			t.Errorf("%s %s Authorization = %v", r.Method, r.Path, r.Auth)
		}
		if len(r.UserIDs) != 1 || r.UserIDs[0] == "" {
			t.Errorf("%s %s target header = %v", r.Method, r.Path, r.UserIDs)
		}
		if r.RawQuery != "" {
			t.Errorf("unexpected query %q", r.RawQuery)
		}
		for _, h := range []string{"X-Auth-Token", "X-Tenant-Id", "Cookie"} {
			if r.Header.Get(h) != "" {
				t.Errorf("unexpected header %s=%q", h, r.Header.Get(h))
			}
		}
	}
}

type auditRecord struct {
	Actor, Action, Resource, ResourceID string
	Details                             map[string]string
}

type parentalEnv struct {
	h      *Handler
	prov   *fakePolicyProvider
	sess   *session.Session
	mu     sync.Mutex
	audits []auditRecord
	dir    string
}

func (e *parentalEnv) auditsFor(action string) []auditRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []auditRecord
	for _, a := range e.audits {
		if a.Action == action {
			out = append(out, a)
		}
	}
	return out
}

// newParentalEnv wires a handler to the identity stub (ListUsers) and the fake
// provider. users also become the provider's resolvable accounts.
func newParentalEnv(t *testing.T, users ...*authv1.UserInfo) *parentalEnv {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)
	t.Setenv("ADMIN_UI_PARENTAL_FILE", "")
	t.Setenv("ADMIN_UI_PARENTAL_MIGRATION_FILE", "")
	t.Setenv("ADMIN_UI_USERDATA_URL", "")
	ids := make([]string, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.GetId())
	}
	stub := &identityRPCStub{users: users}
	if users == nil {
		stub.users = []*authv1.UserInfo{}
	}
	h := setupIdentityHandler(t, stub)
	prov := newFakePolicyProvider(t, ids...)
	h.UserdataURL = prov.srv.URL
	e := &parentalEnv{
		h: h, prov: prov, dir: dir,
		sess: &session.Session{UserID: "admin1", Username: "admin", Roles: []string{"admin"}, AuthLocalToken: testAdminBearer, TenantID: testTenant},
	}
	h.AuditHook = func(actor, action, resource, id string, details map[string]string) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.audits = append(e.audits, auditRecord{actor, action, resource, id, details})
	}
	return e
}

func user(id, name string, roles ...string) *authv1.UserInfo {
	return &authv1.UserInfo{Id: id, Username: name, Roles: roles, TenantId: testTenant}
}

func (e *parentalEnv) request(method, target string, form url.Values, sess *session.Session, pathID string) *http.Request {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, target, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	// Browser-supplied identity must never reach the provider.
	r.Header.Set("Authorization", "Bearer browser-forged-token")
	r.Header.Set("X-Auth-Token", "browser-forged-token")
	r.Header.Set("X-Tenant-ID", "browser-tenant")
	if pathID != "" {
		r.SetPathValue("id", pathID)
	}
	if sess != nil {
		r = r.WithContext(context.WithValue(r.Context(), ctxSessionKey, sess))
	}
	return r
}

func (e *parentalEnv) getForm(userID string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.h.UsersParental(w, e.request(http.MethodGet, "/users/"+userID+"/parental", nil, e.sess, userID))
	return w
}

func (e *parentalEnv) postForm(userID string, form url.Values) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.h.UsersParental(w, e.request(http.MethodPost, "/users/"+userID+"/parental", form, e.sess, userID))
	return w
}

func (e *parentalEnv) postPIN(userID string, form url.Values) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.h.UsersParentalPIN(w, e.request(http.MethodPost, "/users/"+userID+"/parental/pin", form, e.sess, userID))
	return w
}

func (e *parentalEnv) migrate(form url.Values, sess *session.Session) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.h.ParentalMigrate(w, e.request(http.MethodPost, "/users/parental/migrate", form, sess, ""))
	return w
}

func restrictedPolicy(rules parental.Rules) parental.Policy {
	if rules.BlockedTags == nil {
		rules.BlockedTags = []string{}
	}
	if rules.AllowedTags == nil {
		rules.AllowedTags = []string{}
	}
	return parental.Policy{Version: 1, Mode: "restricted", Rules: &rules}
}

// loadParentalMap and saveParentalMap are test conveniences over the legacy file.
func loadParentalMap() map[string]parentalSettings {
	src, err := readParentalLegacy()
	if err != nil {
		return map[string]parentalSettings{}
	}
	return src.Entries
}

func saveParentalMap(m map[string]parentalSettings) error {
	parentalMu.Lock()
	defer parentalMu.Unlock()
	return writeFile0600(parentalFilePath(), m)
}
