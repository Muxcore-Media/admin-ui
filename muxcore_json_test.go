package adminui

import (
	"encoding/json"
	"os"
	"testing"
)

func TestMuxcoreJSONPresent(t *testing.T) {
	raw, err := os.ReadFile("muxcore.json")
	if err != nil {
		t.Fatalf("muxcore.json required for spool: %v", err)
	}
	var meta struct {
		Name           string   `json:"name"`
		Description    string   `json:"description"`
		Version        string   `json:"version"`
		Author         string   `json:"author"`
		Roles          []string `json:"roles"`
		Capabilities   []string `json:"capabilities"`
		MinCoreVersion string   `json:"minCoreVersion"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("muxcore.json: %v", err)
	}
	if meta.Name == "" || meta.Version == "" {
		t.Fatal("muxcore.json must set name and version")
	}
	if len(meta.Capabilities) == 0 {
		t.Fatal("muxcore.json must advertise at least one capability")
	}
	if meta.MinCoreVersion == "" {
		t.Fatal("muxcore.json must set minCoreVersion")
	}
}
