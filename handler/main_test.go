package handler

import (
	"os"
	"testing"
)

// TestMain runs handler tests against in-process plaintext gRPC fakes, so the
// mesh dial helper is put in its dev (insecure) mode before first use.
func TestMain(m *testing.M) {
	if err := os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
