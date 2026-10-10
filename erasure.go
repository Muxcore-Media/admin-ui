package adminui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure"

	"github.com/Muxcore-Media/admin-ui/handler"
	"github.com/Muxcore-Media/admin-ui/session"
)

// ADR-0035 wiring: admin-ui is a personal-data owner (sessions.json,
// password-resets.json, parental.json). The disposition lives in
// handler.ErasureOwner; this file builds the SDK reconciler around it.
//
// The reconciler's only input is the erasure ledger of the provider of the
// exclusive `identity` capability, dialled with mesh mTLS and a verified
// provider CN (erasure.ProviderDialer). No event, header or request can erase
// data. Development without TLS needs MUXCORE_INSECURE_DISABLE_TLS=true: the
// SDK dialer does not honour the ADMIN_UI_INSECURE alias.

// erasureShutdownGrace bounds how long Main waits for a sweep to stop.
const erasureShutdownGrace = 5 * time.Second

// erasureProfileRequired reports whether the deployment profile makes the
// reconciler mandatory: household (and its alias staging) must not run
// without it.
func erasureProfileRequired(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("MUXCORE_PROFILE"))) {
	case "household", "staging":
		return true
	}
	return false
}

// erasureCoreAddr is the configured core address: admin-ui's own
// ADMIN_UI_CORE_ADDR, else the mesh-wide MUXCORE_GRPC_ADDR. Unlike the
// operator UI's dial, which defaults to localhost, an unset address here means
// "no core connection configured".
func erasureCoreAddr(getenv func(string) string) string {
	for _, k := range []string{"ADMIN_UI_CORE_ADDR", "MUXCORE_GRPC_ADDR"} {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// providerUsers lists the identity provider's accounts through the verified
// provider connection. It serves only the legacy password-reset purge.
type providerUsers struct {
	dialer   *erasure.ProviderDialer
	moduleID string
}

var _ handler.ErasureUsers = providerUsers{}

func (p providerUsers) ListUsers(ctx context.Context) ([]*authv1.UserInfo, error) {
	conn, err := p.dialer.Dial(ctx, p.moduleID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	resp, err := conn.Client.ListUsers(ctx, &authv1.ListUsersRequest{})
	if err != nil {
		return nil, fmt.Errorf("list users from %s: %w", conn.Provider.ModuleID, err)
	}
	return resp.GetUsers(), nil
}

// erasureSetup is the input of newErasureReconciler.
type erasureSetup struct {
	Getenv   func(string) string
	ModuleID string
	Sessions *session.Store
	// Finder is core's DiscoveryService client; nil when there is no core
	// connection.
	Finder erasure.CapabilityFinder
	// Interval overrides ERASURE_SWEEP_INTERVAL (tests).
	Interval time.Duration
}

// newErasureReconciler builds the reconciler, or returns (nil, nil) when it
// is disabled. The household and staging profiles refuse to start without a
// core connection (the caller exits); other profiles warn and run without it.
func newErasureReconciler(in erasureSetup) (*erasure.Reconciler, error) {
	if erasureCoreAddr(in.Getenv) == "" || in.Finder == nil {
		if erasureProfileRequired(in.Getenv) {
			return nil, errors.New("erasure reconciler: the household profile requires a core connection (set ADMIN_UI_CORE_ADDR or MUXCORE_GRPC_ADDR)")
		}
		slog.Warn("admin-ui erasure reconciler disabled: no core connection configured; user erasures from the identity ledger are not applied")
		return nil, nil
	}
	dialer := &erasure.ProviderDialer{Discovery: in.Finder, Getenv: in.Getenv}
	owner := handler.NewErasureOwner(in.ModuleID, in.Sessions, providerUsers{dialer: dialer, moduleID: in.ModuleID})
	return erasure.New(erasure.Config{
		Owner:    owner,
		Dialer:   dialer,
		Logger:   slog.Default(),
		Getenv:   in.Getenv,
		Interval: in.Interval,
	})
}

// runErasure starts rec and returns a stop function that cancels it and waits
// (bounded) for a running sweep to return.
func runErasure(rec *erasure.Reconciler) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := rec.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("admin-ui erasure reconciler stopped", "error", err)
		}
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(erasureShutdownGrace):
			slog.Warn("admin-ui erasure reconciler did not stop before the shutdown grace period")
		}
	}
}
