package handler

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

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

// Leave three seconds for rendering and writing under the HTTP server's 15s
// WriteTimeout. This budget includes the two current-identity checks, discovery,
// and classification RPCs; it must wrap requireAuth, not the protected handler.
const contentRatingRequestTimeout = 12 * time.Second

type contentRatingRenderContextKey struct{}

func (h *Handler) registerContentRatingRoutes(mux *http.ServeMux, timeout time.Duration) {
	mux.HandleFunc("GET /media/{moduleID}/item/{id}/content-rating", contentRatingRequest(h.requireAuth(h.ContentRatingPage), timeout))
	mux.HandleFunc("POST /media/{moduleID}/item/{id}/content-rating", contentRatingRequest(h.requireAuth(h.ContentRatingSave), timeout))
}

func contentRatingRequest(next http.HandlerFunc, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		// ParseForm reads the body synchronously and does not observe ctx.Done.
		// net/http supports this controller; in-process recorders may not, but
		// have no socket reads to bound. The server resets it for the next request.
		if r.Body != nil && r.Body != http.NoBody {
			// Authentication or validation may return before consuming the
			// body. Close in that case instead of letting net/http drain it
			// before flushing the response; successful ParseForm removes this.
			w.Header().Set("Connection", "close")
			deadline, _ := ctx.Deadline()
			if err := http.NewResponseController(w).SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
				http.Error(w, "Unable to read the classification request safely. Nothing was changed.", http.StatusServiceUnavailable)
				return
			}
		}
		ctx = context.WithValue(ctx, contentRatingRenderContextKey{}, r.Context())
		next(w, r.WithContext(ctx))
	}
}

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
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		d.Error = "Submit exactly one operator classification. Nothing was changed."
		h.renderContentRating(w, r, d, http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	parseErr := r.ParseForm()
	// Do not let a completed body's deadline cancel the server's background
	// connection read while classification RPCs or response rendering run.
	if parseErr == nil {
		_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
		w.Header().Del("Connection")
	}
	if err := parseErr; err != nil {
		var timeoutError interface{ Timeout() bool }
		readTimedOut := errors.As(err, &timeoutError) && timeoutError.Timeout()
		if ctx.Err() != nil || readTimedOut {
			d.Error = "The request timed out before saving. Nothing was changed. Reload to try again."
			// The remainder cannot be reused as another request. Avoid the
			// HTTP/1 server's automatic body drain before writing this refusal.
			w.Header().Set("Connection", "close")
			if readTimedOut {
				// net/http cancels the original request context after a socket
				// read timeout. Render this refusal only (never call a provider)
				// under a separate short bound so its explanation can reach the
				// still-writable connection. A closed caller socket still fails.
				renderCtx, renderCancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Second)
				defer renderCancel()
				r = r.WithContext(context.WithValue(r.Context(), contentRatingRenderContextKey{}, renderCtx))
			}
			h.renderContentRating(w, r, d, http.StatusServiceUnavailable)
			return
		}
		d.Error = "Submit exactly one operator classification. Nothing was changed."
		h.renderContentRating(w, r, d, http.StatusBadRequest)
		return
	}
	if len(r.PostForm["classification"]) != 1 || len(r.PostForm) != 1 || len(r.URL.Query()) != 0 {
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
	wantRating, wantSource := rating, "operator"
	if unrated {
		wantRating = "NR"
	}
	if choice == "clear" {
		wantSource = ""
	}
	// Record each dispatched mutation once, including lost acknowledgements and
	// failed readback. Requested values are not evidence of a committed value.
	outcome, reason := "uncertain", "write_interrupted"
	defer func() {
		details := map[string]string{"module": d.ModuleID, "requested_rating": wantRating, "requested_source": wantSource, "outcome": outcome, "reason": reason}
		if outcome == "confirmed" {
			details["content_rating"], details["source"] = wantRating, wantSource
		}
		h.auditLog(r.Context(), SessionFromContext(r.Context()).UserID, "admin.media.content_rating", "media_item", d.ItemID, details)
	}()
	writeCtx, writeCancel := context.WithTimeout(ctx, mediaReadTimeout)
	switch d.ModuleID {
	case "media-movies":
		_, err = mgmntv1.NewMovieManagementServiceClient(conn).SetContentRating(writeCtx, &mgmntv1.SetContentRatingRequest{MovieId: d.ItemID, ContentRating: rating, ExplicitUnrated: unrated})
	case "media-tvshows":
		_, err = tvmgmtv1.NewTvManagementServiceClient(conn).SetContentRating(writeCtx, &tvmgmtv1.SetContentRatingRequest{SeriesId: d.ItemID, ContentRating: rating, ExplicitUnrated: unrated})
	}
	writeCancel()
	if err != nil {
		reason = status.Code(err).String()
		if contentRatingRefused(err) {
			outcome = "refused"
		}
		code, msg := contentRatingError(err, true)
		d.Error = msg
		h.renderContentRating(w, r, d, code)
		return
	}
	// The mutation returns an empty acknowledgement. Read the authoritative item
	// once; do not claim success for a stale/mismatched value or retry a write.
	err = readContentRating(ctx, conn, &d)
	if err != nil || d.Rating != wantRating || d.Source != wantSource {
		reason = "readback_mismatch"
		if err != nil {
			reason = "readback_unavailable"
		}
		d.Error = "The save was acknowledged, but its current classification could not be confirmed. Reload and check before saving again."
		d.Loaded = false
		h.renderContentRating(w, r, d, http.StatusBadGateway)
		return
	}
	d.Saved = true
	outcome, reason = "confirmed", "readback_matched"
	h.renderContentRating(w, r, d, http.StatusOK)
}

func contentRatingRefused(err error) bool {
	switch status.Code(err) {
	case codes.Unauthenticated, codes.PermissionDenied, codes.NotFound, codes.InvalidArgument, codes.Unimplemented:
		return true
	default:
		return false
	}
}

func contentRatingError(err error, write bool) (int, string) {
	code, message := http.StatusServiceUnavailable, "The media service is unavailable."
	switch status.Code(err) {
	case codes.Unauthenticated:
		code, message = http.StatusUnauthorized, "Sign in again to access content ratings."
	case codes.PermissionDenied:
		code, message = http.StatusForbidden, "The media service refused this operation."
	case codes.NotFound:
		code, message = http.StatusNotFound, "This item was not found."
	case codes.InvalidArgument:
		code, message = http.StatusBadRequest, "The media service rejected the classification."
	case codes.Unimplemented:
		code, message = http.StatusNotImplemented, "This media service does not support content ratings yet."
	}
	if write {
		if contentRatingRefused(err) {
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
	// Only rendering may use the caller context after our operation budget.
	// Provider calls above keep the canceled context; caller cancellation still
	// cancels rendering. All validated UI state is already in d.
	if renderCtx, ok := r.Context().Value(contentRatingRenderContextKey{}).(context.Context); ok {
		r = r.WithContext(renderCtx)
	}
	h.render(w, r, templates.Layout("Content rating", h.nav("/media/"+d.ModuleID), templates.ContentRatingPage(d)))
}
