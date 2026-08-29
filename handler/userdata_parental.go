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

func (h *Handler) syncParentalToUserdata(ctx context.Context, userID string, p parentalSettings) error {
	base := h.UserdataURL
	if base == "" {
		base = h.userdataBaseURL(ctx)
	}
	if base == "" {
		return fmt.Errorf("userdata-local unavailable")
	}

	readCtx, cancel := context.WithTimeout(ctx, userdataReadTimeout)
	defer cancel()

	getReq, err := http.NewRequestWithContext(readCtx, http.MethodGet, base+"/userdata", nil)
	if err != nil {
		return err
	}
	q := getReq.URL.Query()
	q.Set("user_id", userID)
	getReq.URL.RawQuery = q.Encode()
	getReq.Header.Set("X-User-ID", userID)

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
	parentalRaw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	prefs["parental"] = parentalRaw
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
	putReq, err := http.NewRequestWithContext(putCtx, http.MethodPut, base+"/userdata", bytes.NewReader(body))
	if err != nil {
		return err
	}
	putReq.URL.RawQuery = q.Encode()
	putReq.Header.Set("Content-Type", "application/json")
	putReq.Header.Set("X-User-ID", userID)

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
