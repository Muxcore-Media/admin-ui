package adminui

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
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
	m.dashboard = dashboard
	m.modules = modules
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
