package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
)

func (h *Handler) PasswordResetsPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()

	var users []*authv1.UserInfo
	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err == nil {
		defer func() { _ = conn.Close() }()
		readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
		resp, listErr := client.ListUsers(readCtx, &authv1.ListUsersRequest{})
		readCancel()
		if listErr != nil {
			slog.Warn("password-resets: ListUsers failed", "error", listErr)
		} else {
			users = resp.GetUsers()
		}
	}

	queue := loadPasswordResetQueue(users)
	data := templates.PasswordResetsPageData{
		Rows:      queue.Rows,
		Error:     queue.Error,
		SoftEmpty: queue.SoftEmpty,
		Message:   strings.TrimSpace(r.URL.Query().Get("msg")),
	}
	if qErr := strings.TrimSpace(r.URL.Query().Get("error")); qErr != "" && data.Error == "" {
		data.Error = qErr
	}
	content := templates.PasswordResetsPage(data)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Password resets", nav, content))
}

func (h *Handler) PasswordResetsDismiss(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := dismissPasswordResetRequest(id); err != nil {
		http.Redirect(w, r, "/password-resets?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.password_reset.dismiss", "password_reset", id, nil)
	}
	http.Redirect(w, r, "/password-resets?msg=Request+dismissed", http.StatusSeeOther)
}

func (h *Handler) PasswordResetsSetPassword(w http.ResponseWriter, r *http.Request) {
	resetID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/password-resets?error="+url.QueryEscape("invalid form data"), http.StatusSeeOther)
		return
	}
	password := strings.TrimSpace(r.FormValue("password"))
	if password == "" {
		http.Redirect(w, r, "/password-resets?error="+url.QueryEscape("password required"), http.StatusSeeOther)
		return
	}

	entry, ok, err := passwordResetEntryByID(resetID)
	if err != nil {
		http.Redirect(w, r, "/password-resets?error="+url.QueryEscape("Could not read password reset requests."), http.StatusSeeOther)
		return
	}
	if !ok {
		http.Redirect(w, r, "/password-resets?error="+url.QueryEscape("pending request not found"), http.StatusSeeOther)
		return
	}

	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/password-resets?error="+url.QueryEscape("auth unavailable"), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()

	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	resp, err := client.ListUsers(readCtx, &authv1.ListUsersRequest{})
	readCancel()
	if err != nil {
		slog.Warn("password-resets: ListUsers failed", "error", err)
		http.Redirect(w, r, "/password-resets?error="+url.QueryEscape("could not list users"), http.StatusSeeOther)
		return
	}

	var userID string
	for _, u := range resp.GetUsers() {
		if u.GetUsername() == entry.Username {
			userID = u.GetId()
			break
		}
	}
	if userID == "" {
		http.Redirect(w, r, "/password-resets?error="+url.QueryEscape(fmt.Sprintf("no user account for %q", entry.Username)), http.StatusSeeOther)
		return
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, usersReadTimeout)
	setResp, err := client.SetPassword(readCtx, &authv1.SetPasswordRequest{
		UserId:   userID,
		Password: password,
	})
	readCancel()
	if err != nil {
		slog.Warn("password-resets: SetPassword failed", "error", err)
		http.Redirect(w, r, "/password-resets?error="+url.QueryEscape("set password failed"), http.StatusSeeOther)
		return
	}
	if setResp.Error != "" {
		http.Redirect(w, r, "/password-resets?error="+url.QueryEscape(setResp.Error), http.StatusSeeOther)
		return
	}

	if err := resolvePasswordResetByUsername(entry.Username); err != nil {
		slog.Warn("password-resets: resolve failed", "error", err)
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.password_reset.set_password", "user", userID, map[string]string{
			"username": entry.Username,
			"reset_id": resetID,
		})
	}

	http.Redirect(w, r, "/password-resets?msg=Password+set+and+request+resolved", http.StatusSeeOther)
}
