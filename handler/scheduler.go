package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capScheduler = "scheduler"

func (h *Handler) schedulerAddr(ctx *http.Request) (string, error) {
	mod, err := h.Core.Discovery.Resolve(ctx.Context(), "scheduler-cron")
	if err != nil {
		mods, err2 := h.Core.Discovery.FindByCapability(ctx.Context(), capScheduler)
		if err2 != nil || len(mods) == 0 {
			return "", fmt.Errorf("no scheduler module: %w", err)
		}
		return mods[0].GetHttpAddr(), nil
	}
	return mod.GetHttpAddr(), nil
}

func (h *Handler) schedulerURL(ctx *http.Request, path string) string {
	addr, err := h.schedulerAddr(ctx)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("http://%s%s", addr, path)
}

func (h *Handler) SchedulerPage(w http.ResponseWriter, r *http.Request) {
	base := h.schedulerURL(r, "/list")
	if base == "" {
		content := templates.SchedulerPage(nil)
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Scheduler", nav, content)
		h.render(w, r, component)
		return
	}

	resp, err := http.Get(base)
	if err != nil {
		slog.Warn("scheduler: list failed", "error", err)
		content := templates.SchedulerPage(nil)
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Scheduler", nav, content)
		h.render(w, r, component)
		return
	}
	defer resp.Body.Close()

	var tasks []templates.SchedulerTask
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		slog.Warn("scheduler: decode tasks failed", "error", err)
		tasks = nil
	}
	if tasks == nil {
		tasks = []templates.SchedulerTask{}
	}

	content := templates.SchedulerPage(tasks)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Scheduler", nav, content)
	h.render(w, r, component)
}

func (h *Handler) SchedulerAddForm(w http.ResponseWriter, r *http.Request) {
	component := templates.AddTaskModal()
	h.render(w, r, component)
}

func (h *Handler) SchedulerAdd(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r)
	if err := r.ParseForm(); err != nil {
		toast(w, "error", "Invalid form data")
		h.renderSchedulerTable(w, r)
		return
	}

	body := map[string]any{
		"name":      r.FormValue("name"),
		"cron_expr": r.FormValue("cron_expr"),
	}
	if timeout := r.FormValue("timeout"); timeout != "" {
		body["timeout"] = timeout
	}

	payload, _ := json.Marshal(body)
	base := h.schedulerURL(r, "/schedule")
	if base == "" {
		toast(w, "error", "Scheduler unavailable")
		h.renderSchedulerTable(w, r)
		return
	}

	resp, err := http.Post(base, "application/json", bytes.NewReader(payload))
	if err != nil {
		slog.Warn("scheduler: add failed", "error", err)
		toast(w, "error", "Failed to create task")
		h.renderSchedulerTable(w, r)
		return
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		toast(w, "error", "Scheduler rejected request")
		h.renderSchedulerTable(w, r)
		return
	}

	toast(w, "success", "Task created")
	h.renderSchedulerTable(w, r)
}

func (h *Handler) SchedulerDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	base := h.schedulerURL(r, "/cancel/"+id)
	if base == "" {
		toast(w, "error", "Scheduler unavailable")
		return
	}

	req, err := http.NewRequest(http.MethodDelete, base, nil)
	if err != nil {
		toast(w, "error", "Failed to cancel task")
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("scheduler: cancel failed", "id", id, "error", err)
		toast(w, "error", "Cancel failed")
		return
	}
	resp.Body.Close()

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "scheduler.cancel", "task", id, nil)
	toast(w, "success", "Task cancelled")
	w.Write([]byte(``))
}

func (h *Handler) renderSchedulerTable(w http.ResponseWriter, r *http.Request) {
	base := h.schedulerURL(r, "/list")
	if base == "" {
		w.Write([]byte(`<div class="text-sm text-red-400">Scheduler unavailable</div>`))
		return
	}

	resp, err := http.Get(base)
	if err != nil {
		w.Write([]byte(`<div class="text-sm text-red-400">Failed to list tasks</div>`))
		return
	}
	defer resp.Body.Close()

	var tasks []templates.SchedulerTask
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		tasks = []templates.SchedulerTask{}
	}
	if tasks == nil {
		tasks = []templates.SchedulerTask{}
	}

	component := templates.SchedulerTaskTable(tasks)
	h.render(w, r, component)
}
