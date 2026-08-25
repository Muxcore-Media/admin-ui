package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	usersDialTimeout = 3 * time.Second
	usersReadTimeout = 5 * time.Second
)

func (h *Handler) UsersCreateForm(w http.ResponseWriter, r *http.Request) {
	component := templates.UserCreateForm()
	h.render(w, r, component)
}

func (h *Handler) UsersDetail(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}
	defer func() { _ = conn.Close() }()

	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	resp, err := client.ListUsers(readCtx, &authv1.ListUsersRequest{})
	readCancel()
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">list users failed</div>`))
		return
	}

	for _, u := range resp.GetUsers() {
		if u.GetId() == userID {
			component := templates.UserDetailPage(u)
			h.render(w, r, component)
			return
		}
	}

	_, _ = w.Write([]byte(`<div class="text-xs text-red-400">user not found</div>`))
}

func (h *Handler) UsersTOTPStatus(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		_, _ = w.Write([]byte(`<span class="text-xs text-red-400">auth unavailable</span>`))
		return
	}
	defer func() { _ = conn.Close() }()

	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	status, err := client.TOTPStatus(readCtx, &authv1.TOTPStatusRequest{UserId: userID})
	readCancel()
	if err != nil {
		_, _ = w.Write([]byte(`<span class="text-xs text-red-400">totp status failed</span>`))
		return
	}
	if status.GetEnabled() {
		_, _ = w.Write([]byte(`<span class="text-xs text-green-400">TOTP is enabled</span>`))
	} else {
		_, _ = w.Write([]byte(`<span class="text-xs text-gray-400">TOTP is disabled</span>`))
	}
}

func (h *Handler) UsersPage(w http.ResponseWriter, r *http.Request) {
	resets := loadPasswordResetRequests()
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		slog.Warn("users: auth client failed", "error", err)
		content := templates.UsersPage(nil, "", resets)
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Users", nav, content)
		h.render(w, r, component)
		return
	}
	defer func() { _ = conn.Close() }()

	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	resp, err := client.ListUsers(readCtx, &authv1.ListUsersRequest{})
	readCancel()
	if err != nil {
		slog.Warn("users: ListUsers failed", "error", err)
		content := templates.UsersPage(nil, "", resets)
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Users", nav, content)
		h.render(w, r, component)
		return
	}

	content := templates.UsersPage(resp.GetUsers(), "", resets)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Users", nav, content)
	h.render(w, r, component)
}

func (h *Handler) UsersCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">invalid form data</div>`))
		return
	}

	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}
	defer func() { _ = conn.Close() }()

	username := r.FormValue("username")
	password := r.FormValue("password")
	if username == "" || password == "" {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">username and password required</div>`))
		return
	}

	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	resp, err := client.CreateUser(readCtx, &authv1.CreateUserRequest{
		Username: username,
		Password: password,
	})
	readCancel()
	if err != nil {
		slog.Warn("users: CreateUser failed", "error", err)
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">create failed</div>`))
		return
	}
	if resp.Error != "" {
		_, _ = fmt.Fprintf(w, `<div class="text-xs text-red-400">%s</div>`, resp.Error)
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.user.create", "user", resp.GetUserId(), map[string]string{
			"username": username,
		})
	}

	w.Header().Set("HX-Redirect", "/users")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) UsersDelete(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}
	defer func() { _ = conn.Close() }()

	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	resp, err := client.DeleteUser(readCtx, &authv1.DeleteUserRequest{UserId: userID})
	readCancel()
	if err != nil {
		slog.Warn("users: DeleteUser failed", "error", err)
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">delete failed</div>`))
		return
	}
	if resp.Error != "" {
		_, _ = fmt.Fprintf(w, `<div class="text-xs text-red-400">%s</div>`, resp.Error)
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.user.delete", "user", userID, nil)
	}

	w.Header().Set("HX-Redirect", "/users")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) UsersSetPassword(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">invalid form data</div>`))
		return
	}

	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}
	defer func() { _ = conn.Close() }()

	password := r.FormValue("password")
	if password == "" {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">password required</div>`))
		return
	}

	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	resp, err := client.SetPassword(readCtx, &authv1.SetPasswordRequest{
		UserId:   userID,
		Password: password,
	})
	readCancel()
	if err != nil {
		slog.Warn("users: SetPassword failed", "error", err)
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">set password failed</div>`))
		return
	}
	if resp.Error != "" {
		_, _ = fmt.Fprintf(w, `<div class="text-xs text-red-400">%s</div>`, resp.Error)
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.user.set_password", "user", userID, nil)
	}

	w.Header().Set("HX-Redirect", "/users")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) UsersSetRoles(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">invalid form data</div>`))
		return
	}

	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}
	defer func() { _ = conn.Close() }()

	roles := r.Form["roles"]

	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	resp, err := client.SetRoles(readCtx, &authv1.SetRolesRequest{
		UserId: userID,
		Roles:  roles,
	})
	readCancel()
	if err != nil {
		slog.Warn("users: SetRoles failed", "error", err)
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">set roles failed</div>`))
		return
	}
	if resp.Error != "" {
		_, _ = fmt.Fprintf(w, `<div class="text-xs text-red-400">%s</div>`, resp.Error)
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		details := map[string]string{"roles": fmt.Sprintf("%v", roles)}
		h.auditLog(r.Context(), sess.UserID, "admin.user.set_roles", "user", userID, details)
	}

	w.Header().Set("HX-Redirect", "/users")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) UsersTOTP(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+2*usersReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}
	defer func() { _ = conn.Close() }()

	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	status, err := client.TOTPStatus(readCtx, &authv1.TOTPStatusRequest{UserId: userID})
	readCancel()
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">totp status failed</div>`))
		return
	}

	if status.GetEnabled() {
		readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
		resp, err := client.DisableTOTP(readCtx, &authv1.DisableTOTPRequest{UserId: userID})
		readCancel()
		if err != nil {
			_, _ = w.Write([]byte(`<div class="text-xs text-red-400">disable totp failed</div>`))
			return
		}
		if resp.Error != "" {
			_, _ = fmt.Fprintf(w, `<div class="text-xs text-red-400">%s</div>`, resp.Error)
			return
		}
		if sess := SessionFromContext(r.Context()); sess != nil {
			h.auditLog(r.Context(), sess.UserID, "admin.user.totp_disable", "user", userID, nil)
		}
		_, _ = w.Write([]byte(`<span class="text-xs text-green-400">TOTP disabled</span>`))
	} else {
		readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
		resp, err := client.EnableTOTP(readCtx, &authv1.EnableTOTPRequest{UserId: userID})
		readCancel()
		if err != nil {
			_, _ = w.Write([]byte(`<div class="text-xs text-red-400">enable totp failed</div>`))
			return
		}
		if resp.Error != "" {
			_, _ = fmt.Fprintf(w, `<div class="text-xs text-red-400">%s</div>`, resp.Error)
			return
		}
		if sess := SessionFromContext(r.Context()); sess != nil {
			h.auditLog(r.Context(), sess.UserID, "admin.user.totp_enable", "user", userID, nil)
		}
		_, _ = fmt.Fprintf(w, `<div class="text-xs space-y-1"><p class="text-green-400">TOTP enabled</p><p class="text-gray-400">Secret: <code class="text-gray-200">%s</code></p><p class="text-gray-400">QR URL: <a href="%s" class="text-indigo-400 hover:text-indigo-300" target="_blank">open</a></p></div>`,
			resp.GetSecret(), resp.GetQrCodeUrl())
	}
}

