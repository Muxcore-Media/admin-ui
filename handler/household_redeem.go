package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	redeemDialTimeout = 3 * time.Second
	redeemReadTimeout = 5 * time.Second
	redeemPageTimeout = redeemDialTimeout + redeemReadTimeout + time.Second
)

func redeemHTTPDo(_ context.Context, req *http.Request) (*http.Response, error) {
	return (&http.Client{Timeout: redeemReadTimeout}).Do(req)
}

// HouseholdRedeemPage renders the invite redemption first-run form.
// No authentication required — this is how an invited user creates their account.
func (h *Handler) HouseholdRedeemPage(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	data := templates.HouseholdRedeemData{Token: token}

	if token == "" {
		data.Error = "Invite link is missing a token."
		h.renderHouseholdRedeem(w, r, data)
		return
	}

	base := h.authHTTPBase()
	if base == "" {
		data.SoftEmpty = true
		data.Error = "auth-local is not configured; cannot validate the invite."
		h.renderHouseholdRedeem(w, r, data)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), redeemPageTimeout)
	defer cancel()

	validateURL := base + "/api/invite/peek?token=" + url.QueryEscape(token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, validateURL, nil)
	if err != nil {
		data.Error = "Could not build validation request."
		h.renderHouseholdRedeem(w, r, data)
		return
	}
	resp, err := redeemHTTPDo(ctx, req)
	if err != nil {
		// auth-local unreachable — show the form so the user knows what's happening.
		data.SoftEmpty = true
		data.Error = "auth-local unreachable: " + err.Error()
		h.renderHouseholdRedeem(w, r, data)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if invalidInvitePeek(resp.StatusCode, body) {
		data.Error = "This invite link is invalid or has already been used."
	} else if resp.StatusCode == http.StatusOK {
		var info struct {
			Role      string `json:"role"`
			ExpiresAt string `json:"expires_at"`
			CreatedBy string `json:"created_by"`
		}
		if err := json.Unmarshal(body, &info); err == nil {
			data.InviteRole = info.Role
			data.InviteExpiry = info.ExpiresAt
		}
		data.ShowForm = true
	} else {
		data.Error = fmt.Sprintf("Invite validation failed (HTTP %d).", resp.StatusCode)
	}

	h.renderHouseholdRedeem(w, r, data)
}

// HouseholdRedeemSubmit handles the invite redemption form POST.
func (h *Handler) HouseholdRedeemSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/invite/redeem", http.StatusSeeOther)
		return
	}

	token := strings.TrimSpace(r.FormValue("token"))
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	data := templates.HouseholdRedeemData{Token: token, ShowForm: true}

	if token == "" {
		data.Error = "Invite token missing."
		h.renderHouseholdRedeem(w, r, data)
		return
	}
	if username == "" || password == "" {
		data.Error = "Username and password are required."
		h.renderHouseholdRedeem(w, r, data)
		return
	}

	base := h.authHTTPBase()
	if base == "" {
		data.Error = "auth-local is not configured; cannot redeem invite."
		h.renderHouseholdRedeem(w, r, data)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), redeemPageTimeout)
	defer cancel()

	payload, _ := json.Marshal(map[string]string{
		"token":    token,
		"username": username,
		"password": password,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/invite/redeem", bytes.NewReader(payload))
	if err != nil {
		data.Error = "Could not build redeem request."
		h.renderHouseholdRedeem(w, r, data)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := redeemHTTPDo(ctx, req)
	if err != nil {
		data.Error = "auth-local unreachable: " + err.Error()
		h.renderHouseholdRedeem(w, r, data)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = fmt.Sprintf("Redemption failed (HTTP %d).", resp.StatusCode)
		}
		data.Error = msg
		h.renderHouseholdRedeem(w, r, data)
		return
	}

	// Success — redirect to auth-local login so the new user can sign in.
	loginURL := h.AuthAddr + "/login"
	if loginURL == "/login" {
		loginURL = "/login"
	}
	http.Redirect(w, r, loginURL, http.StatusSeeOther)
}

func (h *Handler) renderHouseholdRedeem(w http.ResponseWriter, r *http.Request, data templates.HouseholdRedeemData) {
	h.render(w, r, templates.HouseholdRedeemStandalonePage(data))
}

func invalidInvitePeek(status int, body []byte) bool {
	switch status {
	case http.StatusNotFound, http.StatusGone:
		return true
	case http.StatusBadRequest:
		var peek struct {
			Valid bool `json:"valid"`
		}
		if err := json.Unmarshal(body, &peek); err == nil && !peek.Valid {
			return true
		}
	}
	return false
}
