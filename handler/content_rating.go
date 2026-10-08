package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

// Operator content-rating override (ADR-0031 Decision 2). The media modules own
// the classification (ADR-0009); admin-ui only calls their SetContentRating
// RPC, which performs no role check. This handler is therefore the gate: only
// the "admin" role may read or change ratings here (ADR-0031 §2.7: managers can
// edit item tags, admins set rating overrides), and the check runs before any
// module is resolved or dialled.

const (
	// contentRatingAuditAction is the audit action written once per changed item.
	contentRatingAuditAction = "admin.content_rating.set"

	contentRatingKindMovies = "movies"
	contentRatingKindTV     = "tv"

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
)

// contentRatingChange is the RPC mapping of one form choice. It is the single
// place where "Not rated (NR)" and "Clear (unavailable)" are told apart:
// NR is explicit_unrated=true with an empty rating, Clear is an empty rating
// with explicit_unrated=false, and the modules reject the literal token "NR".
type contentRatingChange struct {
	Rating          string
	ExplicitUnrated bool
	// Mode is "set", "unrated" or "clear"; it is what the audit entry records.
	Mode string
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
		return "Cleared (unavailable)"
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
		return templates.ContentRatingUnavailable, true
	case token == contentRatingChoiceNR:
		return templates.ContentRatingUnrated, true
	case containsString(parentalRatingTokens, token):
		return templates.ContentRatingRated, true
	}
	return templates.ContentRatingUnavailable, false
}

// ratingBackend is one media module reachable for rating work.
type ratingBackend interface {
	list(ctx context.Context, page int, search string) (items []templates.ContentRatingItem, total int, err error)
	set(ctx context.Context, id string, ch contentRatingChange) error
	close()
}

type movieRatingBackend struct {
	conn   *grpc.ClientConn
	client mgmntv1.MovieManagementServiceClient
}

func (b movieRatingBackend) close() { _ = b.conn.Close() }

