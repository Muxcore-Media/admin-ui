// Package parentalseed is the admin-bearer seed helper for the household smoke
// (ADR-0033 §4, slice S9c): `admin-ui parental-seed`. It gives one account an
// explicit `unrestricted` parental policy when, and only when, the account has
// no policy yet, using admin-ui's own existing mesh identity and the published
// checked userdata client.
//
// It runs as a separate process inside admin-ui's service context. It resolves
// only existing identity files (httpclient.FromEnv: MUXCORE_TLS_CERT/KEY/CA or
// MUXCORE_TLS_DIR / $MUXCORE_DATA_DIR/mesh-id): it never enrolls, never reads a
// bootstrap token, never contacts core or the identity provider, never opens
// admin-ui's data files and never starts the daemon. The operator's bearer is
// read from a file or stdin, never argv, and is never printed.
package parentalseed

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/parental"

	"github.com/Muxcore-Media/admin-ui/internal/userdatahttp"
)

// Command is the admin-ui subcommand name.
const Command = "parental-seed"

// Exit codes. Every code other than ExitOK leaves the account's policy as the
// provider holds it; only ExitUncertain cannot say whether a write landed.
const (
	ExitOK            = 0 // account now has a configured unrestricted policy (set now or already)
	ExitUsage         = 2 // bad arguments, unreadable bearer, invalid origin or identity; nothing sent
	ExitRestricted    = 3 // account already has a restricted policy; left untouched
	ExitDenied        = 4 // provider application refusal (401, 403 policy.forbidden, 404, 400)
	ExitUnavailable   = 5 // transport/TLS failure, redirect or unusable answer
	ExitNotPermitted  = 6 // module admission refused admin-ui's mesh identity
	ExitUncertain     = 7 // a write was sent but its outcome is unknown and could not be re-read
	ExitStillConflict = 8 // revision conflicts on every attempt
)

const maxBearerBytes = 16 << 10

// Getenv reads one environment variable.
type Getenv func(string) string

// Main runs the helper with args (excluding the subcommand name) and returns
// the process exit code.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv Getenv) int {
	return run(args, stdin, stdout, stderr, getenv, nil)
}

// resolveFunc lets tests supply client configuration; nil means
// httpclient.FromEnv(origin, "admin-ui").
type resolveFunc func(origin string) (httpclient.Config, error)

type output struct {
	stdout, stderr io.Writer
	redact         string
}

func (o *output) clean(s string) string {
	if o.redact != "" {
		s = strings.ReplaceAll(s, o.redact, "[REDACTED]")
	}
	return s
}

func (o *output) okf(format string, args ...any) {
	_, _ = fmt.Fprintln(o.stdout, o.clean(fmt.Sprintf(format, args...)))
}

