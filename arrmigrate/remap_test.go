package arrmigrate

import (
	"context"
	"testing"
)

func TestRemapRoot(t *testing.T) {
	cases := []struct {
		path, from, to, want string
	}{
		{"/data/media/movies", "/data/media/movies", "/library/movies", "/library/movies"},
		{"/data/media/movies/Fight Club (1999)", "/data/media/movies", "/library/movies", "/library/movies/Fight Club (1999)"},
		{"/tv", "", "/library/tv", "/library/tv"},
		{"/keep/me", "/other", "/library", "/keep/me"},
		{"/movies", "", "", "/movies"},
		{`D:\Media\Movies`, `D:\Media\Movies`, `/library/movies`, "/library/movies"},
		{"", "", "/library/movies", "/library/movies"},
	}
	for _, tc := range cases {
		got := RemapRoot(tc.path, tc.from, tc.to)
		if got != tc.want {
			t.Errorf("RemapRoot(%q, %q, %q) = %q, want %q", tc.path, tc.from, tc.to, got, tc.want)
		}
	}
}

type capturingMovies struct {
	roots []string
}

func (c *capturingMovies) ImportMovie(_ context.Context, _ string, _, _ int, _, root string, _ bool) (string, error) {
	c.roots = append(c.roots, root)
	return "mv_1", nil
}

func TestRunUsesRemappedRoot(t *testing.T) {
	items := RemapItems([]Item{
		{Source: "radarr", Title: "Fight Club", TMDBID: 550, RootFolderPath: "/data/movies"},
	}, "/data/movies", "/library/movies")
	if items[0].RootFolderPath != "/library/movies" {
		t.Fatalf("remap: %q", items[0].RootFolderPath)
	}
	cap := &capturingMovies{}
	res := Run(context.Background(), items, false, cap, nil, nil, nil)
	if res.Imported != 1 || len(cap.roots) != 1 || cap.roots[0] != "/library/movies" {
		t.Fatalf("imported=%d roots=%v errors=%v", res.Imported, cap.roots, res.Errors)
	}
}
