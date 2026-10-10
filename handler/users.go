package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	usersDialTimeout = 3 * time.Second
	usersReadTimeout = 5 * time.Second
)

func (h *Handler) UsersCreateForm(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout)
	defer cancel()
	client, conn, err := h.authClient(ctx)
	if err != nil {
		http.Error(w, "auth unavailable", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = conn.Close() }()
	_, err = client.ListUsers(authContextWithToken(ctx), &authv1.ListUsersRequest{})
	if h.renderIdentityUnsupported(w, r, "", identityUsers, err) {
		return
	}
	if err != nil {
		http.Error(w, "could not load user management", http.StatusBadGateway)
		return
	}
	h.render(w, r, templates.UserCreateForm())
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
	if h.renderIdentityUnsupported(w, r, "", identityUsers, err) {
		return
	}
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
	if h.renderIdentityUnsupported(w, r, "", identityTOTP, err) {
		return
	}
	if err != nil {
		_, _ = w.Write([]byte(`<span class="text-xs text-red-400">totp status failed</span>`))
		return
	}
	h.render(w, r, templates.UserTOTPControls(status.GetEnabled(), userID))
}

func (h *Handler) UsersPage(w http.ResponseWriter, r *http.Request) {
	pendingResets := pendingPasswordResetCount()
	// The erasure status read gets its own usersReadTimeout slice on top of
	// the dial and the user list.
	pageCtx, pageCancel := context.WithTimeout(r.Context(), usersDialTimeout+2*usersReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, usersDialTimeout)
	client, conn, err := h.authClient(dialCtx)
	dialCancel()
	if err != nil {
		slog.Warn("users: auth client failed", "error", err)
		content := templates.UsersPage(nil, identityReadError("load users", err), pendingResets, erasureStatusUnavailable("Erasure status is unavailable while the identity provider cannot be reached."))
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Users", nav, content)
		h.render(w, r, component)
		return
	}
	defer func() { _ = conn.Close() }()

	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	resp, err := client.ListUsers(readCtx, &authv1.ListUsersRequest{})
	readCancel()
	if h.renderIdentityUnsupported(w, r, "Users", identityUsers, err) {
		return
	}
	if err != nil {
		slog.Warn("users: ListUsers failed", "error", err)
		content := templates.UsersPage(nil, identityReadError("load users", err), pendingResets, erasureStatusUnavailable("Erasure status is unavailable while users cannot be loaded."))
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Users", nav, content)
		h.render(w, r, component)
		return
	}

	erasures := h.userErasureStatus(pageCtx, client)
	content := templates.UsersPage(resp.GetUsers(), "", pendingResets, erasures)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Users", nav, content)
	h.render(w, r, component)
}

const (
	// erasureStatusPageSize and erasureStatusMaxPages bound the status walk:
	// tombstones are never pruned, so history grows without limit.
	erasureStatusPageSize = 100
	erasureStatusMaxPages = 10
	// erasureCompleteShown is how many completed erasures /users lists.
	erasureCompleteShown = 5
)

func erasureStatusUnavailable(msg string) templates.UserErasureStatus {
	return templates.UserErasureStatus{Message: msg}
}

// userErasureStatus reads AuthService.GetUserErasureStatus with the signed-in
// admin's bearer (ADR-0035 §2.2). It is the only status source: admin-ui
// neither calls the CN-allowlisted ListUserErasures/AckUserErasure nor keeps
// its own record of other modules' progress. Failures degrade the panel, not
// the user list.
func (h *Handler) userErasureStatus(ctx context.Context, client authv1.AuthServiceClient) templates.UserErasureStatus {
	readCtx, cancel := context.WithTimeout(ctx, usersReadTimeout)
	defer cancel()
	readCtx = authContextWithToken(readCtx)

	var all []*authv1.ErasureStatus
	token := ""
	truncated := false
	for page := 0; ; page++ {
		resp, err := client.GetUserErasureStatus(readCtx, &authv1.GetUserErasureStatusRequest{
			PageSize:  erasureStatusPageSize,
			PageToken: token,
		})
		if err != nil {
			switch status.Code(err) {
			case codes.Unimplemented:
				return erasureStatusUnavailable("This identity provider does not report account erasure status.")
			case codes.PermissionDenied:
				return erasureStatusUnavailable("You do not have permission to view account erasure status.")
			case codes.Unauthenticated:
				return erasureStatusUnavailable("Your identity session is no longer authenticated. Sign in again to see erasure status.")
			}
			slog.Warn("users: GetUserErasureStatus failed", "error", err)
			return erasureStatusUnavailable("Erasure status could not be loaded. Try again when the identity provider is available.")
		}
		all = append(all, resp.GetErasures()...)
		token = resp.GetNextPageToken()
		if token == "" {
			break
		}
		if page+1 >= erasureStatusMaxPages {
			truncated = true
			break
		}
	}
	out := buildErasureStatus(all)
	out.Truncated = out.Truncated || truncated
	return out
}