func (o *output) failf(code int, format string, args ...any) int {
	_, _ = fmt.Fprintln(o.stderr, o.clean(Command+": "+fmt.Sprintf(format, args...)))
	return code
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv Getenv, resolve resolveFunc) int {
	out := &output{stdout: stdout, stderr: stderr}
	fs := flag.NewFlagSet(Command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	userID := fs.String("user", "", "target account ID (required)")
	tenant := fs.String("tenant", "", "expected tenant ID; empty pins the tenant from the first answer")
	origin := fs.String("origin", "", "userdata-local origin; default $"+userdatahttp.OriginEnv)
	bearerFile := fs.String("bearer-file", "", "file holding the admin's identity-provider bearer; - reads stdin (required)")
	timeout := fs.Duration("timeout", userdatahttp.DefaultTimeout, "per-request deadline (at most 30s)")
	attempts := fs.Int("attempts", 3, "read/write attempts on revision conflicts (1-10)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "usage: admin-ui %s --user ID --bearer-file PATH|- [--origin URL] [--tenant ID] [--timeout D] [--attempts N]\n", Command)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 0 {
		return out.failf(ExitUsage, "unexpected arguments")
	}
	if strings.TrimSpace(*userID) == "" || strings.ContainsAny(*userID, "\r\n") {
		return out.failf(ExitUsage, "--user is required")
	}
	if *bearerFile == "" {
		return out.failf(ExitUsage, "--bearer-file is required (use - for stdin); the bearer is never accepted as an argument")
	}
	if *attempts < 1 || *attempts > 10 {
		return out.failf(ExitUsage, "--attempts must be between 1 and 10")
	}
	if *timeout <= 0 || *timeout > httpclient.MaxTimeout {
		return out.failf(ExitUsage, "--timeout must be positive and at most %s", httpclient.MaxTimeout)
	}
	bearer, err := readBearer(*bearerFile, stdin)
	if err != nil {
		return out.failf(ExitUsage, "%v", err)
	}
	out.redact = bearer

	o := userdatahttp.NormalizeOrigin(*origin)
	if o == "" {
		o = userdatahttp.NormalizeOrigin(getenv(userdatahttp.OriginEnv))
	}
	if o == "" {
		return out.failf(ExitUsage, "no userdata origin: pass --origin or set %s (e.g. https://userdata-local:9672)", userdatahttp.OriginEnv)
	}
	pool := &userdatahttp.Pool{Resolve: func(origin string) (httpclient.Config, error) {
		var cfg httpclient.Config
		var err error
		if resolve != nil {
			cfg, err = resolve(origin)
		} else {
			cfg, err = httpclient.FromEnv(origin, userdatahttp.ModuleID)
		}
		cfg.Timeout = *timeout
		return cfg, err
	}}
	client, err := pool.Client(o)
	if err != nil {
		return out.failf(ExitUsage, "cannot build the checked userdata client (an existing admin-ui mesh identity and an https origin are required; nothing was sent): %v", err)
	}
	defer pool.Close()

	s := &seeder{client: client, bearer: bearer, target: userdatahttp.Target{UserID: *userID, TenantID: *tenant, AnyTenant: *tenant == ""}, out: out}
	return s.run(*attempts)
}

// readBearer reads one bearer line. It rejects empty, multi-line, oversized
// or whitespace-containing input.
func readBearer(path string, stdin io.Reader) (string, error) {
	var r io.Reader
	if path == "-" {
		r = stdin
	} else {
		f, err := os.Open(path) //nolint:gosec // operator-chosen protected input
		if err != nil {
			return "", fmt.Errorf("cannot read the bearer file")
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	raw, err := io.ReadAll(io.LimitReader(bufio.NewReader(r), maxBearerBytes+1))
	if err != nil || len(raw) > maxBearerBytes {
		return "", fmt.Errorf("cannot read the bearer (unreadable or larger than %d bytes)", maxBearerBytes)
	}
	bearer := strings.TrimSpace(string(raw))
	if strings.HasPrefix(bearer, "Bearer ") {
		bearer = strings.TrimSpace(strings.TrimPrefix(bearer, "Bearer "))
	}
	if bearer == "" || strings.ContainsAny(bearer, " \t\r\n") {
		return "", fmt.Errorf("the bearer input must hold exactly one token")
	}
	return bearer, nil
}

type seeder struct {
	client *httpclient.Client
	bearer string
	target userdatahttp.Target
	out    *output
}

var unrestricted = parental.Policy{Version: 1, Mode: "unrestricted"}

func (s *seeder) read() (userdatahttp.Document, error) {
	doc, err := userdatahttp.GetPolicy(context.Background(), s.client, s.bearer, s.target)
	if err == nil && s.target.AnyTenant {
		// Every later answer must be about the same tenant.
		s.target.TenantID, s.target.AnyTenant = doc.TenantID, false
	}
	return doc, err
}

func (s *seeder) run(attempts int) int {
	user := s.target.UserID
	uncertain := false
	for attempt := 1; attempt <= attempts; attempt++ {
		uncertain = false
		doc, err := s.read()
		if err != nil {
			return s.fail("read", err)
		}
		if code, done := s.settled(doc, "unchanged"); done {
			return code
		}
		_, err = userdatahttp.PutPolicy(context.Background(), s.client, s.bearer, s.target, 0, unrestricted)
		switch {
		case err == nil:
			s.out.okf("OK parental policy %s: set unrestricted (revision 1, attempt %d)", user, attempt)
			return ExitOK
		case userdatahttp.Status(err) == http.StatusConflict:
			s.out.okf("parental policy %s: revision conflict on attempt %d, re-reading", user, attempt)
			continue
		case userdatahttp.NotApplied(err):
			return s.fail("write", err)
		}
		// The write may or may not have landed: read back what is stored.
		s.out.okf("parental policy %s: no definite answer to the write (%s), re-reading", user, reasonOf(err))
		doc, rerr := s.read()
		if rerr != nil {
			return s.out.failf(ExitUncertain, "%s: the write may or may not have been applied and the policy could not be re-read (%s); run the helper again", user, reasonOf(rerr))
		}
		if code, done := s.settled(doc, "after an uncertain write"); done {
			return code
		}
		uncertain = true
	}
	if uncertain {
		return s.out.failf(ExitUncertain, "%s: no write was confirmed and the account is still unconfigured after %d attempts; run the helper again", user, attempts)
	}
	return s.out.failf(ExitStillConflict, "%s: the policy kept changing during %d attempts; nothing was overwritten", user, attempts)
}

// settled reports the final exit code when doc needs no write.
func (s *seeder) settled(doc userdatahttp.Document, how string) (int, bool) {
	if doc.State != "configured" {
		return 0, false
	}
	user := s.target.UserID
	if doc.Policy.Mode == "unrestricted" {
		s.out.okf("OK parental policy %s: unrestricted (revision %d, %s)", user, doc.Revision, how)
		return ExitOK, true
	}
	return s.out.failf(ExitRestricted, "%s has a restricted parental policy (revision %d); it was not overwritten", user, doc.Revision), true
}

func reasonOf(err error) string {
	if st := userdatahttp.Status(err); st != 0 {
		var se *userdatahttp.StatusError
		if errors.As(err, &se) && se.Code != "" {
			return fmt.Sprintf("HTTP %d %s", st, se.Code)
		}
		return fmt.Sprintf("HTTP %d", st)
	}
	return userdatahttp.Describe(err)
}

func (s *seeder) fail(op string, err error) int {
	user := s.target.UserID
	switch {
	case userdatahttp.ModuleForbidden(err):
		return s.out.failf(ExitNotPermitted, "%s %s: userdata unavailable, not permitted for this service (userdata-local refused admin-ui's mesh identity); nothing was written", op, user)
	case userdatahttp.NotConfigured(err):
		return s.out.failf(ExitUsage, "%s %s: %s", op, user, reasonOf(err))
	}
	switch userdatahttp.Status(err) {
	case http.StatusUnauthorized:
		return s.out.failf(ExitDenied, "%s %s: the identity provider rejected the bearer (%s)", op, user, reasonOf(err))
	case http.StatusForbidden:
		return s.out.failf(ExitDenied, "%s %s: refused (%s); the bearer must belong to an admin in the account's tenant", op, user, reasonOf(err))
	case http.StatusNotFound:
		return s.out.failf(ExitDenied, "%s %s: account not found (%s)", op, user, reasonOf(err))
	case 0:
		return s.out.failf(ExitUnavailable, "%s %s: userdata unavailable (%s)", op, user, reasonOf(err))
	}
	return s.out.failf(ExitDenied, "%s %s: refused (%s)", op, user, reasonOf(err))
}
