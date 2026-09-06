package templates

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"
	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"
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
		`hx-disinherit="hx-select"`,
		`id="sidebar"`,
		`aria-label="Admin navigation"`,
		`id="loading-bar"`,
		`aria-live="polite"`,
		`src="/static/branding/logo-mark.svg"`,
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
		`data-testid="login-brand"`,
		`src="/static/branding/logo-mark.svg"`,
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
	}}, "Could not load users", 2))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Users</h1>`,
		`data-testid="password-reset-banner"`,
		`href="/password-resets"`,
		`<caption class="sr-only">Registered users</caption>`,
		`scope="col"`,
		`role="alert"`,
		`Could not load users`,
		`aria-label="Manage user alice"`,
		`aria-label="Delete user alice"`,
		`type="button"`,
		`hx-get="/users/create-form"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("users page HTML missing %q", want)
		}
	}
}

func TestPasswordResetsPage_HasHeadingListAndStates(t *testing.T) {
	html := renderComponent(t, PasswordResetsPage(PasswordResetsPageData{
		Rows: []PasswordResetRow{{
			ID:       "req-1",
			Username: "alice",
			Note:     "locked out",
			UserID:   "user-1",
		}},
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Password reset queue</h1>`,
		`data-testid="password-reset-row"`,
		`data-testid="password-reset-set-password"`,
		`data-testid="password-reset-dismiss"`,
		`aria-label="Dismiss reset request for alice"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("password resets page HTML missing %q", want)
		}
	}

	emptyHTML := renderComponent(t, PasswordResetsPage(PasswordResetsPageData{SoftEmpty: true}))
	if !strings.Contains(emptyHTML, `data-testid="password-resets-empty"`) {
		t.Fatal("expected empty state on password resets page")
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
	sched := BackupScheduleData{
		CronExpr:       "0 2 * * *",
		RetentionCount: 7,
		RetentionDays:  30,
		Enabled:        true,
		NextRun:        "01 Jan 26 02:00 UTC",
		LastRun:        "31 Dec 25 02:00 UTC",
		LastRunStatus:  "ok",
	}
	html := renderComponent(t, BackupsLivePage([]BackupRow{{
		ID:        "bak-1",
		Timestamp: "2026-01-01",
		Size:      "10 MB",
	}}, "backup module unavailable", "Backup created", sched))

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

func TestBackupsScheduleSection_EnabledState(t *testing.T) {
	sched := BackupScheduleData{
		CronExpr:       "0 2 * * *",
		RetentionCount: 7,
		RetentionDays:  30,
		Enabled:        true,
		NextRun:        "01 Jan 26 02:00 UTC",
		LastRun:        "31 Dec 25 02:00 UTC",
		LastRunStatus:  "ok",
	}
	html := renderComponent(t, BackupsLivePage(nil, "", "", sched))

	for _, want := range []string{
		`aria-labelledby="backup-schedule-heading"`,
		`id="backup-schedule-heading"`,
		`data-testid="schedule-status-enabled"`,
		`data-testid="schedule-form"`,
		`aria-label="Backup schedule configuration"`,
		`name="cron_expr"`,
		`name="retention_count"`,
		`name="retention_days"`,
		`name="enabled"`,
		`action="/backups/schedule"`,
		`data-testid="schedule-save-btn"`,
		`01 Jan 26 02:00 UTC`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("schedule section HTML missing %q", want)
		}
	}
}

func TestBackupsScheduleSection_SoftEmpty(t *testing.T) {
	sched := BackupScheduleData{SoftEmpty: true}
	html := renderComponent(t, BackupsLivePage(nil, "", "", sched))

	for _, want := range []string{
		`data-testid="backup-schedule-section"`,
		`data-testid="schedule-soft-empty"`,
		`backup-local is not registered`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("schedule soft-empty HTML missing %q", want)
		}
	}
	if strings.Contains(html, `data-testid="schedule-form"`) {
		t.Fatal("schedule form must not render when soft-empty")
	}
}

func TestBackupsScheduleSection_DisabledState(t *testing.T) {
	sched := BackupScheduleData{
		CronExpr: "0 3 * * 0",
		Enabled:  false,
	}
	html := renderComponent(t, BackupsLivePage(nil, "", "", sched))

	if !strings.Contains(html, `data-testid="schedule-status-disabled"`) {
		t.Fatal("schedule section must show disabled badge when not enabled")
	}
	if strings.Contains(html, `data-testid="schedule-status-enabled"`) {
		t.Fatal("schedule section must not show enabled badge when disabled")
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

func TestNamingTemplatesPage_HasAlertsSectionsAndActionLabels(t *testing.T) {
	html := renderComponent(t, NamingTemplatesListPage([]*renamev1.NamingTemplate{{
		Id:        "tpl-1",
		Name:      "Movies default",
		MediaType: "movie",
		Pattern:   "{Title} ({Year})",
	}}, "Rename module unavailable"))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Naming Templates</h1>`,
		`role="alert"`,
		`Rename module unavailable`,
		`id="naming-templates-heading"`,
		`aria-labelledby="naming-templates-heading"`,
		`aria-label="Organize and rename files"`,
		`aria-label="Create naming template"`,
		`aria-label="Edit naming template Movies default"`,
		`aria-label="Delete naming template Movies default"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("naming templates page HTML missing %q", want)
		}
	}
}

func TestMetadataPage_HasLabelledSectionsAndNav(t *testing.T) {
	html := renderComponent(t, MetadataManagerPage([]MetadataLibRow{{
		ID:   "lib-1",
		Name: "Movies",
		Path: "/media/movies/browse",
	}}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Metadata</h1>`,
		`id="metadata-list-heading"`,
		`aria-labelledby="metadata-list-heading"`,
		`aria-label="Open library Movies for metadata editing"`,
		`role="list"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("metadata page HTML missing %q", want)
		}
	}
}

func TestFormatsPage_HasAlertsFormLabelsAndSections(t *testing.T) {
	html := renderComponent(t, FormatsListPage(FormatsListPageData{
		Error: "formats unavailable",
		Formats: []FormatRow{{
			ID: "cf1", Name: "Remux", Score: 100, RuleCount: 2,
		}},
		SyncResult: &TrashSyncResult{
			FormatsUpserted:  3,
			FormatsSkipped:   1,
			ProfilesUpserted: 2,
		},
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Custom Formats</h1>`,
		`role="alert"`,
		`formats unavailable`,
		`role="status"`,
		`id="formats-sync-heading"`,
		`for="formats-score-set"`,
		`id="formats-score-set"`,
		`id="formats-list-heading"`,
		`aria-labelledby="formats-list-heading"`,
		`aria-label="Edit custom format Remux"`,
		`aria-label="Delete custom format Remux"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("formats page HTML missing %q", want)
		}
	}
}

func TestProfilesPage_HasAlertsSectionsAndActionLabels(t *testing.T) {
	html := renderComponent(t, ProfilesListPage([]*formatsv1.QualityProfile{{
		Id:          "qp-1",
		Name:        "HD-1080p",
		MinScore:    100,
		CutoffScore: 200,
	}}, "Profiles unavailable"))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Quality Profiles</h1>`,
		`role="alert"`,
		`Profiles unavailable`,
		`id="profiles-list-heading"`,
		`aria-labelledby="profiles-list-heading"`,
		`aria-label="Edit quality profile HD-1080p"`,
		`aria-label="Delete quality profile HD-1080p"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("profiles page HTML missing %q", want)
		}
	}
}

