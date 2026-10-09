package handler

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const requestMutationUnknown = "The request service did not confirm the outcome. The change may have been saved. Reload and check the request before trying again."

// All five mutation routes share this budget across authentication, discovery,
// form reads and the provider operation. Leave three seconds to render/write
// under the HTTP server's 15-second WriteTimeout. Read routes are unchanged.
const requestMutationTimeout = 12 * time.Second

type requestMutationRenderContextKey struct{}

func (h *Handler) registerRequestMutationRoutes(mux *http.ServeMux, timeout time.Duration) {
	mux.HandleFunc("POST /request", h.requestMutationRequest(h.requireAuth(h.RequestCreate), "/request", timeout))
	mux.HandleFunc("POST /request/{id}/approve", h.requestMutationRequest(h.requireAuth(h.RequestApprove), "/request", timeout))
	mux.HandleFunc("POST /request/{id}/deny", h.requestMutationRequest(h.requireAuth(h.RequestDeny), "/request", timeout))
	mux.HandleFunc("POST /approvals/{id}/approve", h.requestMutationRequest(h.requireAuth(h.ApprovalsApprove), "/approvals", timeout))
	mux.HandleFunc("POST /approvals/{id}/deny", h.requestMutationRequest(h.requireAuth(h.ApprovalsDeny), "/approvals", timeout))
}

func (h *Handler) requestMutationRequest(next http.HandlerFunc, returnPath string, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if r.Body != nil && r.Body != http.NoBody {
			// ParseForm does not observe context cancellation. Bound socket reads
			// too. An early rejection must flush without draining an unread body;
			// a successful form read clears Connection: close below.
			w.Header().Set("Connection", "close")
			deadline, _ := ctx.Deadline()
			if err := http.NewResponseController(w).SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
				h.renderRequestMutationError(w, r, http.StatusServiceUnavailable, "The submitted request could not be read safely. No request was sent.", returnPath)
				return
			}
		}
		ctx = context.WithValue(ctx, requestMutationRenderContextKey{}, r.Context())
		next(w, r.WithContext(ctx))
	}
}

func (h *Handler) parseRequestMutationForm(w http.ResponseWriter, r *http.Request, returnPath string) bool {
	if r.Body != nil && r.Body != http.NoBody {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/x-www-form-urlencoded" {
			// ParseForm can succeed without reading an unsupported body. Keep
			// the read deadline and close guard so an early refusal never waits
			// for net/http to drain that body before writing its explanation.
			h.renderRequestMutationError(w, r, http.StatusBadRequest, "Submit a URL-encoded request form. No request was sent.", returnPath)
			return false
		}
	}
	err := r.ParseForm()
	// A completed form read no longer needs a socket deadline. Leaving it in
	// place could cancel the HTTP caller while the provider outcome is rendered.
	if err == nil {
		_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	}
	if err != nil {
		code := http.StatusBadRequest
		message := "The submitted request could not be read. No request was sent."
		var netErr net.Error
		readTimedOut := errors.As(err, &netErr) && netErr.Timeout()
		if r.Context().Err() != nil || readTimedOut {
			code = http.StatusServiceUnavailable
			message = "The submitted request timed out before it could be sent. No request was sent."
			if readTimedOut {
				// net/http cancels the original context on a socket read timeout.
				// Only this pre-dispatch refusal may render under a separate short
				// bound. No provider work follows; a closed socket still fails.
				renderCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Second)
				defer cancel()
				r = r.WithContext(context.WithValue(r.Context(), requestMutationRenderContextKey{}, renderCtx))
			}
		}
		h.renderRequestMutationError(w, r, code, message, returnPath)
		return false
	}
	w.Header().Del("Connection")
	return true
}

// Only mutations use this client. A redirect must not repeat a POST or turn a
// login/error page's 200 into a successful request action. Reads keep their
// existing client. The original context and five-second transport budget apply.
func requestMutationHTTPDo(req *http.Request) (int, error) {
	client := &http.Client{
		Timeout: requestReadTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	const maxBody = 1 << 20
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return 0, err
	}
	if n > maxBody {
		return 0, errors.New("request mutation response too large")
	}
	return resp.StatusCode, nil
}

// A transport or response failure can happen after the provider has committed
// the mutation. Never claim it was not saved and never automatically resubmit.
func (h *Handler) requestMutationSucceeded(w http.ResponseWriter, r *http.Request, code int, err error, returnPath string) bool {
	if err == nil && code >= 200 && code < 300 {
		return true
	}
	message := requestMutationUnknown
	responseCode := http.StatusBadGateway
	if err != nil {
		responseCode = http.StatusServiceUnavailable
	} else {
		switch code {
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
			responseCode = code
			message = "The request service rejected the submitted details. Reload and check the request before trying again."
		case http.StatusUnauthorized:
			responseCode = code
			message = "The request service rejected your sign-in. Sign in again, then reload and check the request."
		case http.StatusForbidden:
			responseCode = code
			message = "The request service did not permit this action. Reload and check the request."
		case http.StatusNotFound:
			responseCode = code
			message = "The request service could not find this request. Reload and check the request list."
		case http.StatusConflict:
			responseCode = code
			message = "The request changed or cannot accept this action. Reload and check its current status."
		case http.StatusTooManyRequests:
			responseCode = code
			message = "The request service's request limit was reached. Reload and check the request before trying again."
		}
	}
	h.renderRequestMutationError(w, r, responseCode, message, returnPath)
	return false
}

func (h *Handler) renderRequestMutationError(w http.ResponseWriter, r *http.Request, code int, message, returnPath string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(swapErrorHeader, "1")
	w.WriteHeader(code)
	// Only rendering can use the original caller context after the route budget
	// expires. Provider work stays bounded and caller cancellation still applies.
	if renderCtx, ok := r.Context().Value(requestMutationRenderContextKey{}).(context.Context); ok {
		r = r.WithContext(renderCtx)
	}
	h.render(w, r, templates.Layout("Check request", h.nav(returnPath), templates.RequestMutationError(message, returnPath)))
}
