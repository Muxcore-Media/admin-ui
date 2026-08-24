package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaRenamer      = "media.renamer"
	renameDialTimeout    = 3 * time.Second
	renameReadTimeout    = 5 * time.Second
	renamePageTimeout    = renameDialTimeout + renameReadTimeout + time.Second
	renameBatchTimeout   = 60 * time.Second
)

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
	addr := normalizeDialAddr(mods[0].GetId(), mods[0].GetHttpAddr())
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
	pageCtx, cancel := context.WithTimeout(r.Context(), renamePageTimeout)
	defer cancel()

	var list []*renamev1.NamingTemplate
	errMsg := ""
	dialCtx, dialCancel := context.WithTimeout(pageCtx, renameDialTimeout)
	client, closer, err := h.withRenameClient(dialCtx)
	dialCancel()
	if err != nil {
		errMsg = "Rename module unavailable: " + err.Error()
	} else {
		defer closer()
		readCtx, readCancel := context.WithTimeout(pageCtx, renameReadTimeout)
		resp, listErr := client.ListTemplates(readCtx, &renamev1.ListTemplatesRequest{})
		readCancel()
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
	pageCtx, cancel := context.WithTimeout(r.Context(), renamePageTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, renameDialTimeout)
	client, closer, err := h.withRenameClient(dialCtx)
	dialCancel()
	if err != nil {
		content := templates.NamingTemplateEditPage(namingTemplateFromForm(r), true, err.Error())
		h.render(w, r, templates.Layout("New Template", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, renameReadTimeout)
	resp, err := client.CreateTemplate(readCtx, &renamev1.CreateTemplateRequest{
		Name:      r.FormValue("name"),
		MediaType: r.FormValue("media_type"),
		Pattern:   r.FormValue("pattern"),
		IsDefault: r.FormValue("is_default") == "1",
	})
	readCancel()
	if err != nil {
		content := templates.NamingTemplateEditPage(namingTemplateFromForm(r), true, err.Error())
		h.render(w, r, templates.Layout("New Template", h.nav(r.URL.Path), content))
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.rename.template.create", "naming_template", resp.GetTemplate().GetId(), map[string]string{
			"name":       r.FormValue("name"),
			"media_type": r.FormValue("media_type"),
		})
	}
	http.Redirect(w, r, "/rename/templates/"+resp.GetTemplate().GetId(), http.StatusSeeOther)
}

func (h *Handler) NamingTemplateEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, cancel := context.WithTimeout(r.Context(), renamePageTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, renameDialTimeout)
	client, closer, err := h.withRenameClient(dialCtx)
	dialCancel()
	if err != nil {
		content := templates.NamingTemplateEditPage(&renamev1.NamingTemplate{Id: id}, false, err.Error())
		h.render(w, r, templates.Layout("Edit Template", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, renameReadTimeout)
	resp, err := client.GetTemplate(readCtx, &renamev1.GetTemplateRequest{Id: id})
	readCancel()
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
	pageCtx, cancel := context.WithTimeout(r.Context(), renamePageTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, renameDialTimeout)
	client, closer, err := h.withRenameClient(dialCtx)
	dialCancel()
	if err != nil {
		t := namingTemplateFromForm(r)
		t.Id = id
		content := templates.NamingTemplateEditPage(t, false, err.Error())
		h.render(w, r, templates.Layout("Edit Template", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, renameReadTimeout)
	_, err = client.UpdateTemplate(readCtx, &renamev1.UpdateTemplateRequest{
		Id:        id,
		Name:      r.FormValue("name"),
		Pattern:   r.FormValue("pattern"),
		IsDefault: r.FormValue("is_default") == "1",
	})
	readCancel()
	if err != nil {
		t := namingTemplateFromForm(r)
		t.Id = id
		content := templates.NamingTemplateEditPage(t, false, err.Error())
		h.render(w, r, templates.Layout("Edit Template", h.nav(r.URL.Path), content))
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.rename.template.update", "naming_template", id, map[string]string{
			"name": r.FormValue("name"),
		})
	}
	http.Redirect(w, r, "/rename/templates/"+id, http.StatusSeeOther)
}

func (h *Handler) NamingTemplateDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, cancel := context.WithTimeout(r.Context(), renamePageTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, renameDialTimeout)
	client, closer, err := h.withRenameClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/rename/templates", http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, renameReadTimeout)
	_, delErr := client.DeleteTemplate(readCtx, &renamev1.DeleteTemplateRequest{Id: id})
	readCancel()
	if delErr != nil {
		err = delErr
		slog.Warn("rename: DeleteTemplate failed", "id", id, "error", err)
		http.Redirect(w, r, "/rename/templates", http.StatusSeeOther)
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.rename.template.delete", "naming_template", id, nil)
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

func (h *Handler) OrganizePage(w http.ResponseWriter, r *http.Request) {
	data := templates.OrganizePageData{
		MediaType: "movie",
		Flash:     r.URL.Query().Get("status"),
		Error:     r.URL.Query().Get("error"),
	}
	content := templates.OrganizePage(data)
	h.render(w, r, templates.Layout("Organize", h.nav(r.URL.Path), content))
}

func (h *Handler) OrganizePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/rename/organize", http.StatusSeeOther)
		return
	}
	directory := strings.TrimSpace(r.FormValue("directory"))
	mediaType := r.FormValue("media_type")
	if mediaType != "tv" {
		mediaType = "movie"
	}
	importMode := r.FormValue("import_mode")
	dryRun := r.FormValue("dry_run") != "0"

	data := templates.OrganizePageData{
		Directory: directory,
		MediaType: mediaType,
	}
	if directory == "" {
		data.Error = "directory required"
		h.render(w, r, templates.Layout("Organize", h.nav(r.URL.Path), templates.OrganizePage(data)))
		return
	}

	pageCtx, cancel := context.WithTimeout(r.Context(), renameBatchTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, renameDialTimeout)
	client, closer, err := h.withRenameClient(dialCtx)
	dialCancel()
	if err != nil {
		data.Error = err.Error()
		h.render(w, r, templates.Layout("Organize", h.nav(r.URL.Path), templates.OrganizePage(data)))
		return
	}
	defer closer()

	resp, err := client.BatchRename(pageCtx, &renamev1.BatchRenameRequest{
		Directory:  directory,
		MediaType:  mediaType,
		DryRun:     dryRun,
		ImportMode: importMode,
	})
	if err != nil {
		data.Error = err.Error()
		h.render(w, r, templates.Layout("Organize", h.nav(r.URL.Path), templates.OrganizePage(data)))
		return
	}
	data.Total = int(resp.GetTotal())
	data.Renamed = int(resp.GetRenamed())
	data.Errors = int(resp.GetErrors())
	for _, row := range resp.GetResults() {
		data.Results = append(data.Results, templates.OrganizeResultRow{
			Original:  row.GetOriginal(),
			RenamedTo: row.GetRenamedTo(),
			Success:   row.GetSuccess(),
			Error:     row.GetError(),
		})
	}
	if dryRun {
		data.Flash = "Preview only — no files changed"
	} else {
		data.Flash = "Rename applied"
		if sess := SessionFromContext(r.Context()); sess != nil {
			h.auditLog(r.Context(), sess.UserID, "admin.rename.organize", "directory", directory, map[string]string{
				"media_type": mediaType,
				"renamed":    strconv.Itoa(data.Renamed),
			})
		}
	}
	h.render(w, r, templates.Layout("Organize", h.nav(r.URL.Path), templates.OrganizePage(data)))
}