func TestReleaseProfilesPage_HasFormLabelsAlertsAndSections(t *testing.T) {
	html := renderComponent(t, ReleaseProfilesPage([]ReleaseProfileRow{{
		ID:             "rp-1",
		Name:           "Bluray preferred",
		Preferred:      "bluray, remux",
		MustContain:    "1080p",
		MustNotContain: "cam",
		PreferredScore: 10,
		Enabled:        true,
	}}, "Release profiles unavailable"))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Release profiles</h1>`,
		`role="alert"`,
		`Release profiles unavailable`,
		`id="release-profile-create-heading"`,
		`for="release-profile-name"`,
		`for="release-profile-score"`,
		`id="release-profiles-list-heading"`,
		`aria-labelledby="release-profiles-list-heading"`,
		`aria-label="Edit release profile Bluray preferred"`,
		`aria-label="Save release profile Bluray preferred"`,
		`aria-label="Delete release profile Bluray preferred"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("release profiles page HTML missing %q", want)
		}
	}
}

func TestMediaLibraryPage_HasHeadingSearchLabelsAndSections(t *testing.T) {
	html := renderComponent(t, MediaListPage("Movies", []*mediaadminv1.MediaItem{{
		Id:    "item-1",
		Title: "Inception",
		Year:  2010,
	}}, 1, 1, 24, "movies", []mediaadminv1.Feature{mediaadminv1.Feature_FEATURE_MISSING}, "dune", ""))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Movies</h1>`,
		`aria-label="Movies library navigation"`,
		`aria-current="page"`,
		`id="media-search-query"`,
		`for="media-search-query"`,
		`id="media-items-heading"`,
		`aria-label="Open Inception"`,
		`role="list"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("media library page HTML missing %q", want)
		}
	}
}

