package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"github.com/a-h/templ"
	"golang.org/x/net/html"
)

// These checks validate DOM relationships, not just the presence of an attribute
// somewhere in a template. The same rendered documents feed the axe suite.
func TestAccessibilityCoreJourneys(t *testing.T) {
	settings := []SettingsModuleGroup{}
	for _, moduleID := range []string{"scanner", "transcoder"} {
		fields := []SettingField{}
		for _, kind := range []string{"bool", "select", "secret", "int", "string"} {
			fields = append(fields, SettingField{
				Key: kind, Label: "Configure " + kind, Type: kind,
				Description: "Setting for " + moduleID, Options: []string{"default", "custom"},
			})
		}
		settings = append(settings, SettingsModuleGroup{
			ModuleID: moduleID, ModuleName: moduleID,
			Groups: map[string][]SettingField{"General": fields},
		})
	}
	journeys := []struct {
		name string
		page templ.Component
	}{
		{"dashboard", DashboardPage(DashboardData{WantedQueueCount: 2, QueueFailureCount: 1})},
		{"dashboard-disconnected", DashboardPage(DashboardData{Disconnected: true})},
		{"users", UsersPage([]*authv1.UserInfo{{Id: "alice", Username: "Alice"}}, "User service unavailable", 1)},
		{"request", RequestPage(RequestPageData{
			Pending:  []RequestRow{{ID: "req-1", Title: "Dune", RequestedBy: "Alice"}},
			Results:  []RequestSearchHit{{ID: 2, Title: "Arrival", Type: "movie"}},
			Requests: []RequestRow{{ID: "req-2", Title: "Arrival", Status: "pending"}},
		})},
		{"request-unavailable", RequestPage(RequestPageData{Error: "Request service unavailable", SoftEmpty: true})},
		{"library", MediaListPage("Movies", []*mediaadminv1.MediaItem{{Id: "movie-1", Title: "Dune"}}, 2, 3, 24, "movies", nil, "", "")},
		{"queue", UnifiedQueuePage(UnifiedQueueData{
			Wanted:       []UnifiedQueueWanted{{ID: "wanted-1", Title: "Dune"}},
			Failures:     []UnifiedQueueHistory{{ID: "failed-1", Title: "Arrival", Stuck: true}},
			FailureCount: 1, Total: 1,
		})},
		{"queue-empty", UnifiedQueuePage(UnifiedQueueData{})},
		{"settings", SettingsPage(settings)},
		{"invites", InvitesPage(InvitesPageData{Invites: []InviteRow{{ID: "invite-1", Prefix: "abc", Role: "user"}}})},
		{"backups", BackupsLivePage([]BackupRow{{ID: "backup-1", Timestamp: "2026-10-07", Size: "10 MB"}}, "", "Backup created", BackupScheduleData{Enabled: true})},
	}
	links := []NavLink{{Label: "Dashboard", Path: "/", Group: "Overview"}, {Label: "Queue", Path: "/queue", Group: "Daily admin"}}
	for _, journey := range journeys {
		t.Run(journey.name, func(t *testing.T) {
			markup := renderComponent(t, Layout(journey.name, Nav(links, "/"), journey.page))
			assertAccessibleDocument(t, markup)
			writeA11yFixture(t, journey.name, markup)
		})
	}
	for _, errorMsg := range []string{"", "Invalid credentials"} {
		name := "login"
		if errorMsg != "" {
			name += "-error"
		}
		t.Run(name, func(t *testing.T) {
			markup := renderComponent(t, LoginPage(errorMsg))
			assertAccessibleDocument(t, markup)
			writeA11yFixture(t, name, markup)
		})
	}
}

