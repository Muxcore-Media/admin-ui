package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Muxcore-Media/userdata-local/parental"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

// Bulk operator content-rating override (ADR-0031 Decision 2). This is the
// list-and-bulk companion of the per-item page in content_rating.go, which owns
// the single-item read, save and read-back. The media modules own the
// classification (ADR-0009); admin-ui only calls their SetContentRating RPC,
// which performs no role check. This handler is therefore the gate: only the
// "admin" role may read or change ratings here (ADR-0031 §2.7: managers can
// edit item tags, admins set rating overrides), and the check runs before any
// module is resolved or dialled. Like the per-item page, it sends the module
// only the bearer that requireAuth validated for this request.

const (
	contentRatingKindMovies = "movies"
	contentRatingKindTV     = "tv"

	// Sources a module reports in content_rating_source. The operator value
	// always wins; tmdb applies only where no operator value exists.
	contentRatingSourceOperator = "operator"
	contentRatingSourceTMDB     = "tmdb"

	contentRatingModuleMovies = "media-movies"
	contentRatingModuleTV     = "media-tvshows"

	// Form choices that are not ladder tokens. Everything else must be one of
	// parentalRatingTokens, exactly as listed.
	contentRatingChoiceNR    = "NR"
	contentRatingChoiceClear = "CLEAR"

	contentRatingPageSize = 50
	// contentRatingMaxPage keeps page*size well inside int32 on the module side.
	contentRatingMaxPage = 1 << 20
	// contentRatingMaxBatch bounds one request. The page lists 50 titles, so a
	// larger batch can only come from a hand-built request.
	contentRatingMaxBatch    = 200
	contentRatingMaxQuery    = 200
	contentRatingMaxIDLength = 200
	contentRatingMaxBody     = 1 << 20

	contentRatingDialTimeout = 3 * time.Second
	contentRatingReadTimeout = 5 * time.Second
	contentRatingListTimeout = contentRatingDialTimeout + contentRatingReadTimeout + time.Second
	// contentRatingApplyTimeout bounds a whole bulk apply. Titles not reached
	// before it expires are reported as not attempted, never dropped.
	contentRatingApplyTimeout = 2 * time.Minute
	// contentRatingRenderMargin is the explicit slack for rendering and writing
	// the result page once the writes and the refresh are done.
	contentRatingRenderMargin = 10 * time.Second
	// contentRatingResponseTimeout is the write deadline of one apply response:
	// the apply budget, the list refresh that follows it, and the render margin.
	// The server-wide WriteTimeout (15s in main.go) is far shorter than a bulk
	// apply, and a response past it never reaches the browser although every
	// write already happened.
	contentRatingResponseTimeout = contentRatingApplyTimeout + contentRatingReadTimeout + contentRatingRenderMargin
)

// contentRatingChange is the RPC mapping of one form choice. It is the single
// place where "Not rated (NR)" and "Clear operator rating" are told apart:
// NR is explicit_unrated=true with an empty rating, Clear is an empty rating
// with explicit_unrated=false, and the modules reject the literal token "NR".
type contentRatingChange struct {
	Rating          string
	ExplicitUnrated bool
	// Mode is "set", "unrated" or "clear"; it is what the audit entry records.
	Mode string
}

// requested is the classification this change asks for, as the audit records
// it: NR is "NR" set by the operator, a clear is nothing at all. It is a record
// of intent only; whether a readback confirms the change is confirmedBy.
func (ch contentRatingChange) requested() (rating, source string) {
	switch ch.Mode {
	case "unrated":
		return contentRatingChoiceNR, contentRatingSourceOperator
	case "clear":
		return "", ""
	}
	return ch.Rating, contentRatingSourceOperator
}

