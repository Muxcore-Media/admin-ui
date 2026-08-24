package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaRoots         = "media.roots"
	rootsDialTimeout      = 3 * time.Second
	rootsReadTimeout      = 5 * time.Second
	rootsListPageTimeout  = rootsDialTimeout + rootsReadTimeout + time.Second
	rootsEditPageTimeout  = rootsDialTimeout + 2*rootsReadTimeout + time.Second
	rootsActionTimeout    = rootsDialTimeout + rootsReadTimeout + time.Second
	rootsBrowseTimeout    = rootsDialTimeout + rootsReadTimeout + time.Second
)

func (h *Handler) rootsModuleAddr(ctx context.Context) (string, error) {
	if h.Core == nil {
		return "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaRoots)
	if err != nil {
		return "", err
	}
	if len(mods) == 0 {
		return "", fmt.Errorf("no module with capability %s", capMediaRoots)
	}
	addr := normalizeDialAddr(mods[0].GetId(), mods[0].GetHttpAddr())
	if addr == "" {
		return "", fmt.Errorf("roots module has no HTTPAddr")
	}
	return addr, nil
}

func (h *Handler) withRootsClient(ctx context.Context) (rootsv1.RootFolderServiceClient, func(), error) {
	addr, err := h.rootsModuleAddr(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return rootsv1.NewRootFolderServiceClient(conn), func() { _ = conn.Close() }, nil
}

func (h *Handler) listRootOptions(ctx context.Context, mediaKind string) []templates.RootOption {
	client, closer, err := h.withRootsClient(ctx)
	if err != nil {
		return nil
	}
	defer closer()
	resp, err := client.ListRoots(ctx, &rootsv1.ListRootsRequest{MediaKind: mediaKind})
	if err != nil {
		slog.Warn("roots: ListRoots failed", "error", err)
		return nil
	}
	out := make([]templates.RootOption, 0, len(resp.GetRoots()))
	for _, r := range resp.GetRoots() {
		name := r.GetName()
		if name == "" {
			name = r.GetPath()
		}
		out = append(out, templates.RootOption{Path: r.GetPath(), Name: name})
	}
	return out
}

func mediaKindFromModule(moduleID, displayName string) string {
	s := strings.ToLower(moduleID + " " + displayName)
	switch {
	case strings.Contains(s, "movie"):
		return "movies"
	case strings.Contains(s, "tv") || strings.Contains(s, "show"):
		return "tv"
	default:
		return ""
	}
}

func (h *Handler) RootsList(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), rootsListPageTimeout)
	defer cancel()

	var roots []*rootsv1.RootFolder
	errMsg := ""
	dialCtx, dialCancel := context.WithTimeout(pageCtx, rootsDialTimeout)
	client, closer, err := h.withRootsClient(dialCtx)
	dialCancel()
	if err != nil {
		errMsg = "Roots module unavailable: " + err.Error()
	} else {
		defer closer()
		readCtx, readCancel := context.WithTimeout(pageCtx, rootsReadTimeout)
		resp, listErr := client.ListRoots(readCtx, &rootsv1.ListRootsRequest{})
		readCancel()
		if listErr != nil {
			errMsg = listErr.Error()
		} else {
			roots = resp.GetRoots()
		}
	}
	content := templates.RootsListPage(roots, errMsg)
	h.render(w, r, templates.Layout("Root Folders", h.nav(r.URL.Path), content))
}

func (h *Handler) RootNew(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), rootsEditPageTimeout)
	defer cancel()
	browse := h.browsePathOrEmpty(pageCtx, "/")
	content := templates.RootEditPage(&rootsv1.RootFolder{MediaKind: "any"}, true, h.listNamingTemplateOptions(r.Context(), ""), browse, "")
	h.render(w, r, templates.Layout("Add Root", h.nav(r.URL.Path), content))
}

func (h *Handler) RootCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/roots/new", http.StatusSeeOther)
		return
	}
	pageCtx, cancel := context.WithTimeout(r.Context(), rootsActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, rootsDialTimeout)
	client, closer, err := h.withRootsClient(dialCtx)
	dialCancel()
	if err != nil {
		browse := h.browsePathOrEmpty(pageCtx, "/")
		content := templates.RootEditPage(rootFromForm(r), true, h.listNamingTemplateOptions(r.Context(), ""), browse, err.Error())
		h.render(w, r, templates.Layout("Add Root", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, rootsReadTimeout)
	resp, err := client.CreateRoot(readCtx, &rootsv1.CreateRootRequest{
		Path:             r.FormValue("path"),
		Name:             r.FormValue("name"),
		MediaKind:        r.FormValue("media_kind"),
		NamingTemplateId: r.FormValue("naming_template_id"),
	})
	readCancel()
	if err != nil {
		browse := h.browsePathOrEmpty(pageCtx, r.FormValue("path"))
		content := templates.RootEditPage(rootFromForm(r), true, h.listNamingTemplateOptions(r.Context(), ""), browse, err.Error())
		h.render(w, r, templates.Layout("Add Root", h.nav(r.URL.Path), content))
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.root.create", "root", resp.GetRoot().GetId(), map[string]string{
			"path": r.FormValue("path"),
			"name": r.FormValue("name"),
		})
	}
	http.Redirect(w, r, "/roots/"+resp.GetRoot().GetId(), http.StatusSeeOther)
}