func writeA11yFixture(t *testing.T, name, markup string) {
	t.Helper()
	if dir := os.Getenv("ADMIN_UI_A11Y_FIXTURE_DIR"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(markup), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertAccessibleDocument(t *testing.T, markup string) {
	t.Helper()
	for _, violation := range accessibilityViolations(markup) {
		t.Error(violation)
	}
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	walkA11yDOM(doc, func(n *html.Node) {
		counts[n.Data]++
		if n.Data == "html" && a11yAttr(n, "lang") == "" {
			t.Error("document language is missing")
		}
	})
	for _, tag := range []string{"main", "h1", "title"} {
		if counts[tag] != 1 {
			t.Errorf("want one %s, got %d", tag, counts[tag])
		}
	}
}

func accessibilityViolations(markup string) []string {
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return []string{err.Error()}
	}
	var violations []string
	ids := map[string]*html.Node{}
	labels := map[string]string{}
	walkA11yDOM(doc, func(n *html.Node) {
		if id := a11yAttr(n, "id"); id != "" {
			if ids[id] != nil {
				violations = append(violations, "duplicate id: "+id)
			}
			ids[id] = n
		}
		if n.Data == "label" && a11yAttr(n, "for") != "" {
			labels[a11yAttr(n, "for")] += a11yText(n)
		}
	})
	walkA11yDOM(doc, func(n *html.Node) {
		describe := fmt.Sprintf("%s#%s", n.Data, a11yAttr(n, "id"))
		for _, attr := range []string{"aria-labelledby", "aria-describedby", "aria-controls"} {
			for _, id := range strings.Fields(a11yAttr(n, attr)) {
				if ids[id] == nil {
					violations = append(violations, describe+" has unresolved "+attr+": "+id)
				}
			}
		}
		if n.Data == "label" {
			if id := a11yAttr(n, "for"); id != "" && ids[id] == nil {
				violations = append(violations, "label has missing control: "+id)
			}
		}
		if tab, _ := strconv.Atoi(a11yAttr(n, "tabindex")); tab > 0 {
			violations = append(violations, describe+" has positive tabindex")
		}
		name := a11yAttr(n, "aria-label")
		for _, id := range strings.Fields(a11yAttr(n, "aria-labelledby")) {
			if target := ids[id]; target != nil {
				name += a11yText(target)
			}
		}
		name += labels[a11yAttr(n, "id")]
		if n.Data == "input" && a11yAttr(n, "type") == "hidden" {
			return
		}
		switch n.Data {
		case "input", "select", "textarea":
			for parent := n.Parent; parent != nil; parent = parent.Parent {
				if parent.Data == "label" {
					name += a11yText(parent)
				}
			}
			if strings.TrimSpace(name) == "" {
				violations = append(violations, describe+" has no accessible label")
			}
		case "button", "a":
			if n.Data == "a" && a11yAttr(n, "href") == "" {
				return
			}
			if strings.TrimSpace(name+a11yText(n)) == "" {
				violations = append(violations, describe+" has no accessible name")
			}
		case "img":
			hasAlt := false
			for _, attr := range n.Attr {
				hasAlt = hasAlt || attr.Key == "alt"
			}
			if !hasAlt {
				violations = append(violations, describe+" has no alt attribute")
			}
		}
	})
	return violations
}

func walkA11yDOM(n *html.Node, visit func(*html.Node)) {
	if n.Type == html.ElementNode {
		visit(n)
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walkA11yDOM(child, visit)
	}
}

func a11yAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func a11yText(n *html.Node) string {
	if a11yAttr(n, "aria-hidden") == "true" {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	text := a11yAttr(n, "alt")
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		text += a11yText(child)
	}
	return text
}

func TestAccessibilityAuditRejectsBrokenDOM(t *testing.T) {
	broken := `<input id="duplicate"><input id="duplicate">
	<label for="missing">Name</label><button aria-labelledby="missing"></button>
	<input aria-describedby="missing" tabindex="1"><img src="poster.jpg">`
	violations := strings.Join(accessibilityViolations(broken), "\n")
	for _, rule := range []string{"duplicate id", "missing control", "unresolved aria-labelledby", "unresolved aria-describedby", "positive tabindex", "no accessible label", "no accessible name", "no alt attribute"} {
		if !strings.Contains(violations, rule) {
			t.Errorf("audit failed to catch %q in deliberately broken DOM: %s", rule, violations)
		}
	}
	valid := `<label for="name">Name</label><input id="name" aria-describedby="hint"><p id="hint">Your name</p><button>Save</button><img src="decoration.svg" alt="">`
	if got := accessibilityViolations(valid); len(got) != 0 {
		t.Fatalf("valid labelled controls rejected: %v", got)
	}
}

func TestSettingFieldIDsAndStatusTargetsAreModuleScoped(t *testing.T) {
	for _, moduleID := range []string{"scanner", "transcoder", "module.with/punctuation"} {
		field := SettingField{Key: "enabled", Type: "bool", Label: "Enabled", Description: "Enable module"}
		markup := renderComponent(t, SettingFieldForm(field, moduleID))
		for _, violation := range accessibilityViolations(markup) {
			t.Error(violation)
		}
		doc, err := html.Parse(strings.NewReader(markup))
		if err != nil {
			t.Fatal(err)
		}
		id := settingControlID(moduleID, field.Key)
		walkA11yDOM(doc, func(n *html.Node) {
			if n.Data == "form" && a11yAttr(n, "hx-target") != "#msg-"+id {
				t.Errorf("status updates must target this module's control: %s", markup)
			}
			if n.Data == "input" && a11yAttr(n, "type") == "checkbox" && a11yAttr(n, "id") != id {
				t.Errorf("checkbox has wrong ID: %s", markup)
			}
		})
	}
	if settingControlID("a-b", "c") == settingControlID("a", "b-c") {
		t.Fatal("module/key boundary must not collide")
	}
}

func TestNavigationExposesCurrentPageBeforeJavaScript(t *testing.T) {
	links := []NavLink{{Label: "Dashboard", Path: "/", Group: "Overview"}, {Label: "Queue", Path: "/queue", Group: "Daily admin"}}
	markup := renderComponent(t, Nav(links, "/queue"))
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	current := 0
	walkA11yDOM(doc, func(n *html.Node) {
		if a11yAttr(n, "aria-current") == "page" {
			current++
			if a11yAttr(n, "href") != "/queue" {
				t.Error("wrong page marked current")
			}
		}
	})
	if current != 1 {
		t.Errorf("want exactly one current navigation link; got %d", current)
	}
}

func TestHistoryRestoresContentWithoutReplacingNavigation(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(renderComponent(t, Layout("Dashboard", Nav(nil, "/"), DashboardPage(DashboardData{})))))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	walkA11yDOM(doc, func(n *html.Node) {
		if a11yAttr(n, "hx-history-elt") != "" {
			count++
			if n.Data != "main" || a11yAttr(n, "id") != "main-content" {
				t.Error("HTMX history must preserve the sidebar and its keyboard listeners")
			}
		}
	})
	if count != 1 {
		t.Errorf("expected one content history target; got %d (HTMX otherwise replaces the whole body)", count)
	}
}
