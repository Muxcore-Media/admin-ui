package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Muxcore-Media/admin-ui/internal/meshdial"
	templates "github.com/Muxcore-Media/admin-ui/templ"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"github.com/Muxcore-Media/userdata-local/parental"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// These endpoints consume only the owning modules' classification RPCs. Generic
// media metadata, browser identity headers and vote averages are not authority.
func contentRatingModule(moduleID string) bool {
	return moduleID == "media-movies" || moduleID == "media-tvshows"
}

func contentRatingContext(ctx context.Context) (context.Context, bool) {
	sess := SessionFromContext(ctx)
	if sess == nil || strings.TrimSpace(sess.AuthLocalToken) == "" {
		return ctx, false
	}
	// Do not allow inherited identity metadata to compete with the bearer
	// requireAuth validated and authorized for this request.
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("x-auth-token")
	md.Delete("x-muxcore-user-id")
	md.Delete("x-muxcore-tenant-id")
	md.Set("authorization", "Bearer "+sess.AuthLocalToken)
	return metadata.NewOutgoingContext(ctx, md), true
}

func (h *Handler) ContentRatingPage(w http.ResponseWriter, r *http.Request) {
	d := contentRatingData(r)
	if !contentRatingModule(d.ModuleID) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), mediaDialTimeout+mediaReadTimeout)
	defer cancel()
	ctx, ok := contentRatingContext(ctx)
	if !ok {
		d.Error = "Sign in again to read the current classification."
		h.renderContentRating(w, r, d, http.StatusUnauthorized)
		return
	}
	conn, err := h.contentRatingClient(ctx, d.ModuleID)
	if err == nil {
		defer func() { _ = conn.Close() }()
		err = readContentRating(ctx, conn, &d)
	}
	code := http.StatusOK
	if err != nil {
		code, d.Error = contentRatingError(err, false)
	}
	h.renderContentRating(w, r, d, code)
}

func contentRatingData(r *http.Request) templates.ContentRatingData {
	_, admin := parentalActor(r)
	return templates.ContentRatingData{
		ModuleID: r.PathValue("moduleID"), ItemID: r.PathValue("id"),
		CanEdit: admin, Options: parentalRatingTokens,
	}
}

func (h *Handler) contentRatingClient(ctx context.Context, moduleID string) (*grpc.ClientConn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, mediaDialTimeout)
	defer cancel()
	addr, err := h.mediaModuleAddr(dialCtx, moduleID)
	if err != nil {
		return nil, err
	}
	return meshdial.NewClient(addr)
}

func readContentRating(ctx context.Context, conn *grpc.ClientConn, d *templates.ContentRatingData) error {
	var id string
	switch d.ModuleID {
	case "media-movies":
		resp, err := mgmntv1.NewMovieManagementServiceClient(conn).GetMovie(ctx, &mgmntv1.GetMovieRequest{MovieId: d.ItemID})
		if err != nil {
			return err
		}
		item := resp.GetMovie()
		id, d.Title, d.Rating, d.Source = item.GetId(), item.GetTitle(), item.GetContentRating(), item.GetContentRatingSource()
	case "media-tvshows":
		resp, err := tvmgmtv1.NewTvManagementServiceClient(conn).GetTVShow(ctx, &tvmgmtv1.GetTVShowRequest{SeriesId: d.ItemID})
		if err != nil {
			return err
		}
		item := resp.GetSeries()
		id, d.Title, d.Rating, d.Source = item.GetId(), item.GetName(), item.GetContentRating(), item.GetContentRatingSource()
	}
	if id == "" || id != d.ItemID {
		// Suppress data from a malformed or misrouted response.
		d.Title, d.Rating, d.Source = "", "", ""
		return errors.New("classification response item mismatch")
	}
	d.Loaded = true
	d.State = "Unavailable — restricted accounts are denied."
	if d.Rating == "" && d.Source == "" {
		d.Selected = "clear"
	} else if d.Source == "operator" {
		if _, valid := parental.RatingLevel(d.Rating); valid {
			d.State, d.Selected = "Rated by an operator", strings.ToUpper(strings.TrimSpace(d.Rating))
		} else if d.Rating == "NR" || d.Rating == "UR" {
			d.State, d.Selected = "Explicit unrated", "unrated"
		}
	}
	return nil
}

