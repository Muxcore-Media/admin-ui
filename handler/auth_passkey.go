package handler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) authClient(ctx context.Context) (authv1.AuthServiceClient, *grpc.ClientConn, error) {
	mod, err := h.findFirstModule(ctx, "auth")
	if err != nil {
		return nil, nil, fmt.Errorf("auth module unavailable: %w", err)
	}
	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return nil, nil, fmt.Errorf("auth module has no gRPC address")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial auth module: %w", err)
	}
	return authv1.NewAuthServiceClient(conn), conn, nil
}

func (h *Handler) PasskeyList(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if userID == "" {
		w.Write([]byte(`<div class="text-xs text-red-400">user_id required</div>`))
		return
	}

	client, conn, err := h.authClient(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}
	defer conn.Close()

	resp, err := client.ListWebAuthnCredentials(r.Context(), &authv1.ListWebAuthnCredentialsRequest{
		UserId: userID,
	})
	if err != nil {
		slog.Warn("passkey: list failed", "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">list failed</div>`))
		return
	}

	component := templates.PasskeyList(resp.GetCredentials(), len(resp.GetCredentials()), userID)
	h.render(w, r, component)
}

func (h *Handler) PasskeyDelete(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	credID := r.PathValue("credId")
	if userID == "" || credID == "" {
		w.Write([]byte(`<div class="text-xs text-red-400">missing params</div>`))
		return
	}

	client, conn, err := h.authClient(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}
	defer conn.Close()

	_, err = client.DeleteWebAuthnCredential(r.Context(), &authv1.DeleteWebAuthnCredentialRequest{
		UserId:       userID,
		CredentialId: credID,
	})
	if err != nil {
		slog.Warn("passkey: delete failed", "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">delete failed</div>`))
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.user.passkey_delete", "user", userID, map[string]string{
			"credential_id": credID,
		})
	}

	// Re-render the passkey list.
	h.PasskeyList(w, r)
}

func (h *Handler) PasskeyBeginRegister(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if userID == "" {
		w.Write([]byte(`<div class="text-xs text-red-400">user_id required</div>`))
		return
	}

	client, conn, err := h.authClient(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}
	defer conn.Close()

	resp, err := client.BeginAdminRegistration(r.Context(), &authv1.BeginAdminRegistrationRequest{
		UserId: userID,
	})
	if err != nil {
		slog.Warn("passkey: begin register failed", "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">begin registration failed</div>`))
		return
	}

	component := templates.PasskeyRegisterContainer(string(resp.GetOptionsJson()), userID, resp.GetChallenge())
	h.render(w, r, component)
}

func (h *Handler) PasskeyCompleteRegister(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	challenge := r.URL.Query().Get("challenge")
	if userID == "" || challenge == "" {
		http.Error(w, "user_id and challenge required", http.StatusBadRequest)
		return
	}

	client, conn, err := h.authClient(r.Context())
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">auth unavailable</div>`))
		return
	}
	defer conn.Close()

	bodyBytes := jsonFromBody(r)

	resp, err := client.CompleteAdminRegistration(r.Context(), &authv1.CompleteAdminRegistrationRequest{
		UserId:                 userID,
		Challenge:              challenge,
		CredentialResponseJson: bodyBytes,
	})
	if err != nil || resp.Error != "" {
		errMsg := "registration failed"
		if resp != nil && resp.Error != "" {
			errMsg = resp.Error
		}
		w.Write([]byte(fmt.Sprintf(`<div class="text-xs text-red-400">%s</div>`, errMsg)))
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.user.passkey_register", "user", userID, nil)
	}

	w.Write([]byte(`<div class="text-xs text-green-400">Passkey registered</div>`))
}

func jsonFromBody(r *http.Request) []byte {
	data, _ := io.ReadAll(r.Body)
	return data
}
