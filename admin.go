package adminui

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

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
	setup     *template.Template

	mu        sync.Mutex
	setupVals map[string]string
}

func NewModule(reg contracts.ServiceRegistry, routes contracts.RouteRegistrar) *Module {
	return &Module{
		reg:       reg,
		routes:    routes,
		setupVals: make(map[string]string),
	}
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
	setup, err := template.ParseFS(templatesFS, "templates/base.html", "templates/setup.html")
	if err != nil {
		return err
	}
	m.dashboard = dashboard
	m.modules = modules
	m.settings = settings
	m.setup = setup
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
	case path == "setup":
		m.setupPage(w, r)
	case path == "setup/next":
		m.setupNext(w, r)
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
	"MUXCORE_STORAGE_PATH":     {Description: "Root path for media storage", Default: "/data/media"},
	"MUXCORE_QBITTORRENT_ADDR": {Description: "qBittorrent Web UI address", Default: "http://localhost:8080"},
	"MUXCORE_QBITTORRENT_USER": {Description: "qBittorrent username", Default: "admin"},
	"MUXCORE_QBITTORRENT_PASS": {Description: "qBittorrent password", Default: "(required)", IsSecret: true},
	"MUXCORE_JACKETT_ADDR":     {Description: "Jackett/Prowlarr server address", Default: "http://localhost:9117"},
	"MUXCORE_JACKETT_APIKEY":   {Description: "Jackett API key", Default: "(required)", IsSecret: true},
	"MUXCORE_JELLYFIN_URL":     {Description: "Jellyfin server address", Default: "http://localhost:8096"},
	"MUXCORE_JELLYFIN_APIKEY":  {Description: "Jellyfin API key", Default: "(required)", IsSecret: true},
	"MUXCORE_NATS_URL":         {Description: "NATS server URL", Default: "nats://localhost:4222"},
	"MUXCORE_NATS_CREDS":       {Description: "NATS credentials file", Default: "(none)", IsSecret: true},
	"MUXCORE_DISCORD_WEBHOOK":  {Description: "Discord webhook URL for notifications", Default: "(required)", IsSecret: true},
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

// -- Setup wizard --

var setupSteps = []string{"Welcome", "Storage", "Indexers", "Downloader", "Playback", "Done"}

type setupData struct {
	Step           int
	Steps          []string
	LastIndex      int
	StoragePath    string
	DownloadsPath  string
	JackettAddr    string
	JackettApiKey  string
	QbitAddr       string
	QbitUser       string
	QbitPass       string
	JellyfinURL    string
	JellyfinApiKey string
	Summary        []summaryItem
}

type summaryItem struct {
	Key   string
	Value string
}

func (m *Module) setupPage(w http.ResponseWriter, r *http.Request) {
	step, _ := strconv.Atoi(r.URL.Query().Get("step"))
	m.mu.Lock()
	vals := make(map[string]string, len(m.setupVals))
	for k, v := range m.setupVals {
		vals[k] = v
	}
	m.mu.Unlock()

	data := m.buildSetupData(step, vals)
	if err := m.setup.ExecuteTemplate(w, "base.html", data); err != nil {
		slog.Error("template error", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func (m *Module) setupNext(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	step, _ := strconv.Atoi(r.FormValue("step"))

	m.mu.Lock()
	for key, vals := range r.Form {
		if key != "step" && len(vals) > 0 {
			m.setupVals[key] = vals[0]
		}
	}
	vals := make(map[string]string, len(m.setupVals))
	for k, v := range m.setupVals {
		vals[k] = v
	}
	m.mu.Unlock()

	nextStep := step + 1
	data := m.buildSetupData(nextStep, vals)
	if err := m.setup.ExecuteTemplate(w, "wizard-content", data); err != nil {
		slog.Error("template error", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func (m *Module) buildSetupData(step int, vals map[string]string) setupData {
	data := setupData{
		Step:           step,
		Steps:          setupSteps,
		LastIndex:      len(setupSteps) - 1,
		StoragePath:    vals["storage_path"],
		DownloadsPath:  vals["downloads_path"],
		JackettAddr:    vals["jackett_addr"],
		JackettApiKey:  vals["jackett_apikey"],
		QbitAddr:       vals["qbittorrent_addr"],
		QbitUser:       vals["qbittorrent_user"],
		QbitPass:       vals["qbittorrent_pass"],
		JellyfinURL:    vals["jellyfin_url"],
		JellyfinApiKey: vals["jellyfin_apikey"],
	}

	if step >= len(setupSteps) {
		data.Summary = []summaryItem{
			{Key: "Storage Path", Value: strDefault(data.StoragePath, "/data/media")},
			{Key: "Downloads Path", Value: strDefault(data.DownloadsPath, "/data/downloads")},
			{Key: "Jackett URL", Value: strDefault(data.JackettAddr, "http://localhost:9117")},
			{Key: "qBittorrent URL", Value: strDefault(data.QbitAddr, "http://localhost:8080")},
			{Key: "Jellyfin URL", Value: strDefault(data.JellyfinURL, "http://localhost:8096")},
		}
	}

	return data
}

func strDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}