func TestAutomationPage_HasLiveRegionAlertsAndTableCaptions(t *testing.T) {
	html := renderComponent(t, AutomationPage(AutomationPageData{
		Error: "automation offline",
		Flash: "dl-1",
		Items: []AutomationQueueItem{{
			ID: "q1", ItemID: "i1", ItemType: "movie", Title: "Dune", Year: 2021,
		}},
		Total: 1,
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Automation</h1>`,
		`aria-live="polite"`,
		`role="alert"`,
		`automation offline`,
		`role="status"`,
		`id="automation-filter"`,
		`for="automation-filter"`,
		`id="automation-queue-heading"`,
		`<caption class="sr-only">Wanted queue items</caption>`,
		`scope="col"`,
		`aria-label="Search and dispatch Dune"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("automation page HTML missing %q", want)
		}
	}
}

func TestManualImportPage_HasAlertsTableCaptionAndActionLabels(t *testing.T) {
	html := renderComponent(t, ManualImportPage(ManualImportPageData{
		Error: "scanner offline",
		Flash: "Imported 1 file",
		Candidates: []ImportCandidateRow{{
			Path: "/watch/Inception.mkv",
			Name: "Inception.mkv",
			Size: 1024,
		}},
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Manual import</h1>`,
		`role="alert"`,
		`scanner offline`,
		`role="status"`,
		`Imported 1 file`,
		`<caption class="sr-only">Pending import candidates</caption>`,
		`scope="col"`,
		`aria-label="Import Inception.mkv"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("import page HTML missing %q", want)
		}
	}
}

func TestSubtitlesPage_HasLabelledSectionsAlertsAndTables(t *testing.T) {
	html := renderComponent(t, SubtitlesPage(SubtitlesPageData{
		Error: "subtitles offline",
		Flash: "synced",
		Wanted: []SubtitlesWantedItem{{
			ID: "w1", Title: "Dune", Language: "en", Type: "movie",
		}},
		WantedTot: 1,
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">Subtitles</h1>`,
		`role="alert"`,
		`subtitles offline`,
		`role="status"`,
		`aria-label="Subtitle actions"`,
		`id="subtitles-search-query"`,
		`id="subtitles-wanted-heading"`,
		`<caption class="sr-only">Wanted subtitles</caption>`,
		`scope="col"`,
		`aria-label="Open Dune"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("subtitles page HTML missing %q", want)
		}
	}
}

func TestListSyncPage_HasAlertsTableCaptionAndFormLabels(t *testing.T) {
	html := renderComponent(t, ListSyncPage(ListSyncPageData{
		Error: "list-sync offline",
		Flash: "sync started",
		Sources: []ListSyncSourceRow{{
			ID: "src-1", Name: "Trakt watchlist", Type: "trakt", Enabled: true, IntervalMin: 60,
		}},
	}))

	for _, want := range []string{
		`<h1 class="text-2xl font-bold">List Sync</h1>`,
		`role="alert"`,
		`list-sync offline`,
		`role="status"`,
		`sync started`,
		`id="list-sync-sources-heading"`,
		`<caption class="sr-only">List sync sources</caption>`,
		`scope="col"`,
		`aria-label="Edit source Trakt watchlist"`,
		`id="list-sync-name"`,
		`for="list-sync-name"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("list sync page HTML missing %q", want)
		}
	}
}
