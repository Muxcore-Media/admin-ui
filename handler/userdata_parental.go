package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	capUserdataLocal    = "userdata.local"
	userdataDialTimeout = 3 * time.Second
	userdataReadTimeout = 5 * time.Second
	userdataHTTPPath    = "/api/userdata"
	muxcoreUserIDHeader = "X-MuxCore-User-Id"
)

type userdataBlob struct {
	Progress  map[string]json.RawMessage `json:"progress"`
	Favorites map[string]json.RawMessage `json:"favorites"`
	Prefs     json.RawMessage            `json:"prefs,omitempty"`
	Playlists json.RawMessage            `json:"playlists,omitempty"`
	Queue     json.RawMessage            `json:"queue,omitempty"`
}

func (h *Handler) userdataBaseURL(ctx context.Context) string {
	if u := strings.TrimRight(strings.TrimSpace(os.Getenv("ADMIN_UI_USERDATA_URL")), "/"); u != "" {
		return u
	}
	if h.Core == nil {
		return ""
	}
	dialCtx, cancel := context.WithTimeout(ctx, userdataDialTimeout)
	defer cancel()
	mod, err := h.findFirstModule(dialCtx, capUserdataLocal)
	if err != nil {
		return ""
	}
	addr := mod.GetHttpAddr()
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + strings.TrimRight(addr, "/")
}

// syncParentalPINToUserdata mirrors the PIN hash into the user's writable
// userdata blob (prefs.parental.pin_hash). PIN behaviour is unchanged until
// FR-AUTH-010. This is the only field admin-ui still writes into the blob:
// restriction fields live only in the provider-backed policy (ADR-0031 §5.5),
// and the blob is never an authority for them. An empty pinHash removes the
// key. Other keys already present in prefs.parental are left exactly as found.
func (h *Handler) syncParentalPINToUserdata(ctx context.Context, userID, pinHash string) error {
	base := h.UserdataURL
	if base == "" {
		base = h.userdataBaseURL(ctx)
	}
	if base == "" {
		return fmt.Errorf("userdata-local unavailable")
	}

	readCtx, cancel := context.WithTimeout(ctx, userdataReadTimeout)
	defer cancel()

	getReq, err := http.NewRequestWithContext(readCtx, http.MethodGet, base+userdataHTTPPath, nil)
	if err != nil {
		return err
	}
	if err := applyUserdataRequestAuth(getReq, ctx, userID); err != nil {
		return err
	}

	resp, err := (&http.Client{Timeout: userdataReadTimeout}).Do(getReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("userdata GET: %s", resp.Status)
	}

	var blob userdataBlob
	if err := json.NewDecoder(resp.Body).Decode(&blob); err != nil {
		return err
	}
	if blob.Progress == nil {
		blob.Progress = map[string]json.RawMessage{}
	}
	if blob.Favorites == nil {
		blob.Favorites = map[string]json.RawMessage{}
	}

	prefs := map[string]json.RawMessage{}
	if len(blob.Prefs) > 0 {
		_ = json.Unmarshal(blob.Prefs, &prefs)
	}
	parentalPrefs := map[string]json.RawMessage{}
	if raw, ok := prefs["parental"]; ok {
		_ = json.Unmarshal(raw, &parentalPrefs)
	}
	if pinHash == "" {
		delete(parentalPrefs, "pin_hash")
	} else {
		quoted, err := json.Marshal(pinHash)
		if err != nil {
			return err
		}
		parentalPrefs["pin_hash"] = quoted
	}
	if len(parentalPrefs) == 0 {
		delete(prefs, "parental")
	} else {
		parentalRaw, err := json.Marshal(parentalPrefs)
		if err != nil {
			return err
		}
		prefs["parental"] = parentalRaw
	}
	blob.Prefs, err = json.Marshal(prefs)
	if err != nil {
		return err
	}

	putCtx, putCancel := context.WithTimeout(ctx, userdataReadTimeout)
	defer putCancel()
	body, err := json.Marshal(blob)
	if err != nil {
		return err
	}
	putReq, err := http.NewRequestWithContext(putCtx, http.MethodPut, base+userdataHTTPPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	putReq.Header.Set("Content-Type", "application/json")
	if err := applyUserdataRequestAuth(putReq, ctx, userID); err != nil {
		return err
	}

	putResp, err := (&http.Client{Timeout: userdataReadTimeout}).Do(putReq)
	if err != nil {
		return err
	}
	defer func() { _ = putResp.Body.Close() }()
	if putResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(putResp.Body, 512))
		return fmt.Errorf("userdata PUT: %s (%s)", putResp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func applyUserdataRequestAuth(req *http.Request, ctx context.Context, targetUserID string) error {
	req.Header.Set(muxcoreUserIDHeader, targetUserID)
	sess := SessionFromContext(ctx)
	if sess == nil || strings.TrimSpace(sess.AuthLocalToken) == "" {
		return fmt.Errorf("missing auth-local session token")
	}
	req.Header.Set("Authorization", "Bearer "+sess.AuthLocalToken)
	return nil
}
