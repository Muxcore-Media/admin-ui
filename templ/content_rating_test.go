package templates

import (
	"strings"
	"testing"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
)

func TestContentRatingLibraryEntry(t *testing.T) {
	for _, module := range []string{"media-movies", "media-tvshows", "media-music"} {
		t.Run(module, func(t *testing.T) {
			body := renderComponent(t, MediaDetailPage(&mediaadminv1.MediaItem{Id: "fixture", Title: "Fixture"}, module, nil, nil, nil, nil, module, nil, nil, nil, nil, nil, "", "", ""))
			want := module != "media-music"
			if got := strings.Contains(body, "/media/"+module+"/item/fixture/content-rating"); got != want {
				t.Fatalf("content rating entry present=%v, want %v", got, want)
			}
		})
	}
}