// confirmedBy is the confirmation predicate for the readback that follows an
// acknowledged write.
//
// A set or an NR must read back exactly as requested: the operator source and
// the exact token. A clear is confirmed when the OPERATOR value is gone, not
// when nothing is left: the modules fall back to a lower-precedence TMDB value
// (media-movies and media-tvshows v0.1.24), so the effective classification
// after a clear is either nothing ("", "") or a TMDB rating (a ladder token or
// NR, source "tmdb"). A readback that still names the operator source, an
// unknown source, or an inconsistent rating/source pair is not confirmed.
func (ch contentRatingChange) confirmedBy(rating, source string) bool {
	if ch.Mode != "clear" {
		wantRating, wantSource := ch.requested()
		return rating == wantRating && source == wantSource
	}
	switch source {
	case "":
		return rating == ""
	case contentRatingSourceTMDB:
		return contentRatingCanonicalRating(rating)
	}
	return false
}

// contentRatingCanonicalRating reports whether rating is exactly a ladder token
// or NR, the only values a module reports for a usable classification.
func contentRatingCanonicalRating(rating string) bool {
	if rating == contentRatingChoiceNR {
		return true
	}
	_, valid := parental.RatingLevel(rating)
	return valid && rating == strings.ToUpper(strings.TrimSpace(rating))
}

// parseContentRatingChoice maps a submitted choice to the RPC fields. It
// accepts nothing but the ladder tokens, NR and CLEAR, spelled exactly; free
// text and different case are rejected before any module call.
func parseContentRatingChoice(choice string) (contentRatingChange, bool) {
	switch choice {
	case contentRatingChoiceNR:
		return contentRatingChange{ExplicitUnrated: true, Mode: "unrated"}, true
	case contentRatingChoiceClear:
		return contentRatingChange{Mode: "clear"}, true
	}
	if containsString(parentalRatingTokens, choice) {
		return contentRatingChange{Rating: choice, Mode: "set"}, true
	}
	return contentRatingChange{}, false
}

// contentRatingChoiceLabel is the operator-facing name of a choice.
func contentRatingChoiceLabel(ch contentRatingChange) string {
	switch ch.Mode {
	case "unrated":
		return "Not rated (NR)"
	case "clear":
		return "Operator rating cleared"
	}
	return ch.Rating
}

// classifyContentRating derives the ADR-0031 §2.5 state from what a module
// reports. An empty or unrecognised value is "unavailable"; it is never read as
// unrated.
func classifyContentRating(rating string) (state string, recognised bool) {
	token := strings.ToUpper(strings.TrimSpace(rating))
	switch {
	case token == "":
		return templates.ContentRatingBulkUnavailable, true
	case token == contentRatingChoiceNR:
		return templates.ContentRatingBulkUnrated, true
	case containsString(parentalRatingTokens, token):
		return templates.ContentRatingBulkRated, true
	}
	return templates.ContentRatingBulkUnavailable, false
}

// ratingBackend is one media module reachable for rating work.
type ratingBackend interface {
	list(ctx context.Context, page int, search string) (items []templates.ContentRatingBulkItem, total int, err error)
	set(ctx context.Context, id string, ch contentRatingChange) error
	// get reads one title's current classification, for the readback that
	// follows an acknowledged set; it is the per-item page's read.
	get(ctx context.Context, id string) (title, rating, source string, err error)
	close()
}

type movieRatingBackend struct {
	conn   *grpc.ClientConn
	client mgmntv1.MovieManagementServiceClient
}

func (b movieRatingBackend) close() { _ = b.conn.Close() }

func (b movieRatingBackend) list(ctx context.Context, page int, search string) ([]templates.ContentRatingBulkItem, int, error) {
	resp, err := b.client.ListMovies(ctx, &mgmntv1.ListMoviesRequest{
		Page: int32(page), PageSize: contentRatingPageSize, Search: search,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]templates.ContentRatingBulkItem, 0, len(resp.GetMovies()))
	for _, m := range resp.GetMovies() {
		out = append(out, ratingItem(m.GetId(), m.GetTitle(), int(m.GetYear()), m.GetContentRating(), m.GetContentRatingSource(), m.GetTagLabels()))
	}
	return out, int(resp.GetTotal()), nil
}

