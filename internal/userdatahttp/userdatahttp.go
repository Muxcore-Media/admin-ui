// Package userdatahttp is admin-ui's only path to the userdata-local HTTP
// provider (ADR-0030, ADR-0033). Every request goes through the published
// checked client (github.com/Muxcore-Media/userdata-local/httpclient): HTTPS
// with admin-ui's own mesh identity, the fixed userdata-local server identity
// (SAN and exact CN), an origin-bound transport, no proxy and no redirects.
//
// This package adds admin-ui's side of the contract on top of that client:
// exactly one bearer and one target per request (taken from the caller's server
// session, never from a browser), strict policy envelopes, and error classes
// that keep transport or module-admission unavailability apart from the
// provider's application answers (401, 403 policy.forbidden, 404, 409, ...).
package userdatahttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/parental"
)

const (
	// ModuleID is admin-ui's mesh identity. The provider admits exactly this
	// certificate CN for policy GET/PUT and blob GET/PUT (ADR-0033 §2).
	ModuleID = "admin-ui"
	// OriginEnv configures the provider origin, e.g. https://userdata-local:9672.
	OriginEnv = "ADMIN_UI_USERDATA_URL"
	// DefaultTimeout bounds one provider request, connect through body read.
	DefaultTimeout = 5 * time.Second

	userIDHeader = "X-MuxCore-User-Id"
)

var (
	// ErrUnavailable marks every failure that is not a clean application
	// answer: no origin, no usable transport identity, TLS or connection
	// failure, timeout, redirect, module admission refusal, or an oversized,
	// malformed or mismatched answer. It must never read as "unrestricted".
	ErrUnavailable = errors.New("userdata provider unavailable")
	// ErrNotConfigured is joined with ErrUnavailable when admin-ui cannot
	// build a checked request at all (no origin, invalid origin, missing or
	// invalid mesh identity). Nothing was sent.
	ErrNotConfigured = errors.New("userdata transport not configured")
	// errNotSent is joined when a failure happened before any request was
	// sent, so a write is certain not to have been applied.
	errNotSent = errors.New("request not sent")
)

// StatusError is an application answer from the provider: a non-2xx status
// carrying the provider's own error code. Transport and admission failures are
// never StatusErrors.
type StatusError struct {
	Status int
	Code   string
}

func (e *StatusError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("userdata provider: HTTP %d (%s)", e.Status, e.Code)
	}
	return fmt.Sprintf("userdata provider: HTTP %d", e.Status)
}

func unavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnavailable, fmt.Sprintf(format, args...))
}

func notConfigured(cause error) error {
	return fmt.Errorf("%w: %w: %w: %w", ErrUnavailable, ErrNotConfigured, errNotSent, cause)
}

func notSent(format string, args ...any) error {
	return fmt.Errorf("%w: %w: %s", ErrUnavailable, errNotSent, fmt.Sprintf(format, args...))
}

// Unresolved reports that no request could be addressed (no bearer, no
// provider registered, no origin). It is unavailability and nothing was sent.
func Unresolved(reason string) error { return notSent("%s", reason) }

