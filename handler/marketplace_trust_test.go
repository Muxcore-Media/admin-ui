package handler

import (
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	spoolv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/spool/v1"

	"github.com/Muxcore-Media/admin-ui/session"
)

func TestMarketplaceTrust_AddListRevoke(t *testing.T) {
	dir := t.TempDir()
	spoolTrustRoot = dir
	t.Setenv("ADMIN_UI_SPOOL_TRUST_DIR", dir)
	t.Setenv("MUXCORE_SPOOL_REQUIRE_SIGNATURE", "1")

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	hexKey := hex.EncodeToString(pub)

	form := url.Values{}
	form.Set("label", "release")
	form.Set("public_key", hexKey)
	r := httptest.NewRequest(http.MethodPost, "/marketplace/trust/keys", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.MarketplaceTrustAddKey(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("add status %d", w.Code)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "keys"))
	if len(entries) != 1 {
		t.Fatalf("expected 1 key file, got %d", len(entries))
	}
	envRaw, err := os.ReadFile(filepath.Join(dir, "spool-trust.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envRaw), "MUXCORE_SPOOL_TRUSTED_KEYS_DIR=") {
		t.Fatalf("env file missing trusted keys dir: %s", envRaw)
	}

	r = mustRequest("GET", "/marketplace/trust")
	w = httptest.NewRecorder()
	h.MarketplaceTrustPage(w, r)
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="marketplace-trust-page"`) {
		t.Fatal("missing trust page")
	}
	if !strings.Contains(body, `data-testid="sig-require-on"`) {
		t.Fatal("expected signature requirement on")
	}
	if !strings.Contains(body, "release") {
		t.Fatal("expected key label")
	}

	settings := url.Values{}
	settings.Set("require_signature", "1")
	settings.Set("allowed_publishers", "MuxCore")
	r = httptest.NewRequest(http.MethodPost, "/marketplace/trust/settings", strings.NewReader(settings.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1"
	w = httptest.NewRecorder()
	h.MarketplaceTrustSaveSettings(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("settings status %d", w.Code)
	}
	envRaw, _ = os.ReadFile(filepath.Join(dir, "spool-trust.env"))
	if !strings.Contains(string(envRaw), "MUXCORE_SPOOL_REQUIRE_SIGNATURE=1") {
		t.Fatalf("env: %s", envRaw)
	}
	if !strings.Contains(string(envRaw), "MUXCORE_SPOOL_ALLOWED_PUBLISHERS=MuxCore") {
		t.Fatalf("env publishers: %s", envRaw)
	}

	tstore := loadSpoolTrust()
	if len(tstore.Keys) != 1 {
		t.Fatalf("keys=%d", len(tstore.Keys))
	}
	id := tstore.Keys[0].ID
	r = httptest.NewRequest(http.MethodPost, "/marketplace/trust/keys/"+id+"/revoke", nil)
	r.SetPathValue("id", id)
	r.RemoteAddr = "127.0.0.1:1"
	w = httptest.NewRecorder()
	h.MarketplaceTrustRevokeKey(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("revoke status %d", w.Code)
	}
	if len(loadSpoolTrust().Keys) != 0 {
		t.Fatal("expected empty keys after revoke")
	}
}

func TestMarketplacePage_ShowsPublisher(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)
	h.Spool = &stubSpool{
		spools: []*spoolv1.SpoolInfo{{Url: "https://example.test/spool", Active: true}},
		tags: map[string][]*spoolv1.TagSummary{
			"https://example.test/spool": {{Name: "media", Description: "Media stack", Version: "1.0", ModuleCount: 1}},
		},
		fetch: map[string]*spoolv1.FetchTagResponse{
			"https://example.test/spool|media": {
				Name: "media",
				Modules: []*spoolv1.TagModuleProto{
					{Repo: "github.com/Muxcore-Media/request-media", Version: "v0.2.2", Checksum: "abcdef0123456789deadbeef", Publisher: "MuxCore", Required: true},
				},
			},
		},
	}
	r := mustRequest("GET", "/marketplace")
	w := httptest.NewRecorder()
	h.MarketplacePage(w, r)
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="marketplace-pin-publisher"`) || !strings.Contains(body, "MuxCore") {
		t.Fatalf("expected publisher on pin: %s", truncate(body, 900))
	}
	if !strings.Contains(body, `data-testid="marketplace-trust-link"`) {
		t.Fatal("expected trust link")
	}
}
