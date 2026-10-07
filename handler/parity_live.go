package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

var brandingMu sync.Mutex

// LogsPage / LogsPartial live in logs.go (file viewer + event ring).

type brandingFile struct {
	ServerName  string `json:"server_name"`
	LoginBanner string `json:"login_banner"`
	CustomCSS   string `json:"custom_css"`
	SplashURL   string `json:"splash_url"`
}

func loadBranding() brandingFile {
	brandingMu.Lock()
	defer brandingMu.Unlock()
	raw, err := os.ReadFile(brandingFilePath())
	if err != nil {
		return brandingFile{ServerName: "MuxCore"}
	}
	var b brandingFile
	if json.Unmarshal(raw, &b) != nil {
		return brandingFile{ServerName: "MuxCore"}
	}
	if b.ServerName == "" {
		b.ServerName = "MuxCore"
	}
	return b
}

func saveBranding(b brandingFile) error {
	brandingMu.Lock()
	defer brandingMu.Unlock()
	path := brandingFilePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (h *Handler) BrandingPage(w http.ResponseWriter, r *http.Request) {
	b := loadBranding()
	data := templates.BrandingData{
		ServerName:  b.ServerName,
		LoginBanner: b.LoginBanner,
		CustomCSS:   b.CustomCSS,
		SplashURL:   b.SplashURL,
	}
	if r.URL.Query().Get("saved") == "1" {
		data.Saved = true
	}
	content := templates.BrandingEditPage(data)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Branding", nav, content)
	h.render(w, r, component)
}

func (h *Handler) BrandingSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b := brandingFile{
		ServerName:  r.FormValue("server_name"),
		LoginBanner: r.FormValue("login_banner"),
		CustomCSS:   r.FormValue("custom_css"),
		SplashURL:   r.FormValue("splash_url"),
	}
	if err := saveBranding(b); err != nil {
		data := templates.BrandingData{
			ServerName: b.ServerName, LoginBanner: b.LoginBanner,
			CustomCSS: b.CustomCSS, SplashURL: b.SplashURL, Error: err.Error(),
		}
		content := templates.BrandingEditPage(data)
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Branding", nav, content)
		h.render(w, r, component)
		return
	}
	http.Redirect(w, r, "/branding?saved=1", http.StatusSeeOther)
}

func (h *Handler) BrandingCSS(w http.ResponseWriter, r *http.Request) {
	b := loadBranding()
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(b.CustomCSS))
}
