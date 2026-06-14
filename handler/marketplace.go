package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	spoolv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/spool/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const officialSpoolURL = "https://raw.githubusercontent.com/Muxcore-Media/spool/main"

type SpoolStore struct {
	mu     sync.RWMutex
	spools []string
}

func NewSpoolStore() *SpoolStore {
	return &SpoolStore{
		spools: []string{officialSpoolURL},
	}
}

func (s *SpoolStore) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.spools))
	copy(out, s.spools)
	return out
}

func (s *SpoolStore) Add(spoolURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.spools {
		if existing == spoolURL {
			return
		}
	}
	s.spools = append(s.spools, spoolURL)
}

func (s *SpoolStore) Remove(spoolURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.spools {
		if existing == spoolURL {
			s.spools = append(s.spools[:i], s.spools[i+1:]...)
			return
		}
	}
}

type catalogJSON struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Modules     []catalogModuleJSON `json:"modules"`
	Version     string            `json:"version"`
}

type catalogModuleJSON struct {
	Repo    string `json:"repo"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type tagFileJSON struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	Modules     []tagModuleJSON   `json:"modules"`
}

type tagModuleJSON struct {
	Repo     string `json:"repo"`
	Version  string `json:"version"`
	Required bool   `json:"required"`
}

func fetchJSON(url string, target interface{}) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func (h *Handler) MarketplacePage(w http.ResponseWriter, r *http.Request) {
	spoolURLs := h.Marketplace.List()
	nav := h.nav(r.URL.Path)

	results := make([]templates.SpoolData, 0, len(spoolURLs))
	for _, spoolURL := range spoolURLs {
		sd := templates.SpoolData{URL: spoolURL}
		var raw catalogJSON
		catalogURL := spoolURL + "/catalog.json"
		if err := fetchJSON(catalogURL, &raw); err != nil {
			sd.Error = fmt.Sprintf("failed to fetch catalog: %v", err)
			slog.Warn("marketplace: fetch catalog failed", "url", catalogURL, "error", err)
		} else {
			mods := make([]templates.CatalogModuleData, len(raw.Modules))
			for i, m := range raw.Modules {
				mods[i] = templates.CatalogModuleData{
					Repo:    m.Repo,
					Name:    m.Name,
					Version: m.Version,
				}
			}
			sd.Catalog = &templates.CatalogData{
				Name:        raw.Name,
				Description: raw.Description,
				Version:     raw.Version,
				Modules:     mods,
			}
		}
		results = append(results, sd)
	}

	content := templates.MarketplacePage(results)
	component := templates.Layout("Marketplace", nav, content)
	h.render(w, r, component)
}

func (h *Handler) MarketplaceAddSpoolForm(w http.ResponseWriter, r *http.Request) {
	component := templates.AddSpoolModal()
	h.render(w, r, component)
}

func (h *Handler) MarketplaceAddSpool(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		toast(w, "error", "Invalid form")
		http.Redirect(w, r, "/marketplace", http.StatusSeeOther)
		return
	}
	spoolURL := r.FormValue("url")
	if spoolURL == "" {
		toast(w, "error", "URL is required")
		http.Redirect(w, r, "/marketplace", http.StatusSeeOther)
		return
	}
	h.Marketplace.Add(spoolURL)
	toast(w, "success", "Spool added")
	http.Redirect(w, r, "/marketplace", http.StatusSeeOther)
}

func (h *Handler) MarketplaceRemoveSpool(w http.ResponseWriter, r *http.Request) {
	spoolURL := r.URL.Query().Get("url")
	if spoolURL == "" {
		toast(w, "error", "URL required")
		http.Redirect(w, r, "/marketplace", http.StatusSeeOther)
		return
	}
	if spoolURL == officialSpoolURL {
		toast(w, "error", "Cannot remove official spool")
		http.Redirect(w, r, "/marketplace", http.StatusSeeOther)
		return
	}
	h.Marketplace.Remove(spoolURL)
	toast(w, "success", "Spool removed")
	http.Redirect(w, r, "/marketplace", http.StatusSeeOther)
}

func (h *Handler) MarketplaceDeployTag(w http.ResponseWriter, r *http.Request) {
	tagName := r.PathValue("tag")
	if tagName == "" {
		toast(w, "error", "Tag name required")
		http.Redirect(w, r, "/marketplace", http.StatusSeeOther)
		return
	}

	conn, err := h.cachedConn(r.Context(), h.CoreAddr)
	if err != nil {
		toast(w, "error", "Core unavailable")
		http.Redirect(w, r, "/marketplace", http.StatusSeeOther)
		return
	}
	client := spoolv1.NewSpoolServiceClient(conn)
	_, err = client.DeployTag(r.Context(), &spoolv1.DeployTagRequest{
		TagName: tagName,
	})
	if err != nil {
		slog.Warn("marketplace: deploy tag failed", "tag", tagName, "error", err)
		toast(w, "error", fmt.Sprintf("Deploy failed: %v", err))
		http.Redirect(w, r, "/marketplace", http.StatusSeeOther)
		return
	}

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "marketplace.deploy", "tag", tagName, nil)
	toast(w, "success", fmt.Sprintf("Deploying tag: %s", tagName))
	http.Redirect(w, r, "/marketplace", http.StatusSeeOther)
}

func (h *Handler) MarketplaceTagDetail(w http.ResponseWriter, r *http.Request) {
	tagName := r.PathValue("tag")
	spoolURL := r.URL.Query().Get("spool")
	if spoolURL == "" {
		spoolURL = officialSpoolURL
	}

	var raw tagFileJSON
	tagURL := fmt.Sprintf("%s/tags/%s.json", spoolURL, tagName)
	if err := fetchJSON(tagURL, &raw); err != nil {
		slog.Warn("marketplace: fetch tag failed", "url", tagURL, "error", err)
		nav := h.nav(r.URL.Path)
		content := templates.ErrorPage(404, "Tag not found")
		component := templates.Layout("Tag Not Found", nav, content)
		h.render(w, r, component)
		return
	}

	mods := make([]templates.TagFileModuleData, len(raw.Modules))
	for i, m := range raw.Modules {
		mods[i] = templates.TagFileModuleData{
			Repo:     m.Repo,
			Version:  m.Version,
			Required: m.Required,
		}
	}
	tag := templates.TagFileData{
		Name:        raw.Name,
		Description: raw.Description,
		Version:     raw.Version,
		Modules:     mods,
	}
	content := templates.MarketplaceTagDetail(tag)
	nav := h.nav(r.URL.Path)
	component := templates.Layout(tag.Name, nav, content)
	h.render(w, r, component)
}
