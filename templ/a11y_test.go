package templates

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func renderComponent(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), io.Writer(&buf)); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestLayout_HasSkipLinkAndMainLandmark(t *testing.T) {
	html := renderComponent(t, Layout("Dashboard", Nav(nil, "/"), DashboardPage(DashboardData{})))

	for _, want := range []string{
		`href="#main-content"`,
		`Skip to main content`,
		`<main id="main-content"`,
		`id="sidebar"`,
		`aria-label="Admin navigation"`,
		`id="loading-bar"`,
		`aria-live="polite"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("layout HTML missing %q", want)
		}
	}
}

func TestLoginPage_HasAccessibleFormAndAlert(t *testing.T) {
	html := renderComponent(t, LoginPage("Invalid credentials"))

	for _, want := range []string{
		`<main id="main-content"`,
		`Skip to sign in form`,
		`id="login-form"`,
		`aria-label="Sign in"`,
		`role="alert"`,
		`Invalid credentials`,
		`<h1 class="text-2xl font-bold">MuxCore Admin</h1>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("login HTML missing %q", want)
		}
	}
}

func TestDashboardPage_HasHeadingAndLabelledSections(t *testing.T) {
	html := renderComponent(t, DashboardPage(DashboardData{
		QueueFailureCount: 2,
		WantedQueueCount:  5,
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Dashboard</h1>`,
		`id="dashboard-queue-heading"`,
		`id="dashboard-calendar-heading"`,
		`id="dashboard-tasks-heading"`,
		`id="dashboard-health-heading"`,
		`id="dashboard-monitor-heading"`,
		`role="alert"`,
		`<section`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("dashboard HTML missing %q", want)
		}
	}
}

func TestErrorPages_UsePageHeading(t *testing.T) {
	for _, c := range []templ.Component{NotFound(), Forbidden()} {
		html := renderComponent(t, c)
		if !strings.Contains(html, "<h1") {
			t.Fatalf("error page missing h1: %s", html)
		}
	}
}

func TestModuleTable_HasCaptionAndColumnScopes(t *testing.T) {
	html := renderComponent(t, ModuleTable([]ModuleListItem{{
		ID: "core", Name: "Core", Version: "1.0.0", State: "running",
	}}))

	for _, want := range []string{
		`<caption class="sr-only">Registered modules</caption>`,
		`scope="col"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("module table HTML missing %q", want)
		}
	}
}

func TestSettingsFieldForm_LinksDescriptionToInput(t *testing.T) {
	html := renderComponent(t, SettingFieldForm(SettingField{
		Key:         "timeout",
		Label:       "Request timeout",
		Type:        "int",
		Description: "Seconds before giving up",
		Required:    true,
	}, "test-module"))

	for _, want := range []string{
		`id="desc-timeout"`,
		`aria-describedby="desc-timeout"`,
		`aria-label="Request timeout"`,
		`role="status"`,
		`(required)`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("settings form HTML missing %q", want)
		}
	}
}
