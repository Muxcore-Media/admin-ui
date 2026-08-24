package templates

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	rootsv1 "github.com/Muxcore-Media/media-root-folders/proto/rootsv1"
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

func TestUsersPage_HasHeadingTableCaptionAndAlerts(t *testing.T) {
	html := renderComponent(t, UsersPage([]*authv1.UserInfo{{
		Username: "alice",
		Id:       "user-1",
	}}, "Could not load users", []PasswordResetRow{{
		Username: "bob",
	}}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Users</h1>`,
		`id="users-resets-heading"`,
		`aria-labelledby="users-resets-heading"`,
		`<caption class="sr-only">Registered users</caption>`,
		`scope="col"`,
		`role="alert"`,
		`Could not load users`,
		`aria-label="Manage user alice"`,
		`aria-label="Delete user alice"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("users page HTML missing %q", want)
		}
	}
}

func TestInvitesPage_HasLabelledSectionsAndAlerts(t *testing.T) {
	html := renderComponent(t, InvitesPage(InvitesPageData{
		Error: "auth failed",
		Invites: []InviteRow{{
			ID:     "inv-1",
			Prefix: "abc123",
			Role:   "user",
		}},
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Invite links</h1>`,
		`role="alert"`,
		`auth failed`,
		`id="invites-list-heading"`,
		`aria-labelledby="invites-list-heading"`,
		`for="invite-role"`,
		`id="invite-role"`,
		`aria-label="Revoke invite abc123"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("invites page HTML missing %q", want)
		}
	}
}

func TestLibrariesPage_HasSemanticListAndNav(t *testing.T) {
	html := renderComponent(t, LibrariesLivePage([]LibraryModRow{{
		ID:   "lib-1",
		Name: "Movies",
		Path: "/media/movies",
	}}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Libraries</h1>`,
		`id="libraries-list-heading"`,
		`aria-labelledby="libraries-list-heading"`,
		`<ul`,
		`aria-label="Open library Movies"`,
		`aria-label="Library configuration"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("libraries page HTML missing %q", want)
		}
	}
}

func TestRequestPage_HasLabelledSectionsAndFormLabels(t *testing.T) {
	html := renderComponent(t, RequestPage(RequestPageData{
		Error:   "search unavailable",
		Pending: []RequestRow{{ID: "r1", Title: "Inception", Year: 2010, ItemType: "movie", RequestedBy: "alice"}},
		Results: []RequestSearchHit{{ID: 1, Title: "Fight Club", Year: 1999, Type: "movie"}},
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Request Media</h1>`,
		`role="alert"`,
		`search unavailable`,
		`id="request-pending-heading"`,
		`for="request-search-query"`,
		`aria-label="Approve request for Inception"`,
		`aria-label="Request Fight Club"`,
		`id="request-history-heading"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("request page HTML missing %q", want)
		}
	}
}

func TestQueuePage_HasLiveRegionTableCaptionAndSections(t *testing.T) {
	html := renderComponent(t, UnifiedQueuePage(UnifiedQueueData{
		Error:        "queue offline",
		FailureCount: 1,
		Failures: []UnifiedQueueHistory{{
			ID:    "h1",
			Title: "Stuck import",
			Stuck: true,
		}},
		Wanted: []UnifiedQueueWanted{{
			ID:    "w1",
			Title: "Dune",
		}},
		Total: 1,
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Queue</h1>`,
		`aria-live="polite"`,
		`role="alert"`,
		`queue offline`,
		`id="queue-failures-heading"`,
		`id="queue-wanted-heading"`,
		`id="queue-history-heading"`,
		`<caption class="sr-only">Wanted queue items</caption>`,
		`scope="col"`,
		`for="queue-filter"`,
		`aria-label="Remove Dune from queue"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("queue page HTML missing %q", want)
		}
	}
}

func TestCalendarPage_HasHeadingFilterLabelsAndTableCaption(t *testing.T) {
	html := renderComponent(t, UnifiedCalendarPage(UnifiedCalendarData{
		Error:   "calendar offline",
		Warning: "partial data",
		Start:   "2026-01-01",
		End:     "2026-01-31",
		Items: []UnifiedCalendarItem{{
			ModuleID:   "lib-1",
			ModuleName: "Movies",
			ParentID:   "item-1",
			Title:      "Dune",
			Date:       "2026-01-15",
			HasFile:    true,
		}},
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Calendar</h1>`,
		`role="alert"`,
		`calendar offline`,
		`role="status"`,
		`partial data`,
		`id="calendar-filter-heading"`,
		`for="calendar-start"`,
		`for="calendar-end"`,
		`for="calendar-unmonitored"`,
		`id="calendar-items-heading"`,
		`<caption class="sr-only">Upcoming calendar items</caption>`,
		`scope="col"`,
		`aria-label="Open Dune"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("calendar page HTML missing %q", want)
		}
	}
}

func TestBackupsPage_HasAlertsSectionsAndFormLabels(t *testing.T) {
	html := renderComponent(t, BackupsLivePage([]BackupRow{{
		ID:        "bak-1",
		Timestamp: "2026-01-01",
		Size:      "10 MB",
	}}, "backup module unavailable", "Backup created"))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Backups</h1>`,
		`role="status"`,
		`Backup created`,
		`role="alert"`,
		`backup module unavailable`,
		`id="backups-list-heading"`,
		`aria-labelledby="backups-list-heading"`,
		`aria-label="Delete backup bak-1"`,
		`aria-label="Restore backup bak-1"`,
		`id="backup-restore-path-bak-1"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("backups page HTML missing %q", want)
		}
	}
}

func TestBrandingPage_HasAccessibleFormAndAlerts(t *testing.T) {
	html := renderComponent(t, BrandingEditPage(BrandingData{
		ServerName: "MuxCore",
		Saved:      true,
		Error:      "save failed",
		CustomCSS:  "body { color: red; }",
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Branding</h1>`,
		`role="status"`,
		`Saved.`,
		`role="alert"`,
		`save failed`,
		`aria-label="Branding settings"`,
		`id="branding-server-name"`,
		`id="branding-login-banner"`,
		`id="branding-splash-url"`,
		`id="branding-custom-css"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("branding page HTML missing %q", want)
		}
	}
}

func TestActivityPage_HasFilterLabelsTableCaptionAndSections(t *testing.T) {
	html := renderComponent(t, ActivityPage(ActivityPageData{
		EventType: "grab",
		Entries: []ActivityEntry{{
			ID:         "act-1",
			EventType:  "grab",
			Title:      "Inception",
			ModuleID:   "lib-1",
			ItemID:     "item-1",
			ModuleName: "Movies",
			CreatedAt:  "2026-01-01",
		}},
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Activity</h1>`,
		`id="activity-filter-heading"`,
		`for="activity-event-type"`,
		`id="activity-event-type"`,
		`id="activity-table-heading"`,
		`<caption class="sr-only">Activity history</caption>`,
		`scope="col"`,
		`aria-label="Open Inception"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("activity page HTML missing %q", want)
		}
	}
}

func TestRootsPage_HasAlertsSectionsAndActionLabels(t *testing.T) {
	html := renderComponent(t, RootsListPage([]*rootsv1.RootFolder{{
		Id:         "root-1",
		Name:       "Movies",
		Path:       "/media/movies",
		MediaKind:  "movies",
		Accessible: true,
	}}, "Roots module unavailable"))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Root Folders</h1>`,
		`role="alert"`,
		`Roots module unavailable`,
		`id="roots-list-heading"`,
		`aria-labelledby="roots-list-heading"`,
		`aria-label="Add root folder"`,
		`aria-label="Edit root folder Movies"`,
		`aria-label="Delete root folder Movies"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("roots page HTML missing %q", want)
		}
	}
}
