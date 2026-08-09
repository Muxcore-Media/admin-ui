package handler

import (
	"testing"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func TestFilterEventItems(t *testing.T) {
	now := time.Now()
	in := []templates.EventItem{
		{Type: "module.degraded", Source: "health-monitor", Timestamp: now},
		{Type: "health.smoke", Source: "health-monitor", Timestamp: now},
		{Type: "download.completed", Source: "media-automation", Timestamp: now},
		{Type: "media.movie.added", Source: "media-movies", Timestamp: now},
	}
	health := filterEventItems(in, "health")
	if len(health) != 2 {
		t.Fatalf("health filter: got %d", len(health))
	}
	deg := filterEventItems(in, "degraded")
	if len(deg) != 1 || deg[0].Type != "module.degraded" {
		t.Fatalf("degraded filter: %+v", deg)
	}
	dl := filterEventItems(in, "download")
	if len(dl) != 1 || dl[0].Type != "download.completed" {
		t.Fatalf("download filter: %+v", dl)
	}
	all := filterEventItems(in, "all")
	if len(all) != 4 {
		t.Fatalf("all filter: got %d", len(all))
	}
}
