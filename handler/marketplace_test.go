package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	spoolv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/spool/v1"

	"github.com/Muxcore-Media/admin-ui/session"
)

type stubSpool struct {
	spools   []*spoolv1.SpoolInfo
	tags     map[string][]*spoolv1.TagSummary
	fetch    map[string]*spoolv1.FetchTagResponse
	deployed []string
	err      error
}

func (s *stubSpool) ListSpools(ctx context.Context) ([]*spoolv1.SpoolInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.spools, nil
}

func (s *stubSpool) ListTags(ctx context.Context, spoolURL string) ([]*spoolv1.TagSummary, error) {
	return s.tags[spoolURL], nil
}

func (s *stubSpool) FetchTag(ctx context.Context, spoolURL, tagName string) (*spoolv1.FetchTagResponse, error) {
	if s.fetch != nil {
		if ft, ok := s.fetch[spoolURL+"|"+tagName]; ok {
			return ft, nil
		}
	}
	return &spoolv1.FetchTagResponse{Name: tagName, SpoolUrl: spoolURL}, nil
}

func (s *stubSpool) DeployTag(ctx context.Context, spoolURL, tagName string) (*spoolv1.DeployTagResponse, error) {
	s.deployed = append(s.deployed, spoolURL+"|"+tagName)
	return &spoolv1.DeployTagResponse{
		TagName:  tagName,
		SpoolUrl: spoolURL,
		Total:    1,
		Spawned:  1,
		Results: []*spoolv1.ModuleDeployResult{
			{ModuleId: "fixture-mod", Spawned: true},
		},
	}, nil
}

func TestMarketplacePage_WithStub(t *testing.T) {
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
					{Repo: "github.com/Muxcore-Media/request-media", Version: "v0.2.2", Checksum: "abcdef0123456789deadbeef", Required: true},
				},
			},
		},
	}

	r := mustRequest("GET", "/marketplace")
	w := httptest.NewRecorder()
	h.MarketplacePage(w, r)
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="marketplace-page"`) {
		t.Fatalf("missing marketplace page: %s", truncate(body, 300))
	}
	if !strings.Contains(body, "media") || !strings.Contains(body, "Install / Deploy") {
		t.Fatalf("expected tag + deploy CTA, got: %s", truncate(body, 500))
	}
	if !strings.Contains(body, "Rollback") {
		t.Fatal("expected rollback note")
	}
	if !strings.Contains(body, `data-testid="marketplace-trust"`) || !strings.Contains(body, "checksum") {
		t.Fatal("expected trust / checksum copy")
	}
	if !strings.Contains(body, `data-testid="marketplace-module-pins"`) || !strings.Contains(body, "request-media") {
		t.Fatalf("expected module pins, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, "sha256:abcdef012345") {
		t.Fatal("expected truncated checksum pin")
	}
}

func TestMarketplaceDeploy_WithStub(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)
	stub := &stubSpool{}
	h.Spool = stub
	var audited []string
	h.AuditHook = func(actor, action, resource, resourceID string, details map[string]string) {
		audited = append(audited, action+"|"+resourceID)
		if details["checksum_verified"] != "true" {
			t.Fatalf("details=%v", details)
		}
	}

	form := url.Values{}
	form.Set("spool_url", "https://example.test/spool")
	form.Set("tag_name", "default")
	r := httptest.NewRequest(http.MethodPost, "/marketplace/deploy", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.MarketplaceDeploy(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "deployed=") || !strings.Contains(loc, "checksums") {
		t.Fatalf("location=%s", loc)
	}
	if len(stub.deployed) != 1 || stub.deployed[0] != "https://example.test/spool|default" {
		t.Fatalf("deployed=%v", stub.deployed)
	}
	if len(audited) != 1 || audited[0] != "marketplace.deploy|default" {
		t.Fatalf("audit=%v", audited)
	}
}

func TestMarketplacePage_Unavailable(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/marketplace")
	w := httptest.NewRecorder()
	h.MarketplacePage(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "SpoolService unavailable") {
		t.Fatalf("expected unavailable message, got %s", truncate(body, 400))
	}
	if !strings.Contains(body, "gRPC") || !strings.Contains(body, "ADMIN_UI_API_REST_URL") {
		t.Fatalf("expected gRPC / api-rest path hint, got %s", truncate(body, 500))
	}
}
