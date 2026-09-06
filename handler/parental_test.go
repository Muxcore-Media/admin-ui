package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

// --- helpers ---

func testAdminSession() *session.Session {
	return &session.Session{
		UserID:         "admin1",
		Username:       "admin",
		Roles:          []string{"admin"},
		AuthLocalToken: "test-auth-local-token",
	}
}

func mockUserdataServer(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != userdataHTTPPath {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get(muxcoreUserIDHeader) == "" {
			http.Error(w, "missing user header", http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(userdataBlob{
				Progress:  map[string]json.RawMessage{},
				Favorites: map[string]json.RawMessage{},
			})
		case http.MethodPut:
			var blob userdataBlob
			_ = json.NewDecoder(r.Body).Decode(&blob)
			_ = json.NewEncoder(w).Encode(blob)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func setupParentalHandler(t *testing.T) *Handler {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.UserdataURL = mockUserdataServer(t)
	return h
}

func postParental(t *testing.T, h *Handler, userID, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/users/"+userID+"/parental", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", userID)
	r = r.WithContext(context.WithValue(r.Context(), ctxSessionKey, testAdminSession()))
	w := httptest.NewRecorder()
	h.UsersParental(w, r)
	return w
}

func getParental(t *testing.T, h *Handler, userID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/users/"+userID+"/parental", nil)
	r.SetPathValue("id", userID)
	w := httptest.NewRecorder()
	h.UsersParental(w, r)
	return w
}

// --- PIN tests ---

func TestUsersParentalSavePIN(t *testing.T) {
	h := setupParentalHandler(t)

	w := postParental(t, h, "u1", "max_rating=PG-13&pin=1234")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	m := loadParentalMap()
	p, ok := m["u1"]
	if !ok {
		t.Fatal("expected parental entry for u1")
	}
	if p.PINHash == "" {
		t.Fatal("expected PINHash to be set after saving PIN")
	}
	want := hashParentalPIN("u1", "1234")
	if p.PINHash != want {
		t.Fatalf("PINHash mismatch: got %q, want %q", p.PINHash, want)
	}
}

func TestUsersParentalClearPIN(t *testing.T) {
	h := setupParentalHandler(t)

	// First set a PIN.
	postParental(t, h, "u2", "pin=5678")

	m := loadParentalMap()
	if m["u2"].PINHash == "" {
		t.Fatal("expected PIN set after first save")
	}

	// Now clear it.
	w := postParental(t, h, "u2", "clear_pin=1")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	m = loadParentalMap()
	if m["u2"].PINHash != "" {
		t.Fatalf("expected PINHash cleared, got %q", m["u2"].PINHash)
	}
}

func TestUsersParentalPINPreservedWhenNotChanged(t *testing.T) {
	h := setupParentalHandler(t)

	postParental(t, h, "u3", "pin=9999")
	before := loadParentalMap()["u3"].PINHash

	// Submit without providing a new PIN or clear_pin.
	postParental(t, h, "u3", "max_rating=TV-14")

	after := loadParentalMap()["u3"].PINHash
	if before != after {
		t.Fatalf("PIN should be preserved when no new PIN provided: before=%q after=%q", before, after)
	}
}

func TestUsersParentalPINTooShort(t *testing.T) {
	h := setupParentalHandler(t)

	w := postParental(t, h, "u4", "pin=12")
	body := w.Body.String()
	if !strings.Contains(body, "4") {
		t.Fatalf("expected error mentioning digit requirement, got: %s", body)
	}
	// PIN must not be saved on validation error.
	if m := loadParentalMap(); m["u4"].PINHash != "" {
		t.Fatal("PIN should not be stored after validation error")
	}
}

func TestUsersParentalPINTooLong(t *testing.T) {
	h := setupParentalHandler(t)

	w := postParental(t, h, "u5", "pin=1234567")
	body := w.Body.String()
	if !strings.Contains(body, "6") {
		t.Fatalf("expected error mentioning max digits, got: %s", body)
	}
}

func TestUsersParentalPINNonNumeric(t *testing.T) {
	h := setupParentalHandler(t)

	w := postParental(t, h, "u6", "pin=ab12")
	body := w.Body.String()
	if !strings.Contains(body, "digit") {
		t.Fatalf("expected digit-only error, got: %s", body)
	}
}

// --- Rating validation tests ---

func TestUsersParentalRatingAccepted(t *testing.T) {
	h := setupParentalHandler(t)

	for _, rating := range []string{"", "G", "PG", "PG-13", "R", "NC-17", "TV-14", "TV-MA"} {
		w := postParental(t, h, "v1", "max_rating="+rating)
		if w.Code != http.StatusOK {
			t.Fatalf("rating %q: expected 200, got %d: %s", rating, w.Code, w.Body.String())
		}
		body := w.Body.String()
		if strings.Contains(body, "invalid") || strings.Contains(body, "error") {
			t.Fatalf("rating %q: unexpected error in response: %s", rating, body)
		}
	}
}

func TestUsersParentalRatingInvalidCharacter(t *testing.T) {
	h := setupParentalHandler(t)

	for _, rating := range []string{"PG<13", "R@ted", "X/X"} {
		w := postParental(t, h, "v2", "max_rating="+rating)
		body := w.Body.String()
		if !strings.Contains(body, "invalid") {
			t.Fatalf("rating %q: expected validation error, got: %s", rating, body)
		}
	}
}

func TestUsersParentalRatingTooLong(t *testing.T) {
	h := setupParentalHandler(t)

	long := strings.Repeat("A", 21)
	w := postParental(t, h, "v3", "max_rating="+long)
	if !strings.Contains(w.Body.String(), "long") {
		t.Fatalf("expected 'too long' error, got: %s", w.Body.String())
	}
}

// --- Kids mode test ---

func TestUsersParentalKidsModeToggle(t *testing.T) {
	h := setupParentalHandler(t)

	// Enable kids mode.
	postParental(t, h, "k1", "kids_mode=1")
	if !loadParentalMap()["k1"].KidsMode {
		t.Fatal("expected KidsMode=true")
	}

	// Disable kids mode (omitting kids_mode means unchecked).
	postParental(t, h, "k1", "max_rating=")
	if loadParentalMap()["k1"].KidsMode {
		t.Fatal("expected KidsMode=false after unchecked submit")
	}
}

// --- GET renders form ---

func TestUsersParentalGETRendersForm(t *testing.T) {
	h := setupParentalHandler(t)

	// Pre-load a setting so we verify the form reflects stored state.
	postParental(t, h, "g1", "max_rating=PG&pin=1111&kids_mode=1")

	w := getParental(t, h, "g1")
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(body, "parental-form") {
		t.Fatal("expected parental-form in GET response")
	}
	if !strings.Contains(body, "PG") {
		t.Fatalf("expected max rating in form, got: %s", body)
	}
	// PIN hash must not be exposed in the HTML.
	pinHash := hashParentalPIN("g1", "1111")
	if strings.Contains(body, pinHash) {
		t.Fatal("PIN hash must not appear in rendered HTML")
	}
	// Kids mode checkbox should be checked.
	if !strings.Contains(body, `name="kids_mode"`) {
		t.Fatal("expected kids_mode checkbox in form")
	}
}

// --- Userdata unavailable (hard-fail) ---

func TestUsersParentalUserdataUnavailable(t *testing.T) {
	h := setupParentalHandler(t)
	// UserdataURL points to nowhere — sync must fail visibly.
	h.UserdataURL = "http://127.0.0.1:1" // refused port

	w := postParental(t, h, "ud1", "max_rating=PG-13&kids_mode=1")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error banner, got %d", w.Code)
	}
	// The settings must still be saved locally.
	if loadParentalMap()["ud1"].MaxParentalRating != "PG-13" {
		t.Fatal("settings must be saved locally even when userdata sync fails")
	}
	body := w.Body.String()
	if !strings.Contains(body, "userdata sync failed") {
		t.Fatalf("expected userdata sync error in response, got: %s", body)
	}
	if strings.Contains(body, "Parental settings saved") {
		t.Fatal("must not show saved banner when userdata sync fails")
	}
	if !strings.Contains(body, "parental-form") {
		t.Fatalf("expected parental-form in response, got: %s", body)
	}
}

// --- hashParentalPIN unit tests ---

func TestHashParentalPINDeterministic(t *testing.T) {
	h1 := hashParentalPIN("user1", "1234")
	h2 := hashParentalPIN("user1", "1234")
	if h1 != h2 {
		t.Fatal("hashParentalPIN must be deterministic")
	}
}

func TestHashParentalPINSalted(t *testing.T) {
	h1 := hashParentalPIN("user1", "1234")
	h2 := hashParentalPIN("user2", "1234")
	if h1 == h2 {
		t.Fatal("same PIN for different users must produce different hashes")
	}
}

// --- validateParentalRating unit tests ---

func TestValidateParentalRating(t *testing.T) {
	cases := []struct {
		rating string
		ok     bool
	}{
		{"", true},
		{"G", true},
		{"PG", true},
		{"PG-13", true},
		{"TV-MA", true},
		{"NC-17", true},
		{"R", true},
		{strings.Repeat("A", 21), false},
		{"PG<13", false},
		{"R@ted", false},
	}
	for _, tc := range cases {
		err := validateParentalRating(tc.rating)
		if tc.ok && err != nil {
			t.Errorf("rating %q: expected ok, got %v", tc.rating, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("rating %q: expected error, got nil", tc.rating)
		}
	}
}

// --- validateParentalPIN unit tests ---

func TestValidateParentalPIN(t *testing.T) {
	cases := []struct {
		pin string
		ok  bool
	}{
		{"1234", true},
		{"12345", true},
		{"123456", true},
		{"123", false},     // too short
		{"1234567", false}, // too long
		{"abcd", false},    // non-numeric
		{"12a4", false},    // mixed
	}
	for _, tc := range cases {
		err := validateParentalPIN(tc.pin)
		if tc.ok && err != nil {
			t.Errorf("pin %q: expected ok, got %v", tc.pin, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("pin %q: expected error, got nil", tc.pin)
		}
	}
}

// --- syncParentalToUserdata (extended) ---

func TestSyncParentalToUserdataWithKidsMode(t *testing.T) {
	var gotPut bool
	var gotPath string
	var gotUserHeader string
	var gotAuth string
	var gotSettings parentalSettings
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUserHeader = r.Header.Get(muxcoreUserIDHeader)
		gotAuth = r.Header.Get("Authorization")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(userdataBlob{
				Progress:  map[string]json.RawMessage{},
				Favorites: map[string]json.RawMessage{},
			})
		case http.MethodPut:
			gotPut = true
			var blob userdataBlob
			_ = json.NewDecoder(r.Body).Decode(&blob)
			var prefs map[string]json.RawMessage
			_ = json.Unmarshal(blob.Prefs, &prefs)
			_ = json.Unmarshal(prefs["parental"], &gotSettings)
			_ = json.NewEncoder(w).Encode(blob)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.UserdataURL = srv.URL

	settings := parentalSettings{
		MaxParentalRating: "PG",
		KidsMode:          true,
		PINHash:           hashParentalPIN("kid2", "0000"),
	}
	ctx := context.WithValue(context.Background(), ctxSessionKey, testAdminSession())
	err := h.syncParentalToUserdata(ctx, "kid2", settings)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != userdataHTTPPath {
		t.Fatalf("expected path %q, got %q", userdataHTTPPath, gotPath)
	}
	if gotUserHeader != "kid2" {
		t.Fatalf("expected X-MuxCore-User-Id kid2, got %q", gotUserHeader)
	}
	if gotAuth != "Bearer test-auth-local-token" {
		t.Fatalf("expected bearer token, got %q", gotAuth)
	}
	if !gotPut {
		t.Fatal("expected userdata PUT")
	}
	if !gotSettings.KidsMode {
		t.Fatalf("KidsMode not synced: %+v", gotSettings)
	}
	if gotSettings.MaxParentalRating != "PG" {
		t.Fatalf("MaxParentalRating not synced: %+v", gotSettings)
	}
}