// buildErasureStatus maps the provider's per-erasure status to the page view:
// every incomplete erasure first (oldest first, as the provider orders them),
// then the most recent completed ones. Complete is the provider's own flag
// (every module in AUTH_ERASURE_REQUIRED acknowledged OK), not recomputed.
func buildErasureStatus(in []*authv1.ErasureStatus) templates.UserErasureStatus {
	out := templates.UserErasureStatus{Available: true}
	var done []templates.UserErasureRow
	for _, e := range in {
		row := templates.UserErasureRow{
			ErasureID: e.GetErasureId(),
			DeletedAt: e.GetDeletedAt(),
			Complete:  e.GetComplete(),
		}
		for _, m := range e.GetModules() {
			row.Modules = append(row.Modules, templates.UserErasureModule{
				ModuleID: m.GetModuleId(),
				State:    erasureModuleState(m.GetOutcome()),
				Detail:   m.GetDetailCode(),
				AckedAt:  m.GetAckedAt(),
				Required: m.GetRequired(),
			})
		}
		if row.Complete {
			done = append(done, row)
		} else {
			out.Rows = append(out.Rows, row)
		}
	}
	// Completed erasures arrive oldest first; show the newest few.
	for i, j := 0, len(done)-1; i < j; i, j = i+1, j-1 {
		done[i], done[j] = done[j], done[i]
	}
	if len(done) > erasureCompleteShown {
		done = done[:erasureCompleteShown]
		out.Truncated = true
	}
	out.Rows = append(out.Rows, done...)
	return out
}

func erasureModuleState(o authv1.ErasureOutcome) string {
	switch o {
	case authv1.ErasureOutcome_ERASURE_OUTCOME_OK:
		return "ok"
	case authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED:
		return "failed"
	case authv1.ErasureOutcome_ERASURE_OUTCOME_UNSUPPORTED:
		return "unsupported"
	default:
		return "pending"
	}
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
	if h.renderIdentityUnsupported(w, r, "", identityUsers, err) {
		return
	}
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

// UsersDelete deletes an account through the identity provider, which records
// the ADR-0035 erasure tombstone. The provider requires the signed-in
// administrator's own bearer: a mesh certificate alone is not enough
// (conflict C-37), so a session without a bound bearer is refused here and
// DeleteUser is never called with the module identity only.
func (h *Handler) UsersDelete(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	sess := SessionFromContext(r.Context())
	if sess == nil || strings.TrimSpace(sess.AuthLocalToken) == "" {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">delete requires an administrator sign-in with the identity provider; sign in again</div>`))
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

	readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
	resp, err := client.DeleteUser(authContextWithToken(readCtx), &authv1.DeleteUserRequest{UserId: userID})
	readCancel()
	if h.renderIdentityUnsupported(w, r, "", identityUsers, err) {
		return
	}
	if err != nil {
		slog.Warn("users: DeleteUser failed", "error", err)
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">delete failed</div>`))
		return
	}
	if resp.Error != "" {
		_, _ = fmt.Fprintf(w, `<div class="text-xs text-red-400">%s</div>`, resp.Error)
		return
	}

	var details map[string]string
	if id := resp.GetErasureId(); id != "" {
		details = map[string]string{"erasure_id": id}
	}
	h.auditLog(r.Context(), sess.UserID, "admin.user.delete", "user", userID, details)

	// Apply this module's disposition without waiting for the next sweep.
	if h.ErasureTrigger != nil {
		h.ErasureTrigger()
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
	if h.renderIdentityUnsupported(w, r, "", identityUsers, err) {
		return
	}
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

	readCtx2, readCancel2 := context.WithTimeout(pageCtx, usersReadTimeout)
	listResp, listErr := client.ListUsers(readCtx2, &authv1.ListUsersRequest{})
	readCancel2()
	if listErr == nil {
		for _, u := range listResp.GetUsers() {
			if u.GetId() == userID {
				_ = resolvePasswordResetByUsername(u.GetUsername())
				break
			}
		}
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
	if h.renderIdentityUnsupported(w, r, "", identityUsers, err) {
		return
	}
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
	if h.renderIdentityUnsupported(w, r, "", identityTOTP, err) {
		return
	}
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">totp status failed</div>`))
		return
	}

	if status.GetEnabled() {
		readCtx, readCancel := context.WithTimeout(pageCtx, usersReadTimeout)
		resp, err := client.DisableTOTP(readCtx, &authv1.DisableTOTPRequest{UserId: userID})
		readCancel()
		if h.renderIdentityUnsupported(w, r, "", identityTOTP, err) {
			return
		}
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
		if h.renderIdentityUnsupported(w, r, "", identityTOTP, err) {
			return
		}
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
		if h.renderIdentityUnsupported(w, r, "", identityTokens, err) {
			return
		}
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
		if h.renderIdentityUnsupported(w, r, "", identityTokens, err) {
			return
		}
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
		resp, err := client.DeleteAPIToken(readCtx, &authv1.DeleteAPITokenRequest{TokenId: tokenID})
		readCancel()
		if h.renderIdentityUnsupported(w, r, "", identityTokens, err) {
			return
		}
		if err != nil {
			_, _ = w.Write([]byte(`<div class="text-xs text-red-400">delete token failed</div>`))
			return
		}
		if resp.GetError() != "" {
			http.Error(w, resp.GetError(), http.StatusBadRequest)
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
