package templates

import (
	"os"
	"strings"
	"testing"
)

func TestInputCSS_ContainsMuxCoreBrandingTokens(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../input.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, want := range []string{
		"--muxcore-color-primary:",
		"--muxcore-color-accent:",
		"--muxcore-color-background:",
		"--accent-color: var(--muxcore-color-primary)",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("input.css missing %q", want)
		}
	}
}

func TestNavBrand_HasLogoMarkup(t *testing.T) {
	t.Parallel()

	html := renderComponent(t, NavBrand())
	for _, want := range []string{
		`data-testid="nav-brand"`,
		`src="/static/branding/logo-mark.svg"`,
		`alt="MuxCore"`,
		`>Admin</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("nav brand HTML missing %q", want)
		}
	}
}

func TestLoginBrandHeader_HasLogoMarkup(t *testing.T) {
	t.Parallel()

	html := renderComponent(t, LoginBrandHeader())
	for _, want := range []string{
		`data-testid="login-brand"`,
		`src="/static/branding/logo-mark.svg"`,
		`<h1 class="text-2xl font-bold">MuxCore Admin</h1>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("login brand HTML missing %q", want)
		}
	}
}
