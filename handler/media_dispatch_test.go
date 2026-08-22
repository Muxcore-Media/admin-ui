package handler

import (
	"testing"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
)

func TestAutomationItemType(t *testing.T) {
	tests := []struct {
		moduleID, displayName, want string
	}{
		{"media-movies", "Movies", "movie"},
		{"media-tvshows", "TV Shows", "tv"},
		{"library", "Music", "music"},
	}
	for _, tt := range tests {
		if got := automationItemType(tt.moduleID, tt.displayName); got != tt.want {
			t.Errorf("automationItemType(%q, %q) = %q, want %q", tt.moduleID, tt.displayName, got, tt.want)
		}
	}
}

func TestItemTMDBID(t *testing.T) {
	if got := itemTMDBID(nil); got != 0 {
		t.Fatalf("nil item: got %d", got)
	}
	item := &mediaadminv1.MediaItem{
		Metadata: map[string]string{"tmdb_id": "550"},
	}
	if got := itemTMDBID(item); got != 550 {
		t.Fatalf("got %d, want 550", got)
	}
}
