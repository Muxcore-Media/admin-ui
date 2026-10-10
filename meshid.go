package adminui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Muxcore-Media/core/sdk/go/module/meshid"

	"github.com/Muxcore-Media/admin-ui/internal/meshdial"
)

// defaultMeshModuleID is admin-ui's mesh identity (certificate CN) when
// MUXCORE_MODULE_ID is unset.
const defaultMeshModuleID = "admin-ui"

// ensureFunc is meshid.Ensure (swapped in tests).
type ensureFunc func(context.Context, meshid.Config) (meshid.Paths, error)

// ensureMeshIdentity gives admin-ui its mesh identity before the first gRPC
// dial (ADR-0017). It reuses MUXCORE_TLS_CERT/KEY or the certificate stored
// in MUXCORE_TLS_DIR (default <MUXCORE_DATA_DIR>/mesh-id), or enrolls with
// core at coreAddr using MUXCORE_BOOTSTRAP_TOKEN. meshid exports
// MUXCORE_TLS_CERT/KEY/CA, which internal/meshdial reads for every dial (core
// and peer modules), so it must run before meshdial's process-wide config is
// first loaded. Insecure dev mode (MUXCORE_INSECURE_DISABLE_TLS or the
// deprecated aliases meshdial honours) skips it; the insecure flag in the
// household profile is an error (ADR-0016).
func ensureMeshIdentity(ctx context.Context, ensure ensureFunc, getenv func(string) string, coreAddr string) (meshid.Paths, error) {
	moduleID := meshModuleID(getenv)
	paths, err := ensure(ctx, meshid.Config{
		Getenv:   getenv,
		ModuleID: moduleID,
		GRPCAddr: strings.TrimSpace(coreAddr),
		Insecure: insecureFrom(getenv),
	})
	if err != nil {
		return meshid.Paths{}, fmt.Errorf("mesh identity for %q: %w", moduleID, err)
	}
	return paths, nil
}

// meshModuleID is admin-ui's module id: the CN of its mesh certificate, and
// the identity the ADR-0035 reconciler acknowledges erasures as.
func meshModuleID(getenv func(string) string) string {
	if id := strings.TrimSpace(getenv(meshid.EnvModuleID)); id != "" {
		return id
	}
	return defaultMeshModuleID
}

// insecureFrom mirrors meshdial.ConfigFromEnv's insecure flags over getenv.
func insecureFrom(getenv func(string) string) bool {
	for _, k := range []string{meshdial.EnvInsecure, meshdial.EnvInsecureLegacy, meshdial.EnvAdminInsecure} {
		if v := strings.ToLower(strings.TrimSpace(getenv(k))); v == "true" || v == "1" {
			return true
		}
	}
	return false
}
