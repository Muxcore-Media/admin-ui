package handler

import (
	"fmt"
	"log/slog"
	"net/http"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) UsersCreateForm(w http.ResponseWriter, r *http.Request) {
	component := templates.UserCreateForm()
	h.render(w, r, component)
}

func (h *Handler) UsersDetail(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	ctx := r.Context()

	client, _, err := h.authClient(ctx)
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}

	resp, err := client.ListUsers(ctx, &authv1.ListUsersRequest{})
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">list users failed</div>`))
		return
	}

	for _, u := range resp.GetUsers() {
		if u.GetId() == userID {
			component := templates.UserDetailPage(u)
			h.render(w, r, component)
			return
		}
	}

	w.Write([]byte(`<div class="text-xs text-red-400">user not found</div>`))
}

func (h *Handler) UsersTOTPStatus(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	client, _, err := h.authClient(r.Context())
	if err != nil {
		w.Write([]byte(`<span class="text-xs text-red-400">auth unavailable</span>`))
		return
	}

	status, err := client.TOTPStatus(r.Context(), &authv1.TOTPStatusRequest{UserId: userID})
	if err != nil {
		w.Write([]byte(`<span class="text-xs text-red-400">totp status failed</span>`))
		return
	}
	if status.GetEnabled() {
		w.Write([]byte(`<span class="text-xs text-green-400">TOTP is enabled</span>`))
	} else {
		w.Write([]byte(`<span class="text-xs text-gray-400">TOTP is disabled</span>`))
	}
}

func (h *Handler) UsersPage(w http.ResponseWriter, r *http.Request) {
	client, _, err := h.authClient(r.Context())
	if err != nil {
		slog.Warn("users: auth client failed", "error", err)
		content := templates.UsersPage(nil, "")
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Users", nav, content)
		h.render(w, r, component)
		return
	}

	resp, err := client.ListUsers(r.Context(), &authv1.ListUsersRequest{})
	if err != nil {
		slog.Warn("users: ListUsers failed", "error", err)
		content := templates.UsersPage(nil, "")
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Users", nav, content)
		h.render(w, r, component)
		return
	}

	content := templates.UsersPage(resp.GetUsers(), "")
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Users", nav, content)
	h.render(w, r, component)
}