// Describe is a log-safe description of an unavailability: the checked
// client's reason and underlying cause. Neither carries request headers.
func Describe(err error) string {
	var ue *httpclient.UnavailableError
	if errors.As(err, &ue) {
		if ue.Cause != nil {
			return ue.Reason + ": " + ue.Cause.Error()
		}
		return ue.Reason
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

// Status returns the application status of err, or 0 for anything else.
func Status(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

// ModuleForbidden reports whether the provider refused admin-ui's verified
// mesh identity for this method and path (403 userdata.module_forbidden). That
// is a deployment problem, never the signed-in operator's role or tenant.
func ModuleForbidden(err error) bool {
	var ue *httpclient.UnavailableError
	return errors.As(err, &ue) && ue.Reason == httpclient.ReasonModuleForbidden
}

// NotConfigured reports whether no request could be built (see ErrNotConfigured).
func NotConfigured(err error) bool { return errors.Is(err, ErrNotConfigured) }

// NotApplied reports whether a failed write is certain not to have been
// applied: nothing was sent, the provider's admission layer refused it before
// any handler ran, or the provider gave a definite client-side refusal.
// Timeouts, connection loss, redirects, 5xx and mismatched acknowledgements
// leave the outcome unknown.
func NotApplied(err error) bool {
	if errors.Is(err, errNotSent) || ModuleForbidden(err) {
		return true
	}
	switch Status(err) {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusRequestEntityTooLarge:
		return true
	}
	return false
}

// Pool builds the checked client lazily, after admin-ui has enrolled (the SDK
// exports MUXCORE_TLS_CERT/KEY/CA), and rebuilds it when the origin, profile
// or identity files change. The zero value is ready to use.
type Pool struct {
	// Resolve returns the client configuration for origin. nil means
	// httpclient.FromEnv(origin, ModuleID): existing configured identity
	// files only, never enrollment.
	Resolve func(origin string) (httpclient.Config, error)

	mu     sync.Mutex
	key    poolKey
	client *httpclient.Client
}

type fileStamp struct {
	mod  time.Time
	size int64
}

type poolKey struct {
	cfg    httpclient.Config
	stamps [3]fileStamp
}

func stamp(path string) fileStamp {
	if path == "" {
		return fileStamp{}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fileStamp{size: -1}
	}
	return fileStamp{mod: fi.ModTime(), size: fi.Size()}
}

func (p *Pool) config(origin string) (httpclient.Config, error) {
	if p.Resolve != nil {
		return p.Resolve(origin)
	}
	return httpclient.FromEnv(origin, ModuleID)
}

// Insecure reports whether the transport runs in explicit insecure dev mode
// (plaintext HTTP, no module identity). It selects the scheme for a bare
// discovered address; it never relaxes verification of a configured origin.
func (p *Pool) Insecure() (bool, error) {
	cfg, err := p.config("")
	if err != nil {
		return false, notConfigured(err)
	}
	return cfg.Insecure, nil
}

// Client returns the checked client bound to origin.
func (p *Pool) Client(origin string) (*httpclient.Client, error) {
	cfg, err := p.config(origin)
	if err != nil {
		return nil, notConfigured(err)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	key := poolKey{cfg: cfg, stamps: [3]fileStamp{stamp(cfg.CertFile), stamp(cfg.KeyFile), stamp(cfg.CAFile)}}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil && p.key == key {
		return p.client, nil
	}
	c, err := httpclient.New(cfg)
	if err != nil {
		return nil, notConfigured(err)
	}
	if p.client != nil {
		p.client.CloseIdleConnections()
	}
	p.client, p.key = c, key
	return c, nil
}

// Close releases idle connections.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		p.client.CloseIdleConnections()
		p.client = nil
	}
}

// NormalizeOrigin trims a configured origin. Validation (bare origin, HTTPS
// outside explicit insecure dev) is the checked client's.
func NormalizeOrigin(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// OriginFromAdvertised turns a dialable discovery address into an origin for
// the selected mode: a bare host:port gets https://, or http:// only in
// explicit insecure dev. An address that already names a scheme is kept as is
// and must match the mode (the checked client refuses http:// in secure mode):
// it is never downgraded or rewritten. Unspecified addresses are refused.
func OriginFromAdvertised(addr string, insecure bool) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", notSent("the userdata provider advertises no address")
	}
	if strings.Contains(addr, "://") {
		return NormalizeOrigin(addr), nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return "", notSent("the userdata provider advertises an invalid address")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return "", notSent("the userdata provider advertises an unspecified address")
	}
	scheme := "https"
	if insecure {
		scheme = "http"
	}
	return (&url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}).String(), nil
}

// headers are the only request headers admin-ui sends: the session's bearer,
// the target selector and content negotiation.
func headers(bearer, target string, withBody bool) (http.Header, error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		return nil, notSent("no identity-provider bearer")
	}
	if strings.ContainsAny(bearer, " \t\r\n") || strings.ContainsAny(target, "\r\n") || strings.TrimSpace(target) == "" {
		return nil, notSent("invalid bearer or target")
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+bearer)
	h.Set(userIDHeader, target)
	h.Set("Accept", "application/json")
	if withBody {
		h.Set("Content-Type", "application/json")
	}
	return h, nil
}

var policyCodePattern = regexp.MustCompile(`^policy\.[a-z0-9_.]{1,57}$`)

// do performs one checked request and returns the 200 body. Application
// statuses become StatusError only when accept recognises the answer as the
// provider's own; anything else is unavailability.
func do(ctx context.Context, c *httpclient.Client, op httpclient.Operation, h http.Header, body []byte, accept func(int, []byte) *StatusError) ([]byte, error) {
	if c == nil {
		return nil, notSent("no userdata client")
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	resp, err := c.Do(ctx, op, h, reader)
	if err != nil {
		var ue *httpclient.UnavailableError
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, ue)
		}
		// Not an UnavailableError: the request could not be constructed.
		return nil, notSent("request could not be built")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, unavailable("unreadable response")
	}
	if resp.StatusCode != http.StatusOK {
		if se := accept(resp.StatusCode, raw); se != nil {
			return nil, se
		}
		return nil, unavailable("unrecognised HTTP %d answer", resp.StatusCode)
	}
	return raw, nil
}

// acceptPolicyStatus recognises only the policy resource's own error envelope.
// A bare status without a policy.* code (for example the 400 a TLS listener
// sends to a plaintext client, or an intermediary's 403) is not an answer
// about this account.
func acceptPolicyStatus(status int, raw []byte) *StatusError {
	var e struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &e) != nil || !policyCodePattern.MatchString(e.Code) {
		return nil
	}
	return &StatusError{Status: status, Code: e.Code}
}

