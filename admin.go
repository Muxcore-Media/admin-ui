package adminui

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func init() {
	contracts.Register(func(deps contracts.ModuleDeps) contracts.Module {
		return NewModule(deps.Registry, deps.Routes)
	})
}

//go:embed templates
var templatesFS embed.FS

type Module struct {
	reg       contracts.ServiceRegistry
	routes    contracts.RouteRegistrar
	dashboard *template.Template
	modules   *template.Template
	settings  *template.Template
}

func NewModule(reg contracts.ServiceRegistry, routes contracts.RouteRegistrar) *Module {
	return &Module{reg: reg, routes: routes}
}

func (m *Module) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:           "admin-ui",
		Name:         "Admin UI",
		Version:      "1.0.0",
		Kind:         contracts.ModuleKindUI,
		Description:  "HTMX + Tailwind CSS admin dashboard",
		Author:       "MuxCore",
		Capabilities: []string{"web.admin", "web.dashboard"},
	}
}

func (m *Module) Init(ctx context.Context) error {
	dashboard, err := template.ParseFS(templatesFS, "templates/base.html", "templates/dashboard.html")
	if err != nil {
		return err
	}
	modules, err := template.ParseFS(templatesFS, "templates/base.html", "templates/modules.html")
	if err != nil {
		return err
	}
	settings, err := template.ParseFS(templatesFS, "templates/base.html", "templates/settings.html")
	if err != nil {
		return err
	}
	m.dashboard = dashboard
	m.modules = modules
	m.settings = settings
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	m.routes.Handle("/", http.HandlerFunc(m.serveHTTP))
	slog.Info("admin-ui routes registered")
	return nil
}

func (m *Module) Stop(ctx context.Context) error { return nil }
func (m *Module) Health(ctx context.Context) error { return nil }

func (m *Module) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	switch {
	case path == "" || path == "dashboard":
		m.dashboardPage(w)
	case path == "modules":
		m.modulesPage(w)
	case path == "settings":
		m.settingsPage(w)
	default:
		http.NotFound(w, r)
	}
}

func (m *Module) dashboardPage(w http.ResponseWriter) {
	all := m.reg.ListAll()
	data := map[string]any{
		"ModuleCount": len(all),
		"Modules":     all,
	}
	if err := m.dashboard.ExecuteTemplate(w, "base.html", data); err != nil {
		slog.Error("template error", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func (m *Module) modulesPage(w http.ResponseWriter) {
	all := m.reg.ListAll()
	data := map[string]any{
		"Modules": all,
	}
	if err := m.modules.ExecuteTemplate(w, "base.html", data); err != nil {
		slog.Error("template error", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// -- Settings page --

type settingsVar struct {
	Name        string
	Value       string
	Default     string
	Description string
	IsSet       bool
	IsSecret    bool
}

type settingsGroup struct {
	Label  string
	Prefix string
	Vars   []settingsVar
}

var knownVars = map[string]settingsVar{
	"MUXCORE_STORAGE_PATH":       {Description: "Root path for media storage", Default: "/data/media"},
	"MUXCORE_QBITTORRENT_ADDR":   {Description: "qBittorrent Web UI address", Default: "http://localhost:8080"},
	"MUXCORE_QBITTORRENT_USER":   {Description: "qBittorrent username", Default: "admin"},
	"MUXCORE_QBITTORRENT_PASS":   {Description: "qBittorrent password", Default: "(required)", IsSecret: true},
	"MUXCORE_JACKETT_ADDR":       {Description: "Jackett/Prowlarr server address", Default: "http://localhost:9117"},
	"MUXCORE_JACKETT_APIKEY":     {Description: "Jackett API key", Default: "(required)", IsSecret: true},
	"MUXCORE_JELLYFIN_URL":       {Description: "Jellyfin server address", Default: "http://localhost:8096"},
	"MUXCORE_JELLYFIN_APIKEY":    {Description: "Jellyfin API key", Default: "(required)", IsSecret: true},
	"MUXCORE_NATS_URL":           {Description: "NATS server URL", Default: "nats://localhost:4222"},
	"MUXCORE_NATS_CREDS":         {Description: "NATS credentials file", Default: "(none)", IsSecret: true},
	"MUXCORE_DISCORD_WEBHOOK":    {Description: "Discord webhook URL for notifications", Default: "(required)", IsSecret: true},
}

var groupLabels = map[string]string{
	"storage":     "Storage",
	"qbittorrent": "qBittorrent Downloader",
	"jackett":     "Jackett Indexer",
	"jellyfin":    "Jellyfin Playback",
	"nats":        "NATS Event Bus",
	"discord":     "Discord Notifier",
}

func (m *Module) settingsPage(w http.ResponseWriter) {
	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "MUXCORE_") {
			continue
		}
		kv := strings.SplitN(e, "=", 2)
		if len(kv) == 2 {
			envMap[kv[0]] = kv[1]
		}
	}

	groups := make(map[string]*settingsGroup)
	for name, meta := range knownVars {
		prefix := modulePrefix(name)
		if _, ok := groups[prefix]; !ok {
			label := groupLabels[prefix]
			if label == "" {
				label = strings.ToUpper(prefix)
			}
			groups[prefix] = &settingsGroup{Label: label, Prefix: "MUXCORE_" + strings.ToUpper(prefix) + "_*"}
		}
		v := meta
		v.Name = name
		if val, ok := envMap[name]; ok {
			v.Value = val
			v.IsSet = true
		}
		groups[prefix].Vars = append(groups[prefix].Vars, v)
	}

	// Sort groups by label
	sorted := make([]settingsGroup, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g.Vars, func(i, j int) bool { return g.Vars[i].Name < g.Vars[j].Name })
		sorted = append(sorted, *g)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Label < sorted[j].Label })

	data := map[string]any{"Groups": sorted}
	if err := m.settings.ExecuteTemplate(w, "base.html", data); err != nil {
		slog.Error("template error", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func modulePrefix(envVar string) string {
	parts := strings.SplitN(envVar, "_", 3)
	if len(parts) >= 3 {
		return strings.ToLower(parts[2])
	}
	if len(parts) == 2 {
		return strings.ToLower(parts[1])
	}
	return "core"
}
