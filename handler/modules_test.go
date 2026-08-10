package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

func TestModuleList_BoostedReturnsLayout(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/modules")
	r.Header.Set("HX-Request", "true")
	r.Header.Set("HX-Boosted", "true")
	w := httptest.NewRecorder()
	h.ModuleList(w, r)

	body := w.Body.String()
	if !strings.Contains(body, `id="sidebar"`) {
		t.Fatalf("boosted /modules must include sidebar layout, got %d bytes", len(body))
	}
	if !strings.Contains(body, "Modules") {
		t.Fatal("expected Modules heading")
	}
}

func TestModuleList_PartialHXReturnsTableOnly(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/modules")
	r.Header.Set("HX-Request", "true")
	// No HX-Boosted — intentional fragment refresh
	w := httptest.NewRecorder()
	h.ModuleList(w, r)

	body := w.Body.String()
	if strings.Contains(body, `id="sidebar"`) {
		t.Fatal("non-boosted HX fragment must not include full layout/sidebar")
	}
}