func (b movieRatingBackend) set(ctx context.Context, id string, ch contentRatingChange) error {
	_, err := b.client.SetContentRating(ctx, &mgmntv1.SetContentRatingRequest{
		MovieId: id, ContentRating: ch.Rating, ExplicitUnrated: ch.ExplicitUnrated,
	})
	return err
}

func (b movieRatingBackend) get(ctx context.Context, id string) (string, string, string, error) {
	return fetchContentRating(ctx, b.conn, contentRatingModuleMovies, id)
}

type tvRatingBackend struct {
	conn   *grpc.ClientConn
	client tvmgmtv1.TvManagementServiceClient
}

func (b tvRatingBackend) close() { _ = b.conn.Close() }

func (b tvRatingBackend) list(ctx context.Context, page int, search string) ([]templates.ContentRatingBulkItem, int, error) {
	resp, err := b.client.ListTVShows(ctx, &tvmgmtv1.ListTVShowsRequest{
		Page: int32(page), PageSize: contentRatingPageSize, Search: search,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]templates.ContentRatingBulkItem, 0, len(resp.GetSeries()))
	for _, s := range resp.GetSeries() {
		out = append(out, ratingItem(s.GetId(), s.GetName(), int(s.GetYear()), s.GetContentRating(), s.GetContentRatingSource(), s.GetTagLabels()))
	}
	return out, int(resp.GetTotal()), nil
}

func (b tvRatingBackend) set(ctx context.Context, id string, ch contentRatingChange) error {
	_, err := b.client.SetContentRating(ctx, &tvmgmtv1.SetContentRatingRequest{
		SeriesId: id, ContentRating: ch.Rating, ExplicitUnrated: ch.ExplicitUnrated,
	})
	return err
}

func (b tvRatingBackend) get(ctx context.Context, id string) (string, string, string, error) {
	return fetchContentRating(ctx, b.conn, contentRatingModuleTV, id)
}

func ratingItem(id, title string, year int, rating, source string, tags []string) templates.ContentRatingBulkItem {
	state, recognised := classifyContentRating(rating)
	it := templates.ContentRatingBulkItem{
		ID: id, Title: title, Year: year, State: state,
		Source: source, Tags: append([]string(nil), tags...),
	}
	switch {
	case state == templates.ContentRatingBulkUnavailable && !recognised:
		it.Unrecognised = strings.TrimSpace(rating)
		it.Source = ""
	case state == templates.ContentRatingBulkUnavailable:
		it.Source = ""
	default:
		it.Rating = strings.ToUpper(strings.TrimSpace(rating))
	}
	return it
}

// contentRatingModuleID is the media module a kind resolves to: the same module
// IDs the per-item page accepts, so both pages act on the same service.
func contentRatingModuleID(kind string) string {
	if kind == contentRatingKindTV {
		return contentRatingModuleTV
	}
	return contentRatingModuleMovies
}

// openRatingBackend resolves and dials the module for kind. The caller closes it.
func (h *Handler) openRatingBackend(ctx context.Context, kind string) (ratingBackend, error) {
	dialCtx, cancel := context.WithTimeout(ctx, contentRatingDialTimeout)
	addr, err := h.mediaModuleAddr(dialCtx, contentRatingModuleID(kind))
	cancel()
	if err != nil {
		return nil, err
	}
	if kind == contentRatingKindTV {
		conn, client, err := h.dialTVModule(addr)
		if err != nil {
			return nil, err
		}
		return tvRatingBackend{conn: conn, client: client}, nil
	}
	conn, client, err := h.dialMovieModule(addr)
	if err != nil {
		return nil, err
	}
	return movieRatingBackend{conn: conn, client: client}, nil
}

func contentRatingKindOrDefault(v string) string {
	if v == contentRatingKindTV {
		return contentRatingKindTV
	}
	return contentRatingKindMovies
}

func contentRatingForbidden(w http.ResponseWriter, r *http.Request, h *Handler) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	h.render(w, r, templates.Layout("Forbidden", h.nav(r.URL.Path), templates.Forbidden()))
}