func (h *Handler) ContentRatingSave(w http.ResponseWriter, r *http.Request) {
	d := contentRatingData(r)
	if !contentRatingModule(d.ModuleID) {
		http.NotFound(w, r)
		return
	}
	if !d.CanEdit {
		d.Error = "Only admins can change content ratings. Nothing was changed."
		h.renderContentRating(w, r, d, http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), mediaDialTimeout+2*mediaReadTimeout)
	defer cancel()
	ctx, ok := contentRatingContext(ctx)
	if !ok {
		d.CanEdit = false
		d.Error = "Sign in again before changing content ratings. Nothing was changed."
		h.renderContentRating(w, r, d, http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || len(r.PostForm["classification"]) != 1 || len(r.PostForm) != 1 || len(r.URL.Query()) != 0 {
		d.Error = "Submit exactly one operator classification. Nothing was changed."
		h.renderContentRating(w, r, d, http.StatusBadRequest)
		return
	}
	choice := r.PostForm.Get("classification")
	rating, unrated := choice, false
	switch choice {
	case "clear":
		rating = ""
	case "unrated":
		rating, unrated = "", true
	default:
		if _, valid := parental.RatingLevel(choice); !valid || choice != strings.ToUpper(strings.TrimSpace(choice)) {
			d.Error = "Choose a classification from the list. Nothing was changed."
			h.renderContentRating(w, r, d, http.StatusBadRequest)
			return
		}
	}
	conn, err := h.contentRatingClient(ctx, d.ModuleID)
	if err != nil {
		d.Error = "The media service is unavailable. Nothing was changed. Reload to try again."
		h.renderContentRating(w, r, d, http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = conn.Close() }()
	writeCtx, writeCancel := context.WithTimeout(ctx, mediaReadTimeout)
	switch d.ModuleID {
	case "media-movies":
		_, err = mgmntv1.NewMovieManagementServiceClient(conn).SetContentRating(writeCtx, &mgmntv1.SetContentRatingRequest{MovieId: d.ItemID, ContentRating: rating, ExplicitUnrated: unrated})
	case "media-tvshows":
		_, err = tvmgmtv1.NewTvManagementServiceClient(conn).SetContentRating(writeCtx, &tvmgmtv1.SetContentRatingRequest{SeriesId: d.ItemID, ContentRating: rating, ExplicitUnrated: unrated})
	}
	writeCancel()
	if err != nil {
		code, msg := contentRatingError(err, true)
		d.Error = msg
		h.renderContentRating(w, r, d, code)
		return
	}
	// The mutation returns an empty acknowledgement. Read the authoritative item
	// once; do not claim success for a stale/mismatched value or retry a write.
	err = readContentRating(ctx, conn, &d)
	wantRating, wantSource := rating, "operator"
	if unrated {
		wantRating = "NR"
	}
	if choice == "clear" {
		wantSource = ""
	}
	if err != nil || d.Rating != wantRating || d.Source != wantSource {
		d.Error = "The save was acknowledged, but its current classification could not be confirmed. Reload and check before saving again."
		d.Loaded = false
		h.renderContentRating(w, r, d, http.StatusBadGateway)
		return
	}
	d.Saved = true
	sess := SessionFromContext(r.Context())
	h.auditLog(r.Context(), sess.UserID, "admin.media.content_rating", "media_item", d.ItemID, map[string]string{"module": d.ModuleID, "content_rating": wantRating, "source": wantSource})
	h.renderContentRating(w, r, d, http.StatusOK)
}

func contentRatingError(err error, write bool) (int, string) {
	code, message, definite := http.StatusServiceUnavailable, "The media service is unavailable.", false
	switch status.Code(err) {
	case codes.Unauthenticated:
		code, message, definite = http.StatusUnauthorized, "Sign in again to access content ratings.", true
	case codes.PermissionDenied:
		code, message, definite = http.StatusForbidden, "The media service refused this operation.", true
	case codes.NotFound:
		code, message, definite = http.StatusNotFound, "This item was not found.", true
	case codes.InvalidArgument:
		code, message, definite = http.StatusBadRequest, "The media service rejected the classification.", true
	case codes.Unimplemented:
		code, message, definite = http.StatusNotImplemented, "This media service does not support content ratings yet.", true
	}
	if write {
		if definite {
			message += " Nothing was changed."
		} else {
			message += " The change may have been saved. Reload and check before saving again."
		}
	} else {
		message += " Reload to try again."
	}
	return code, message
}

func (h *Handler) renderContentRating(w http.ResponseWriter, r *http.Request, d templates.ContentRatingData, code int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if code >= 400 {
		w.Header().Set(swapErrorHeader, "1")
	}
	w.WriteHeader(code)
	h.render(w, r, templates.Layout("Content rating", h.nav("/media/"+d.ModuleID), templates.ContentRatingPage(d)))
}
