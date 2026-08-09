package handler

import "testing"

func TestNormalizeDialAddr(t *testing.T) {
	t.Setenv("MUXCORE_MESH_DIAL_LOCAL", "")
	if got := normalizeDialAddr("media-automation", ":9460"); got != "media-automation:9460" {
		t.Fatalf("compose dial: got %q", got)
	}
	if got := normalizeDialAddr("x", "127.0.0.1:9460"); got != "127.0.0.1:9460" {
		t.Fatalf("explicit host: got %q", got)
	}
	t.Setenv("MUXCORE_MESH_DIAL_LOCAL", "true")
	if got := normalizeDialAddr("media-automation", ":9460"); got != "127.0.0.1:9460" {
		t.Fatalf("local dial: got %q", got)
	}
}