// contentRatingPageNumber parses a 1-based page, defaulting to 1.
func contentRatingPageNumber(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		return 1
	}
	// The RPC page is an int32. Clamp so a huge value cannot wrap negative;
	// a page this far out simply lists nothing.
	return min(n, contentRatingMaxPage)
}

func contentRatingQuery(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > contentRatingMaxQuery {
		v = v[:contentRatingMaxQuery]
	}
	return strings.ToValidUTF8(v, "")
}

// loadContentRatingList fills the list part of data. A failure becomes
// data.ListError; the page still renders.
func (h *Handler) loadContentRatingList(ctx context.Context, backend ratingBackend, data *templates.ContentRatingBulkData) {
	readCtx, cancel := context.WithTimeout(ctx, contentRatingReadTimeout)
	defer cancel()
	items, total, err := backend.list(readCtx, data.Page, data.Query)
	if err != nil {
		slog.Warn("content-rating: list failed", "kind", data.Kind, "error", err)
		// Also used to refresh the page after an apply, so this must not say
		// anything about whether ratings changed: the per-title results do.
		data.ListError = "Unable to refresh the " + data.KindNoun + " list (" + contentRatingErrorSummary(err) + ")."
		return
	}
	data.Items = items
	data.Total = total
	data.ListLoaded = true
}

func (h *Handler) newContentRatingData(kind, query string, page int) templates.ContentRatingBulkData {
	noun := "movies"
	if kind == contentRatingKindTV {
		noun = "TV series"
	}
	return templates.ContentRatingBulkData{
		Kind: kind, KindNoun: noun, Query: query, Page: page, PageSize: contentRatingPageSize,
		RatingOptions: parentalRatingTokens,
	}
}

func (h *Handler) renderContentRatings(w http.ResponseWriter, r *http.Request, data templates.ContentRatingBulkData) {
	w.Header().Set("Cache-Control", "no-store")
	h.render(w, r, templates.Layout("Content ratings", h.nav(r.URL.Path), templates.ContentRatingBulkPage(data)))
}

// ContentRatingsBulkPage lists movies or series with their operator content rating
// (admin only). It makes no module call for a non-admin.
func (h *Handler) ContentRatingsBulkPage(w http.ResponseWriter, r *http.Request) {
	if _, admin := parentalActor(r); !admin {
		contentRatingForbidden(w, r, h)
		return
	}
	q := r.URL.Query()
	data := h.newContentRatingData(contentRatingKindOrDefault(q.Get("kind")), contentRatingQuery(q.Get("q")), contentRatingPageNumber(q.Get("page")))

	// Without the validated bearer no module is contacted (a local-only admin
	// session has none), exactly as on the per-item page.
	base, ok := contentRatingContext(r.Context())
	if !ok {
		data.ListError = "Sign in again to read content ratings."
		h.renderContentRatingsError(w, r, data, http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(base, contentRatingListTimeout)
	defer cancel()
	backend, err := h.openRatingBackend(ctx, data.Kind)
	if err != nil {
		slog.Warn("content-rating: module unavailable", "kind", data.Kind, "error", err)
		data.ListError = "The " + data.KindNoun + " module is unavailable, so ratings cannot be shown."
		h.renderContentRatings(w, r, data)
		return
	}
	defer backend.close()
	h.loadContentRatingList(ctx, backend, &data)
	h.renderContentRatings(w, r, data)
}

// contentRatingSelection reads the submitted ids and choice. A per-row "Set"
// button posts only=<id> and that row's select; the bulk bar posts the checked
// ids and the bar's select.
func contentRatingSelection(r *http.Request) (ids []string, choice string, problem string) {
	if only := strings.TrimSpace(r.PostFormValue("only")); only != "" {
		ids = []string{only}
		choice = r.PostFormValue("rating_" + only)
	} else {
		ids = r.PostForm["ids"]
		choice = r.PostFormValue("rating")
	}
	seen := make(map[string]bool, len(ids))
	clean := ids[:0:0]
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if len(id) > contentRatingMaxIDLength || strings.ContainsAny(id, "\x00\r\n") {
			return nil, "", "A selected title has an invalid identifier. Reload the page and try again."
		}
		seen[id] = true
		clean = append(clean, id)
	}
	switch {
	case len(clean) == 0:
		return nil, "", "Select at least one title."
	case len(clean) > contentRatingMaxBatch:
		return nil, "", fmt.Sprintf("Select at most %d titles at a time.", contentRatingMaxBatch)
	}
	return clean, choice, ""
}

// contentRatingErrorSummary is a short, non-sensitive reason for a module error.
func contentRatingErrorSummary(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return "the module could not be reached"
	}
	switch st.Code() {
	case codes.InvalidArgument:
		return "the module rejected the request"
	case codes.NotFound:
		return "not found"
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		return "the module did not answer in time"
	case codes.Unimplemented:
		return "the module does not support content ratings; update it to v0.1.23 or later"
	}
	return "the module reported an error (" + st.Code().String() + ")"
}

