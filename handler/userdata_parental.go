package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Muxcore-Media/admin-ui/internal/userdatahttp"
)

const (
	capUserdataLocal    = "userdata.local"
	userdataDialTimeout = 3 * time.Second
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

// syncParentalPINToUserdata mirrors the PIN hash into the user's writable
// userdata blob (prefs.parental.pin_hash). PIN behaviour is unchanged until
// FR-AUTH-010. This is the only field admin-ui still writes into the blob:
// restriction fields live only in the provider-backed policy (ADR-0031 §5.5),
// and the blob is never an authority for them. An empty pinHash removes the
// key. Other keys already present in prefs.parental are left exactly as found.
// Both requests use the checked client (ADR-0033) and the session's bearer.
func (h *Handler) syncParentalPINToUserdata(ctx context.Context, userID, pinHash string) error {
	bearer, err := sessionBearer(SessionFromContext(ctx))
	if err != nil {
		return err
	}
	c, err := h.userdataClient(ctx)
	if err != nil {
		logUserdataFailure("userdata.get", err)
		return err
	}
	raw, err := userdatahttp.GetBlob(ctx, c, bearer, userID)
	if err != nil {
		logUserdataFailure("userdata.get", err)
		return err
	}

	var blob userdataBlob
	if err := json.Unmarshal(raw, &blob); err != nil {
		return userdatahttp.Unresolved("the userdata blob is not valid JSON")
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

	body, err := json.Marshal(blob)
	if err != nil {
		return err
	}
	if err := userdatahttp.PutBlob(ctx, c, bearer, userID, body); err != nil {
		logUserdataFailure("userdata.put", err)
		return err
	}
	return nil
}

// pinSyncMessage explains a failed PIN mirror. The PIN itself was already
// saved in admin-ui's parental.json; only the blob copy is in question.
func pinSyncMessage(err error) string {
	const prefix = "userdata sync failed: "
	switch {
	case userdatahttp.ModuleForbidden(err):
		return prefix + "userdata unavailable, the userdata service does not permit this service (admin-ui's mesh identity). This is a deployment problem, not your admin role. The PIN was not copied to the account's userdata."
	case userdatahttp.NotConfigured(err):
		return prefix + "userdata unavailable, no verified connection to the userdata service is configured (ADMIN_UI_USERDATA_URL / admin-ui mesh identity). The PIN was not copied to the account's userdata."
	}
	switch st := policyStatus(err); st {
	case 0:
		return prefix + "the userdata service is unavailable or returned an unusable answer; the account's userdata may not hold the new PIN. Try again."
	case http.StatusUnauthorized:
		return prefix + "the identity provider no longer accepts your session (HTTP 401). Sign in again."
	case http.StatusForbidden:
		return prefix + "the userdata service refused this account's data for your session (HTTP 403)."
	default:
		return prefix + "the userdata service answered HTTP " + strconv.Itoa(st) + "."
	}
}