func (h *Handler) UsersTokens(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}
	defer func() { _ = conn.Close() }()

	switch r.Method {
	case http.MethodGet:
		readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
		resp, err := client.ListAPITokens(readCtx, &authv1.ListAPITokensRequest{UserId: userID})
		readCancel()
		if err != nil {
			_, _ = w.Write([]byte(`<div class="text-xs text-red-400">list tokens failed</div>`))
			return
		}
		content := templates.UserTokens(resp.GetTokens(), userID)
		h.render(w, r, content)

	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			_, _ = w.Write([]byte(`<div class="text-xs text-red-400">invalid form data</div>`))
			return
		}
		name := r.FormValue("name")
		if name == "" {
			_, _ = w.Write([]byte(`<div class="text-xs text-red-400">name required</div>`))
			return
		}
		readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
		resp, err := client.CreateAPIToken(readCtx, &authv1.CreateAPITokenRequest{
			UserId: userID,
			Name:   name,
		})
		readCancel()
		if err != nil {
			_, _ = w.Write([]byte(`<div class="text-xs text-red-400">create token failed</div>`))
			return
		}
		if resp.Error != "" {
			_, _ = fmt.Fprintf(w, `<div class="text-xs text-red-400">%s</div>`, resp.Error)
			return
		}
		if sess := SessionFromContext(r.Context()); sess != nil {
			h.auditLog(r.Context(), sess.UserID, "admin.user.token_create", "user", userID, map[string]string{
				"token_name": name,
			})
		}
		_, _ = fmt.Fprintf(w, `<div class="text-xs space-y-1"><p class="text-green-400">Token created</p><p class="text-gray-400">Token: <code class="text-gray-200 break-all">%s</code></p><p class="text-yellow-400 text-xs">Store this — it will not be shown again.</p></div>`, resp.GetToken())

	case http.MethodDelete:
		tokenID := r.PathValue("tokenId")
		readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
		_, err := client.DeleteAPIToken(readCtx, &authv1.DeleteAPITokenRequest{TokenId: tokenID})
		readCancel()
		if err != nil {
			_, _ = w.Write([]byte(`<div class="text-xs text-red-400">delete token failed</div>`))
			return
		}
		if sess := SessionFromContext(r.Context()); sess != nil {
			h.auditLog(r.Context(), sess.UserID, "admin.user.token_delete", "user", userID, map[string]string{
				"token_id": tokenID,
			})
		}
		w.Header().Set("HX-Redirect", fmt.Sprintf("/users/%s", userID))
		w.WriteHeader(http.StatusOK)
	}
}