// contentRatingFailureMessage explains a failed SetContentRating call. An
// InvalidArgument carries the module's own message because it says what the
// module refused; other failures get a fixed explanation, not module internals.
// Whether the title may have changed follows contentRatingRefused, which is also
// what the audit outcome uses.
func contentRatingFailureMessage(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return "The module could not be reached. This title may not have been changed; check its current rating."
	}
	switch st.Code() {
	case codes.Unauthenticated:
		return "Your session is no longer authorized to change content ratings. This title was not changed."
	case codes.PermissionDenied:
		return "The module refused this operation. This title was not changed."
	case codes.InvalidArgument:
		msg := strings.TrimSpace(st.Message())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		msg = strings.ToValidUTF8(msg, "")
		if msg == "" {
			msg = "invalid rating"
		}
		return "The module rejected this rating (invalid argument: " + msg + "). This title was not changed."
	case codes.NotFound:
		return "The module no longer has this title (it may have been removed). Nothing was changed."
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		return "The module did not answer in time. This title may not have been changed; check its current rating."
	case codes.Unimplemented:
		return "The module does not support content ratings. Update media-movies and media-tvshows to v0.1.23 or later."
	}
	return "The module reported an error (" + st.Code().String() + "). This title may not have been changed; check its current rating."
}

// extendContentRatingResponseDeadline gives this one response the write
// deadline a full apply needs. If the writer cannot be extended (no
// SetWriteDeadline, or a wrapper hiding the connection) the time left before
// the server's own deadline is unknown, so no promise about delivering the
// per-title results can be kept; the caller must refuse before any write.
func extendContentRatingResponseDeadline(w http.ResponseWriter) error {
	return http.NewResponseController(w).SetWriteDeadline(time.Now().Add(contentRatingResponseTimeout))
}

