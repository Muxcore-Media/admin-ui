package branding

import "testing"

func TestDefaultTheme_HasMuxCoreCSSVariables(t *testing.T) {
	t.Parallel()

	vars := DefaultTheme().CSSVariables()
	for _, key := range []string{
		"--muxcore-color-primary",
		"--muxcore-color-accent",
		"--muxcore-color-background",
		"--muxcore-color-text",
	} {
		if vars[key] == "" {
			t.Fatalf("CSSVariables missing %q", key)
		}
	}
	if vars["--muxcore-color-primary"] != "#3B82F6" {
		t.Fatalf("primary = %q", vars["--muxcore-color-primary"])
	}
}

func TestThemeCSSVariables_MatchesThemeJSON(t *testing.T) {
	t.Parallel()

	fromJSON := ThemeCSSVariables()
	fromTheme := DefaultTheme().CSSVariables()
	if len(fromJSON) == 0 {
		t.Fatal("ThemeCSSVariables returned empty map")
	}
	for key, want := range fromTheme {
		if got := fromJSON[key]; got != want {
			t.Fatalf("%s: json=%q theme=%q", key, got, want)
		}
	}
}

func TestProductIdentity(t *testing.T) {
	t.Parallel()

	if ProductName != "MuxCore" || ProductTitle != "MuxCore Admin" {
		t.Fatalf("unexpected product identity: name=%q title=%q", ProductName, ProductTitle)
	}
}