func (h *Handler) UsersCreate(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r)
	if err := r.ParseForm(); err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">invalid form data</div>`))
		return
	}

	client, _, err := h.authClient(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	if username == "" || password == "" {
		w.Write([]byte(`<div class="text-xs text-red-400">username and password required</div>`))
		return
	}

	resp, err := client.CreateUser(r.Context(), &authv1.CreateUserRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		slog.Warn("users: CreateUser failed", "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">create failed</div>`))
		return
	}
	if resp.Error != "" {
		toast(w, "error", resp.Error)
		w.Write([]byte(fmt.Sprintf(`<div class="text-xs text-red-400">%s</div>`, resp.Error)))
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.user.create", "user", resp.GetUserId(), map[string]string{
			"username": username,
		})
	}

	toast(w, "success", "User "+username+" created")
	w.Header().Set("HX-Redirect", "/users")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) UsersDelete(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")

	client, _, err := h.authClient(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}

	resp, err := client.DeleteUser(r.Context(), &authv1.DeleteUserRequest{UserId: userID})
	if err != nil {
		slog.Warn("users: DeleteUser failed", "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">delete failed</div>`))
		return
	}
	if resp.Error != "" {
		toast(w, "error", resp.Error)
		w.Write([]byte(fmt.Sprintf(`<div class="text-xs text-red-400">%s</div>`, resp.Error)))
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.user.delete", "user", userID, nil)
	}

	toast(w, "success", "User deleted")
	w.Header().Set("HX-Redirect", "/users")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) UsersSetPassword(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r)
	userID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">invalid form data</div>`))
		return
	}

	client, _, err := h.authClient(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}

	password := r.FormValue("password")
	if password == "" {
		w.Write([]byte(`<div class="text-xs text-red-400">password required</div>`))
		return
	}

	resp, err := client.SetPassword(r.Context(), &authv1.SetPasswordRequest{
		UserId:   userID,
		Password: password,
	})
	if err != nil {
		slog.Warn("users: SetPassword failed", "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">set password failed</div>`))
		return
	}
	if resp.Error != "" {
		toast(w, "error", resp.Error)
		w.Write([]byte(fmt.Sprintf(`<div class="text-xs text-red-400">%s</div>`, resp.Error)))
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.user.set_password", "user", userID, nil)
	}

	toast(w, "success", "Password updated")
	w.Header().Set("HX-Redirect", "/users")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) UsersSetRoles(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r)
	userID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">invalid form data</div>`))
		return
	}

	client, _, err := h.authClient(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}

	roles := r.Form["roles"]

	resp, err := client.SetRoles(r.Context(), &authv1.SetRolesRequest{
		UserId: userID,
		Roles:  roles,
	})
	if err != nil {
		slog.Warn("users: SetRoles failed", "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">set roles failed</div>`))
		return
	}
	if resp.Error != "" {
		w.Write([]byte(fmt.Sprintf(`<div class="text-xs text-red-400">%s</div>`, resp.Error)))
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		details := map[string]string{"roles": fmt.Sprintf("%v", roles)}
		h.auditLog(r.Context(), sess.UserID, "admin.user.set_roles", "user", userID, details)
	}

	toast(w, "success", "Roles updated")
	w.Header().Set("HX-Redirect", "/users")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) UsersTOTP(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")

	client, _, err := h.authClient(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}

	status, err := client.TOTPStatus(r.Context(), &authv1.TOTPStatusRequest{UserId: userID})
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">totp status failed</div>`))
		return
	}

	if status.GetEnabled() {
		resp, err := client.DisableTOTP(r.Context(), &authv1.DisableTOTPRequest{UserId: userID})
		if err != nil {
			w.Write([]byte(`<div class="text-xs text-red-400">disable totp failed</div>`))
			return
		}
		if resp.Error != "" {
			w.Write([]byte(fmt.Sprintf(`<div class="text-xs text-red-400">%s</div>`, resp.Error)))
			return
		}
		if sess := SessionFromContext(r.Context()); sess != nil {
			h.auditLog(r.Context(), sess.UserID, "admin.user.totp_disable", "user", userID, nil)
		}
		toast(w, "success", "TOTP disabled")
		w.Write([]byte(`<span class="text-xs text-green-400">TOTP disabled</span>`))
	} else {
		resp, err := client.EnableTOTP(r.Context(), &authv1.EnableTOTPRequest{UserId: userID})
		if err != nil {
			w.Write([]byte(`<div class="text-xs text-red-400">enable totp failed</div>`))
			return
		}
		if resp.Error != "" {
			w.Write([]byte(fmt.Sprintf(`<div class="text-xs text-red-400">%s</div>`, resp.Error)))
			return
		}
		if sess := SessionFromContext(r.Context()); sess != nil {
			h.auditLog(r.Context(), sess.UserID, "admin.user.totp_enable", "user", userID, nil)
		}
		toast(w, "success", "TOTP enabled")
		w.Write([]byte(fmt.Sprintf(`<div class="text-xs space-y-1"><p class="text-green-400">TOTP enabled</p><p class="text-gray-400">Secret: <code class="text-gray-200">%s</code></p><p class="text-gray-400">QR URL: <a href="%s" class="text-indigo-400 hover:text-indigo-300" target="_blank">open</a></p></div>`,
			resp.GetSecret(), resp.GetQrCodeUrl())))
	}
}

func (h *Handler) UsersTokens(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")

	client, _, err := h.authClient(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}

	switch r.Method {
	case http.MethodGet:
		resp, err := client.ListAPITokens(r.Context(), &authv1.ListAPITokensRequest{UserId: userID})
		if err != nil {
			w.Write([]byte(`<div class="text-xs text-red-400">list tokens failed</div>`))
			return
		}
		content := templates.UserTokens(resp.GetTokens(), userID)
		h.render(w, r, content)

	case http.MethodPost:
		limitBody(w, r)
		if err := r.ParseForm(); err != nil {
			w.Write([]byte(`<div class="text-xs text-red-400">invalid form data</div>`))
			return
		}
		name := r.FormValue("name")
		if name == "" {
			w.Write([]byte(`<div class="text-xs text-red-400">name required</div>`))
			return
		}
		resp, err := client.CreateAPIToken(r.Context(), &authv1.CreateAPITokenRequest{
			UserId: userID,
			Name:   name,
		})
		if err != nil {
			w.Write([]byte(`<div class="text-xs text-red-400">create token failed</div>`))
			return
		}
		if resp.Error != "" {
			toast(w, "error", resp.Error)
			w.Write([]byte(fmt.Sprintf(`<div class="text-xs text-red-400">%s</div>`, resp.Error)))
			return
		}
		if sess := SessionFromContext(r.Context()); sess != nil {
			h.auditLog(r.Context(), sess.UserID, "admin.user.token_create", "user", userID, map[string]string{
				"token_name": name,
			})
		}
		toast(w, "success", "Token created")
		w.Write([]byte(fmt.Sprintf(`<div class="text-xs space-y-1"><p class="text-green-400">Token created</p><p class="text-gray-400">Token: <code class="text-gray-200 break-all">%s</code></p><p class="text-yellow-400 text-xs">Store this — it will not be shown again.</p></div>`, resp.GetToken())))

	case http.MethodDelete:
		tokenID := r.PathValue("tokenId")
		_, err := client.DeleteAPIToken(r.Context(), &authv1.DeleteAPITokenRequest{TokenId: tokenID})
		if err != nil {
			w.Write([]byte(`<div class="text-xs text-red-400">delete token failed</div>`))
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