// ContentRatingsBulkApply sets, replaces, marks unrated or clears the operator
// rating of one or more titles (admin only). Each title is its own RPC; the
// response lists every outcome, so a partial failure is never silent.
func (h *Handler) ContentRatingsBulkApply(w http.ResponseWriter, r *http.Request) {
	sess, admin := parentalActor(r)
	if !admin {
		// The modules do not check roles, so refuse before any lookup or dial.
		contentRatingForbidden(w, r, h)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, contentRatingMaxBody)
	if err := r.ParseForm(); err != nil {
		data := h.newContentRatingData(contentRatingKindMovies, "", 1)
		data.FormError = "The form could not be read. Reload the page and try again."
		h.renderContentRatingsError(w, r, data, http.StatusBadRequest)
		return
	}
	kind := r.PostFormValue("kind")
	if kind != contentRatingKindMovies && kind != contentRatingKindTV {
		data := h.newContentRatingData(contentRatingKindMovies, "", 1)
		data.FormError = "Choose movies or TV series."
		h.renderContentRatingsError(w, r, data, http.StatusBadRequest)
		return
	}
	data := h.newContentRatingData(kind, contentRatingQuery(r.PostFormValue("q")), contentRatingPageNumber(r.PostFormValue("page")))

	ids, choice, problem := contentRatingSelection(r)
	if problem != "" {
		data.FormError = problem
		h.renderContentRatingsError(w, r, data, http.StatusBadRequest)
		return
	}
	change, ok := parseContentRatingChoice(choice)
	if !ok {
		data.FormError = "Choose a rating from the list, Not rated (NR), or Clear operator rating. Nothing was changed."
		h.renderContentRatingsError(w, r, data, http.StatusBadRequest)
		return
	}

	// Writes carry only the bearer requireAuth validated for this request. A
	// session without one cannot write, as on the per-item page; refuse before
	// the deadline is extended or any module is resolved.
	base, ok := contentRatingContext(r.Context())
	if !ok {
		data.FormError = "Sign in again before changing content ratings. Nothing was changed."
		h.renderContentRatingsError(w, r, data, http.StatusUnauthorized)
		return
	}

	// The admin gate and validation have passed: only now is the longer write
	// deadline granted, and before the dial and the first write. If it cannot be
	// granted, refuse here; a shortened or best-effort apply could not promise
	// that the per-title results reach the browser.
	if err := extendContentRatingResponseDeadline(w); err != nil {
		slog.Error("content-rating: cannot extend the response write deadline; apply refused", "error", err)
		data.FormError = "This server cannot extend its response time limit for a bulk apply, so nothing was attempted. Nothing was changed."
		h.renderContentRatingsError(w, r, data, http.StatusInternalServerError)
		return
	}
	applyCtx, cancel := context.WithTimeout(base, contentRatingApplyTimeout)
	defer cancel()
	backend, err := h.openRatingBackend(applyCtx, kind)
	if err != nil {
		slog.Warn("content-rating: module unavailable", "kind", kind, "error", err)
		data.FormError = "The " + data.KindNoun + " module is unavailable. Nothing was changed."
		// The list was not and cannot be loaded: say so rather than let the
		// page read as an empty library.
		data.ListError = "The " + data.KindNoun + " module is unavailable, so ratings cannot be shown."
		h.renderContentRatingsError(w, r, data, http.StatusServiceUnavailable)
		return
	}
	defer backend.close()

	applied := &templates.ContentRatingBulkApplied{Choice: contentRatingChoiceLabel(change), Mode: change.Mode}
	module := contentRatingModuleID(kind)
	// authStop is the HTTP status of the first auth failure. A revoked bearer
	// or a refusing provider will refuse every remaining title the same way, so
	// the batch stops: the rest are reported as not attempted and cost no RPC.
	authStop := 0
	for _, id := range ids {
		res := templates.ContentRatingBulkResult{ID: id}
		if authStop != 0 || applyCtx.Err() != nil {
			res.Outcome = templates.ContentRatingBulkOutcomeNotAttempted
			res.Message = "Not attempted: the request ran out of time before this title. Nothing was changed."
			if authStop != 0 {
				res.Message = "Not attempted: your session was no longer authorized before this title. Nothing was changed."
			}
			applied.Skipped++
			applied.Results = append(applied.Results, res)
			continue
		}
		// One audit entry per dispatched write, in the per-item page's shape.
		// Requested values are recorded always; stored values only when a
		// readback confirms them. The audit detaches from request cancellation.
		var outcome, reason string
		var observed *contentRatingReadback
		callCtx, callCancel := context.WithTimeout(applyCtx, contentRatingReadTimeout)
		err := backend.set(callCtx, id, change)
		callCancel()
		var readErr error
		if err != nil {
			slog.Warn("content-rating: SetContentRating failed", "kind", kind, "id", id, "code", status.Code(err).String())
			outcome, reason = contentRatingWriteFailure(err)
			res.Message = contentRatingFailureMessage(err)
		} else {
			// The acknowledgement is empty and proves nothing about the stored
			// value: read the title back once, never retrying the write.
			readCtx, readCancel := context.WithTimeout(applyCtx, contentRatingReadTimeout)
			var title, rating, source string
			title, rating, source, readErr = backend.get(readCtx, id)
			readCancel()
			res.Title = title
			if readErr == nil {
				observed = &contentRatingReadback{Rating: rating, Source: source}
			}
			if readErr == nil && change.confirmedBy(rating, source) {
				outcome, reason = contentRatingOutcomeConfirmed, contentRatingReasonMatched
				res.Rating, res.Source = rating, source
			} else {
				outcome, reason = contentRatingOutcomeUncertain, contentRatingReadbackReason(readErr)
				res.Message = "The save was acknowledged, but this title's current rating could not be confirmed. Check it before saving again."
			}
		}
		switch outcome {
		case contentRatingOutcomeConfirmed:
			res.Outcome = templates.ContentRatingBulkOutcomeOK
			applied.OK++
			if change.Mode == "clear" {
				// The operator value is gone; what remains is the effective
				// classification the module reports now.
				if res.Source == contentRatingSourceTMDB {
					applied.ClearedToTMDB++
				} else {
					applied.ClearedToNone++
				}
			}
		case contentRatingOutcomeRefused:
			res.Outcome = templates.ContentRatingBulkOutcomeFailed
			applied.Failed++
		default:
			res.Outcome = templates.ContentRatingBulkOutcomeUncertain
			applied.Uncertain++
		}
		h.auditLog(r.Context(), sess.UserID, contentRatingAuditAction, "media_item", id, contentRatingAuditDetails(module, change, outcome, reason, observed))
		applied.Results = append(applied.Results, res)
		// An auth failure on the write, or on the readback of an acknowledged
		// write, ends the batch.
		if authStop == 0 {
			authStop = contentRatingAuthStatus(err)
			if authStop == 0 {
				authStop = contentRatingAuthStatus(readErr)
			}
		}
	}

	if authStop != 0 {
		// No further RPCs, including the list refresh: the credential is no
		// longer good. The per-title results already recorded are the answer.
		data.Applied = applied
		data.FormError = "Your session is no longer authorized to change content ratings. Titles after the first refusal were not attempted. Sign in again and check the results below."
		if authStop == http.StatusForbidden {
			data.FormError = "The media service refused this operation for your account. Titles after the first refusal were not attempted. Check the results below."
		}
		data.ListError = "The " + data.KindNoun + " list was not refreshed; reload it to see current ratings."
		h.renderContentRatingsError(w, r, data, authStop)
		return
	}

	// Refresh the visible page after the writes so it shows what the module now
	// holds, and fill result titles from it where the titles are on this page.
	// The refresh has its own read timeout and must not inherit an apply budget
	// that may already be spent.
	h.loadContentRatingList(base, backend, &data)
	titles := make(map[string]string, len(data.Items))
	for _, it := range data.Items {
		titles[it.ID] = it.Title
	}
	for i := range applied.Results {
		if applied.Results[i].Title == "" {
			applied.Results[i].Title = titles[applied.Results[i].ID]
		}
	}
	data.Applied = applied
	h.renderContentRatings(w, r, data)
}

// contentRatingAuthStatus is the HTTP status for an error that ends a batch
// (the per-item page's mapping): 401 for a revoked or missing credential, 403
// for a provider refusing this operator, 0 for anything else.
func contentRatingAuthStatus(err error) int {
	switch status.Code(err) {
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	}
	return 0
}

// renderContentRatingsError renders the page with a form error. The swap
// header lets htmx show the body instead of dropping it (csrf.js swaps any
// error status that carries it).
//
// A refused submission contacts no module (it is validated first), so the list
// is not loaded here and the page says so (ListLoaded stays false) instead of
// presenting an empty library.
func (h *Handler) renderContentRatingsError(w http.ResponseWriter, r *http.Request, data templates.ContentRatingBulkData, code int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(swapErrorHeader, "1")
	w.WriteHeader(code)
	h.render(w, r, templates.Layout("Content ratings", h.nav(r.URL.Path), templates.ContentRatingBulkPage(data)))
}