// acceptBlobStatus keeps the blob endpoint's plain-text application errors.
func acceptBlobStatus(status int, _ []byte) *StatusError {
	if status >= 400 && status < 600 {
		return &StatusError{Status: status}
	}
	return nil
}

// Target names whose policy is requested and the tenant the answer must carry.
type Target struct {
	UserID   string
	TenantID string
	// AnyTenant accepts whichever tenant the provider resolved; Document
	// reports it so a caller can pin it for later requests.
	AnyTenant bool
}

// Document is a validated provider policy document.
type Document struct {
	UserID    string
	TenantID  string
	State     string // "unconfigured" or "configured"
	Revision  int64
	Policy    *parental.Policy
	UpdatedAt string
}

type envelope struct {
	UserID    string          `json:"user_id"`
	TenantID  string          `json:"tenant_id"`
	State     string          `json:"state"`
	Revision  int64           `json:"revision"`
	Policy    json.RawMessage `json:"policy"`
	UpdatedAt string          `json:"updated_at"`
}

// ParseDocument validates a response envelope strictly (ADR-0031 §3): known
// fields only, one document, a coherent state/revision/policy triple and the
// exact target (and tenant) that was requested.
func ParseDocument(raw []byte, t Target) (Document, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var env envelope
	if err := dec.Decode(&env); err != nil {
		return Document{}, unavailable("malformed document")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Document{}, unavailable("trailing data after document")
	}
	if env.UserID != t.UserID || (!t.AnyTenant && env.TenantID != t.TenantID) {
		return Document{}, unavailable("document scope does not match the request")
	}
	doc := Document{UserID: env.UserID, TenantID: env.TenantID}
	switch env.State {
	case "unconfigured":
		if env.Revision != 0 || !bytes.Equal(bytes.TrimSpace(env.Policy), []byte("null")) {
			return Document{}, unavailable("incoherent unconfigured document")
		}
		doc.State = "unconfigured"
		return doc, nil
	case "configured":
		if env.Revision <= 0 {
			return Document{}, unavailable("incoherent configured document")
		}
		p, err := parental.DecodePolicy(env.Policy)
		if err != nil {
			return Document{}, unavailable("invalid stored policy")
		}
		doc.State, doc.Revision, doc.Policy, doc.UpdatedAt = "configured", env.Revision, &p, env.UpdatedAt
		return doc, nil
	}
	return Document{}, unavailable("unknown policy state")
}

// GetPolicy reads t's policy with bearer.
func GetPolicy(ctx context.Context, c *httpclient.Client, bearer string, t Target) (Document, error) {
	h, err := headers(bearer, t.UserID, false)
	if err != nil {
		return Document{}, err
	}
	raw, err := do(ctx, c, httpclient.GetPolicy, h, nil, acceptPolicyStatus)
	if err != nil {
		return Document{}, err
	}
	return ParseDocument(raw, t)
}

// PutPolicy replaces t's policy when the stored revision still equals
// expected. The acknowledgement must be exactly the requested write.
func PutPolicy(ctx context.Context, c *httpclient.Client, bearer string, t Target, expected int64, policy parental.Policy) (Document, error) {
	h, err := headers(bearer, t.UserID, true)
	if err != nil {
		return Document{}, err
	}
	body, err := json.Marshal(parental.Update{ExpectedRevision: expected, Policy: policy})
	if err != nil {
		return Document{}, notSent("encode update")
	}
	raw, err := do(ctx, c, httpclient.PutPolicy, h, body, acceptPolicyStatus)
	if err != nil {
		return Document{}, err
	}
	doc, err := ParseDocument(raw, t)
	if err != nil {
		return Document{}, err
	}
	if doc.State != "configured" || doc.Revision != expected+1 || !PoliciesEqual(*doc.Policy, policy) {
		return Document{}, unavailable("write acknowledgement does not match the request")
	}
	return doc, nil
}

// PoliciesEqual compares two policies after provider normalization.
func PoliciesEqual(a, b parental.Policy) bool {
	na, errA := parental.Normalize(a)
	nb, errB := parental.Normalize(b)
	return errA == nil && errB == nil && reflect.DeepEqual(na, nb)
}

// GetBlob reads target's userdata blob with bearer.
func GetBlob(ctx context.Context, c *httpclient.Client, bearer, target string) ([]byte, error) {
	h, err := headers(bearer, target, false)
	if err != nil {
		return nil, err
	}
	return do(ctx, c, httpclient.GetUserdata, h, nil, acceptBlobStatus)
}

// PutBlob merges body into target's userdata blob with bearer.
func PutBlob(ctx context.Context, c *httpclient.Client, bearer, target string, body []byte) error {
	h, err := headers(bearer, target, true)
	if err != nil {
		return err
	}
	_, err = do(ctx, c, httpclient.PutUserdata, h, body, acceptBlobStatus)
	return err
}