func (b movieRatingBackend) list(ctx context.Context, page int, search string) ([]templates.ContentRatingItem, int, error) {
	resp, err := b.client.ListMovies(ctx, &mgmntv1.ListMoviesRequest{
		Page: int32(page), PageSize: contentRatingPageSize, Search: search,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]templates.ContentRatingItem, 0, len(resp.GetMovies()))
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

type tvRatingBackend struct {
	conn   *grpc.ClientConn
	client tvmgmtv1.TvManagementServiceClient
}

func (b tvRatingBackend) close() { _ = b.conn.Close() }

func (b tvRatingBackend) list(ctx context.Context, page int, search string) ([]templates.ContentRatingItem, int, error) {
	resp, err := b.client.ListTVShows(ctx, &tvmgmtv1.ListTVShowsRequest{
		Page: int32(page), PageSize: contentRatingPageSize, Search: search,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]templates.ContentRatingItem, 0, len(resp.GetSeries()))
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

func ratingItem(id, title string, year int, rating, source string, tags []string) templates.ContentRatingItem {
	state, recognised := classifyContentRating(rating)
	it := templates.ContentRatingItem{
		ID: id, Title: title, Year: year, State: state,
		Source: source, Tags: append([]string(nil), tags...),
	}
	switch {
	case state == templates.ContentRatingUnavailable && !recognised:
		it.Unrecognised = strings.TrimSpace(rating)
		it.Source = ""
	case state == templates.ContentRatingUnavailable:
		it.Source = ""
	default:
		it.Rating = strings.ToUpper(strings.TrimSpace(rating))
	}
	return it
}

// openRatingBackend resolves and dials the module for kind. The caller closes it.
func (h *Handler) openRatingBackend(ctx context.Context, kind string) (ratingBackend, error) {
	capability := capMediaLibraryMovies
	if kind == contentRatingKindTV {
		capability = capMediaLibraryTV
	}
	dialCtx, cancel := context.WithTimeout(ctx, contentRatingDialTimeout)
	addr, err := h.findCapabilityDialAddr(dialCtx, capability)
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
func (h *Handler) loadContentRatingList(ctx context.Context, backend ratingBackend, data *templates.ContentRatingData) {
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
}

func (h *Handler) newContentRatingData(kind, query string, page int) templates.ContentRatingData {
	noun := "movies"
	if kind == contentRatingKindTV {
		noun = "TV series"
	}
	return templates.ContentRatingData{
		Kind: kind, KindNoun: noun, Query: query, Page: page, PageSize: contentRatingPageSize,
		RatingOptions: parentalRatingTokens,
	}
}

func (h *Handler) renderContentRatings(w http.ResponseWriter, r *http.Request, data templates.ContentRatingData) {
	w.Header().Set("Cache-Control", "no-store")
	h.render(w, r, templates.Layout("Content ratings", h.nav(r.URL.Path), templates.ContentRatingsPage(data)))
}

// ContentRatingsPage lists movies or series with their operator content rating
// (admin only). It makes no module call for a non-admin.
func (h *Handler) ContentRatingsPage(w http.ResponseWriter, r *http.Request) {
	if _, admin := parentalActor(r); !admin {
		contentRatingForbidden(w, r, h)
		return
	}
	q := r.URL.Query()
	data := h.newContentRatingData(contentRatingKindOrDefault(q.Get("kind")), contentRatingQuery(q.Get("q")), contentRatingPageNumber(q.Get("page")))

	ctx, cancel := context.WithTimeout(r.Context(), contentRatingListTimeout)
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
func contentRatingFailureMessage(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return "The module could not be reached. This title was not changed."
	}
	switch st.Code() {
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

// ContentRatingsApply sets, replaces, marks unrated or clears the operator
// rating of one or more titles (admin only). Each title is its own RPC; the
// response lists every outcome, so a partial failure is never silent.
func (h *Handler) ContentRatingsApply(w http.ResponseWriter, r *http.Request) {
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
		data.FormError = "Choose a rating from the list, Not rated (NR), or Clear (unavailable). Nothing was changed."
		h.renderContentRatingsError(w, r, data, http.StatusBadRequest)
		return
	}

	applyCtx, cancel := context.WithTimeout(r.Context(), contentRatingApplyTimeout)
	defer cancel()
	backend, err := h.openRatingBackend(applyCtx, kind)
	if err != nil {
		slog.Warn("content-rating: module unavailable", "kind", kind, "error", err)
		data.FormError = "The " + data.KindNoun + " module is unavailable. Nothing was changed."
		h.renderContentRatingsError(w, r, data, http.StatusFailedDependency)
		return
	}
	defer backend.close()

	applied := &templates.ContentRatingApplied{Choice: contentRatingChoiceLabel(change), Mode: change.Mode}
	for _, id := range ids {
		res := templates.ContentRatingResult{ID: id}
		if applyCtx.Err() != nil {
			res.Outcome = templates.ContentRatingOutcomeNotAttempted
			res.Message = "Not attempted: the request ran out of time before this title. Nothing was changed."
			applied.Skipped++
			applied.Results = append(applied.Results, res)
			continue
		}
		callCtx, callCancel := context.WithTimeout(applyCtx, contentRatingReadTimeout)
		err := backend.set(callCtx, id, change)
		callCancel()
		if err != nil {
			slog.Warn("content-rating: SetContentRating failed", "kind", kind, "id", id, "code", status.Code(err).String())
			res.Outcome = templates.ContentRatingOutcomeFailed
			res.Message = contentRatingFailureMessage(err)
			applied.Failed++
		} else {
			res.Outcome = templates.ContentRatingOutcomeOK
			applied.OK++
			h.auditLog(r.Context(), sess.UserID, contentRatingAuditAction, "media_item", id, map[string]string{
				"kind":   kind,
				"mode":   change.Mode,
				"rating": change.Rating,
			})
		}
		applied.Results = append(applied.Results, res)
	}

	// Refresh the visible page after the writes so it shows what the module now
	// holds, and fill result titles from it where the titles are on this page.
	h.loadContentRatingList(applyCtx, backend, &data)
	titles := make(map[string]string, len(data.Items))
	for _, it := range data.Items {
		titles[it.ID] = it.Title
	}
	for i := range applied.Results {
		applied.Results[i].Title = titles[applied.Results[i].ID]
	}
	data.Applied = applied
	h.renderContentRatings(w, r, data)
}

// renderContentRatingsError renders the page with a form error. The swap
// header lets htmx show the 4xx/5xx body instead of dropping it.
func (h *Handler) renderContentRatingsError(w http.ResponseWriter, r *http.Request, data templates.ContentRatingData, code int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(swapErrorHeader, "1")
	w.WriteHeader(code)
	h.render(w, r, templates.Layout("Content ratings", h.nav(r.URL.Path), templates.ContentRatingsPage(data)))
}