func (h *Handler) RootEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, cancel := context.WithTimeout(r.Context(), rootsEditPageTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, rootsDialTimeout)
	client, closer, err := h.withRootsClient(dialCtx)
	dialCancel()
	if err != nil {
		content := templates.RootEditPage(&rootsv1.RootFolder{Id: id}, false, nil, nil, err.Error())
		h.render(w, r, templates.Layout("Edit Root", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, rootsReadTimeout)
	resp, err := client.ListRoots(readCtx, &rootsv1.ListRootsRequest{})
	readCancel()
	if err != nil {
		content := templates.RootEditPage(&rootsv1.RootFolder{Id: id}, false, nil, nil, err.Error())
		h.render(w, r, templates.Layout("Edit Root", h.nav(r.URL.Path), content))
		return
	}
	var root *rootsv1.RootFolder
	for _, rr := range resp.GetRoots() {
		if rr.GetId() == id {
			root = rr
			break
		}
	}
	if root == nil {
		http.Redirect(w, r, "/roots", http.StatusSeeOther)
		return
	}
	browse := h.browsePathOrEmpty(pageCtx, root.GetPath())
	content := templates.RootEditPage(root, false, h.listNamingTemplateOptions(r.Context(), ""), browse, "")
	h.render(w, r, templates.Layout("Edit Root", h.nav(r.URL.Path), content))
}

func (h *Handler) RootUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/roots/"+id, http.StatusSeeOther)
		return
	}
	pageCtx, cancel := context.WithTimeout(r.Context(), rootsActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, rootsDialTimeout)
	client, closer, err := h.withRootsClient(dialCtx)
	dialCancel()
	if err != nil {
		rf := rootFromForm(r)
		rf.Id = id
		content := templates.RootEditPage(rf, false, h.listNamingTemplateOptions(r.Context(), ""), nil, err.Error())
		h.render(w, r, templates.Layout("Edit Root", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, rootsReadTimeout)
	_, err = client.UpdateRoot(readCtx, &rootsv1.UpdateRootRequest{
		Id:               id,
		Path:             r.FormValue("path"),
		Name:             r.FormValue("name"),
		MediaKind:        r.FormValue("media_kind"),
		NamingTemplateId: r.FormValue("naming_template_id"),
	})
	readCancel()
	if err != nil {
		rf := rootFromForm(r)
		rf.Id = id
		content := templates.RootEditPage(rf, false, h.listNamingTemplateOptions(r.Context(), ""), nil, err.Error())
		h.render(w, r, templates.Layout("Edit Root", h.nav(r.URL.Path), content))
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.root.update", "root", id, map[string]string{
			"path": r.FormValue("path"),
			"name": r.FormValue("name"),
		})
	}
	http.Redirect(w, r, "/roots/"+id, http.StatusSeeOther)
}

func (h *Handler) RootDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, cancel := context.WithTimeout(r.Context(), rootsActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, rootsDialTimeout)
	client, closer, err := h.withRootsClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/roots", http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, rootsReadTimeout)
	_, err = client.DeleteRoot(readCtx, &rootsv1.DeleteRootRequest{Id: id})
	readCancel()
	if err != nil {
		slog.Warn("roots: DeleteRoot failed", "id", id, "error", err)
		http.Redirect(w, r, "/roots", http.StatusSeeOther)
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.root.delete", "root", id, nil)
	}
	http.Redirect(w, r, "/roots", http.StatusSeeOther)
}

func (h *Handler) RootsBrowse(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	pageCtx, cancel := context.WithTimeout(r.Context(), rootsBrowseTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, rootsDialTimeout)
	client, closer, err := h.withRootsClient(dialCtx)
	dialCancel()
	if err != nil {
		content := templates.RootsBrowsePartial(nil, err.Error())
		h.render(w, r, content)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, rootsReadTimeout)
	resp, err := client.BrowsePath(readCtx, &rootsv1.BrowsePathRequest{Path: path})
	readCancel()
	if err != nil {
		content := templates.RootsBrowsePartial(&rootsv1.BrowsePathResponse{Path: path}, err.Error())
		h.render(w, r, content)
		return
	}
	h.render(w, r, templates.RootsBrowsePartial(resp, ""))
}

func (h *Handler) browsePathOrEmpty(ctx context.Context, path string) *rootsv1.BrowsePathResponse {
	client, closer, err := h.withRootsClient(ctx)
	if err != nil {
		return &rootsv1.BrowsePathResponse{Path: path}
	}
	defer closer()
	if path == "" {
		path = "/"
	}
	resp, err := client.BrowsePath(ctx, &rootsv1.BrowsePathRequest{Path: path})
	if err != nil {
		return &rootsv1.BrowsePathResponse{Path: path}
	}
	return resp
}

func rootFromForm(r *http.Request) *rootsv1.RootFolder {
	return &rootsv1.RootFolder{
		Path:             r.FormValue("path"),
		Name:             r.FormValue("name"),
		MediaKind:        r.FormValue("media_kind"),
		NamingTemplateId: r.FormValue("naming_template_id"),
	}
}
