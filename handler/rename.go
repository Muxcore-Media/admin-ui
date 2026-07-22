package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capMediaRenamer = "media.renamer"

func (h *Handler) renameModuleAddr(ctx context.Context) (string, error) {
	if h.Core == nil {
		return "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaRenamer)
	if err != nil {
		return "", err
	}
	if len(mods) == 0 {
		return "", fmt.Errorf("no module with capability %s", capMediaRenamer)
	}
	addr := mods[0].GetHttpAddr()
	if addr == "" {
		return "", fmt.Errorf("rename module has no HTTPAddr")
	}
	return addr, nil
}

func (h *Handler) withRenameClient(ctx context.Context) (renamev1.RenameServiceClient, func(), error) {
	addr, err := h.renameModuleAddr(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return renamev1.NewRenameServiceClient(conn), func() { _ = conn.Close() }, nil
}

func (h *Handler) listNamingTemplateOptions(ctx context.Context, mediaType string) []templates.NamingTemplateOption {
	client, closer, err := h.withRenameClient(ctx)
	if err != nil {
		return nil
	}
	defer closer()
	resp, err := client.ListTemplates(ctx, &renamev1.ListTemplatesRequest{MediaType: mediaType})
	if err != nil {
		slog.Warn("rename: ListTemplates failed", "error", err)
		return nil
	}
	out := make([]templates.NamingTemplateOption, 0, len(resp.GetTemplates()))
	for _, t := range resp.GetTemplates() {
		out = append(out, templates.NamingTemplateOption{
			ID:        t.GetId(),
			Name:      t.GetName(),
			MediaType: t.GetMediaType(),
		})
	}
	return out
}

func (h *Handler) NamingTemplatesList(w http.ResponseWriter, r *http.Request) {
	client, closer, err := h.withRenameClient(r.Context())
	var list []*renamev1.NamingTemplate
	errMsg := ""
	if err != nil {
		errMsg = "Rename module unavailable: " + err.Error()
	} else {
		defer closer()
		resp, listErr := client.ListTemplates(r.Context(), &renamev1.ListTemplatesRequest{})
		if listErr != nil {
			errMsg = listErr.Error()
		} else {
			list = resp.GetTemplates()
		}
	}
	content := templates.NamingTemplatesListPage(list, errMsg)
	h.render(w, r, templates.Layout("Naming Templates", h.nav(r.URL.Path), content))
}

func (h *Handler) NamingTemplateNew(w http.ResponseWriter, r *http.Request) {
	content := templates.NamingTemplateEditPage(&renamev1.NamingTemplate{MediaType: "movie"}, true, "")
	h.render(w, r, templates.Layout("New Template", h.nav(r.URL.Path), content))
}

func (h *Handler) NamingTemplateCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/rename/templates/new", http.StatusSeeOther)
		return
	}
	client, closer, err := h.withRenameClient(r.Context())
	if err != nil {
		content := templates.NamingTemplateEditPage(namingTemplateFromForm(r), true, err.Error())
		h.render(w, r, templates.Layout("New Template", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	resp, err := client.CreateTemplate(r.Context(), &renamev1.CreateTemplateRequest{
		Name:      r.FormValue("name"),
		MediaType: r.FormValue("media_type"),
		Pattern:   r.FormValue("pattern"),
		IsDefault: r.FormValue("is_default") == "1",
	})
	if err != nil {
		content := templates.NamingTemplateEditPage(namingTemplateFromForm(r), true, err.Error())
		h.render(w, r, templates.Layout("New Template", h.nav(r.URL.Path), content))
		return
	}
	http.Redirect(w, r, "/rename/templates/"+resp.GetTemplate().GetId(), http.StatusSeeOther)
}

func (h *Handler) NamingTemplateEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	client, closer, err := h.withRenameClient(r.Context())
	if err != nil {
		content := templates.NamingTemplateEditPage(&renamev1.NamingTemplate{Id: id}, false, err.Error())
		h.render(w, r, templates.Layout("Edit Template", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	resp, err := client.GetTemplate(r.Context(), &renamev1.GetTemplateRequest{Id: id})
	if err != nil {
		content := templates.NamingTemplateEditPage(&renamev1.NamingTemplate{Id: id}, false, err.Error())
		h.render(w, r, templates.Layout("Edit Template", h.nav(r.URL.Path), content))
		return
	}
	content := templates.NamingTemplateEditPage(resp.GetTemplate(), false, "")
	h.render(w, r, templates.Layout("Edit Template", h.nav(r.URL.Path), content))
}

func (h *Handler) NamingTemplateUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/rename/templates/"+id, http.StatusSeeOther)
		return
	}
	client, closer, err := h.withRenameClient(r.Context())
	if err != nil {
		t := namingTemplateFromForm(r)
		t.Id = id
		content := templates.NamingTemplateEditPage(t, false, err.Error())
		h.render(w, r, templates.Layout("Edit Template", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	_, err = client.UpdateTemplate(r.Context(), &renamev1.UpdateTemplateRequest{
		Id:        id,
		Name:      r.FormValue("name"),
		Pattern:   r.FormValue("pattern"),
		IsDefault: r.FormValue("is_default") == "1",
	})
	if err != nil {
		t := namingTemplateFromForm(r)
		t.Id = id
		content := templates.NamingTemplateEditPage(t, false, err.Error())
		h.render(w, r, templates.Layout("Edit Template", h.nav(r.URL.Path), content))
		return
	}
	http.Redirect(w, r, "/rename/templates/"+id, http.StatusSeeOther)
}

func (h *Handler) NamingTemplateDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	client, closer, err := h.withRenameClient(r.Context())
	if err != nil {
		http.Redirect(w, r, "/rename/templates", http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.DeleteTemplate(r.Context(), &renamev1.DeleteTemplateRequest{Id: id}); err != nil {
		slog.Warn("rename: DeleteTemplate failed", "id", id, "error", err)
	}
	http.Redirect(w, r, "/rename/templates", http.StatusSeeOther)
}

func namingTemplateFromForm(r *http.Request) *renamev1.NamingTemplate {
	return &renamev1.NamingTemplate{
		Name:      r.FormValue("name"),
		MediaType: r.FormValue("media_type"),
		Pattern:   r.FormValue("pattern"),
		IsDefault: r.FormValue("is_default") == "1",
	}
}
