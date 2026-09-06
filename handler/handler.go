package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"

	"github.com/Muxcore-Media/admin-ui/arrmigrate"
	"github.com/Muxcore-Media/admin-ui/session"
	"github.com/a-h/templ"

	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capAuthorizer   = "authorizer"
	capMediaLibrary = "media.library"
)

type LoginMetrics interface {
	IncSuccess()
	IncFailure()
}

type Handler struct {
	Core             *client.Client
	Sessions         *session.Store
	secure           bool
	events           *eventRing
	version          string
	loginMetrics     LoginMetrics
	coreConnected    bool
	AuthAddr         string // browser-facing auth base (e.g. https://auth.gringotts)
	AuthInternalAddr string // server-side auth base for code exchange (defaults to AuthAddr)
	PublicURL        string // optional public origin override for OAuth callbacks
	TrustedProxies   []net.IPNet
	// HealthMonitorURL is the health-monitor HTTP base (e.g. http://127.0.0.1:9203).
	// Empty disables the dashboard monitor panel.
	HealthMonitorURL string
	// UserdataURL is optional HTTP base for userdata-local (parental sync).
	UserdataURL string
	// RequestMediaURL is optional HTTP base for request-media (empty = mesh only).
	RequestMediaURL string
	// APIRestURL is optional api-rest base for SpoolService HTTP proxy fallback.
	APIRestURL string
	// Spool is the marketplace DeployTag client (gRPC, HTTP proxy, or test stub).
	Spool SpoolAPI
	// AuditHook, when set, receives audit events (tests).
	AuditHook func(actor, action, resource, resourceID string, details map[string]string)

	// FormatsClient, when set, bypasses mesh discovery (tests).
	FormatsClient formatsv1.FormatServiceClient
	// ArrHTTPClient overrides the Arr migrate HTTP client (tests).
	ArrHTTPClient *http.Client
	// MigrateMovies / MigrateTV / MigrateMusic inject library Add RPCs for Arr migrate tests.
	MigrateMovies arrmigrate.MovieImporter
	MigrateTV     arrmigrate.TVImporter
	MigrateMusic  arrmigrate.MusicImporter
	// ResolveProfileID maps quality profile name → MuxCore id (tests / optional).
	ResolveProfileID func(ctx context.Context, name string) string

	ResetLoginRate func(ip string)

	mediaMu      sync.RWMutex
	mediaModules []*discoveryv1.ModuleInfoProto

	mediaRefreshCh chan struct{}
	mediaSubCancel func()
}

func New(core *client.Client, store *session.Store, secure bool, version string, lm LoginMetrics, connected bool, authAddr string, resetLoginRate func(ip string), trustedProxies []net.IPNet) *Handler {
	h := &Handler{
		Core:           core,
		Sessions:       store,
		secure:         secure,
		events:         newEventRing(100),
		version:        version,
		loginMetrics:   lm,
		coreConnected:  connected,
		AuthAddr:       authAddr,
		TrustedProxies: trustedProxies,
		ResetLoginRate: resetLoginRate,
		mediaRefreshCh: make(chan struct{}, 1),
	}
	if connected && core != nil {
		h.refreshMediaNavLinks(context.Background())
		h.startMediaEventSubscription(context.Background())
	}
	h.startEventSubscription(context.Background())
	return h
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", h.requireNoAuth(h.LoginPage))
	mux.HandleFunc("POST /login", h.requireNoAuth(h.Login))
	mux.HandleFunc("GET /logout", h.Logout)

	mux.HandleFunc("GET /", h.requireAuth(h.Dashboard))
	mux.HandleFunc("GET /dashboard/health", h.requireAuth(h.HealthGrid))
	mux.HandleFunc("GET /dashboard/monitor", h.requireAuth(h.MonitorSummary))

	mux.HandleFunc("GET /modules", h.requireAuth(h.ModuleList))
	mux.HandleFunc("GET /modules/{id}", h.requireAuth(h.ModuleDetail))
	mux.HandleFunc("GET /marketplace", h.requireAuth(h.MarketplacePage))
	mux.HandleFunc("POST /marketplace/deploy", h.requireAuth(h.MarketplaceDeploy))
	mux.HandleFunc("GET /marketplace/trust", h.requireAuth(h.MarketplaceTrustPage))
	mux.HandleFunc("POST /marketplace/trust/settings", h.requireAuth(h.MarketplaceTrustSaveSettings))
	mux.HandleFunc("POST /marketplace/trust/keys", h.requireAuth(h.MarketplaceTrustAddKey))
	mux.HandleFunc("POST /marketplace/trust/keys/{id}/revoke", h.requireAuth(h.MarketplaceTrustRevokeKey))

	mux.HandleFunc("GET /cluster", h.requireAuth(h.ClusterPage))
	mux.HandleFunc("GET /cluster/nodes", h.requireAuth(h.ClusterNodes))
	mux.HandleFunc("GET /cluster/sse", h.requireAuth(h.ClusterSSE))

	mux.HandleFunc("GET /events", h.requireAuth(h.EventsPage))
	mux.HandleFunc("GET /events/stream", h.requireAuth(h.EventsStream))
	mux.HandleFunc("GET /events/stats", h.requireAuth(h.EventStatsPanel))

	mux.HandleFunc("GET /storage", h.requireAuth(h.StoragePage))

	mux.HandleFunc("GET /settings", h.requireAuth(h.SettingsPage))
	mux.HandleFunc("POST /settings/{moduleID}/{key}", h.requireAuth(h.SettingsUpdate))

	mux.HandleFunc("GET /audit", h.requireAuth(h.AuditPage))

	mux.HandleFunc("GET /activity", h.requireAuth(h.ActivityPage))
	mux.HandleFunc("GET /calendar", h.requireAuth(h.UnifiedCalendarPage))
	mux.HandleFunc("GET /queue", h.requireAuth(h.UnifiedQueuePage))
	mux.HandleFunc("POST /queue/remove", h.requireAuth(h.UnifiedQueueRemove))
	mux.HandleFunc("POST /queue/retry-import", h.requireAuth(h.UnifiedQueueRetryImport))
	mux.HandleFunc("POST /queue/blocklist", h.requireAuth(h.UnifiedQueueBlocklist))
	mux.HandleFunc("GET /automation", h.requireAuth(h.AutomationQueuePage))
	mux.HandleFunc("POST /automation/dispatch", h.requireAuth(h.AutomationDispatch))
	mux.HandleFunc("POST /automation/queue/remove", h.requireAuth(h.AutomationQueueRemove))
	mux.HandleFunc("POST /automation/blocklist/clear", h.requireAuth(h.AutomationBlocklistClear))
	mux.HandleFunc("POST /automation/delay", h.requireAuth(h.AutomationDelayUpdate))
	mux.HandleFunc("GET /release-search", h.requireAuth(h.ReleaseSearchPage))
	mux.HandleFunc("POST /release-search/grab", h.requireAuth(h.ReleaseSearchGrab))

	mux.HandleFunc("GET /subtitles", h.requireAuth(h.SubtitlesPage))
	mux.HandleFunc("POST /subtitles/sync", h.requireAuth(h.SubtitlesSync))
	mux.HandleFunc("POST /subtitles/search-wanted", h.requireAuth(h.SubtitlesSearchWanted))
	mux.HandleFunc("POST /subtitles/upgrade", h.requireAuth(h.SubtitlesUpgrade))
	mux.HandleFunc("POST /subtitles/providers/{id}", h.requireAuth(h.SubtitlesProviderToggle))
	mux.HandleFunc("POST /subtitles/blacklist/{id}/delete", h.requireAuth(h.SubtitlesBlacklistRemove))
	mux.HandleFunc("POST /subtitles/mass-edit", h.requireAuth(h.SubtitlesMassEdit))
	mux.HandleFunc("POST /subtitles/history/clear", h.requireAuth(h.SubtitlesClearHistory))
	mux.HandleFunc("POST /subtitles/download", h.requireAuth(h.SubtitlesDownload))
	mux.HandleFunc("POST /subtitles/profiles", h.requireAuth(h.SubtitlesUpsertProfile))
	mux.HandleFunc("GET /subtitles/media/{id}", h.requireAuth(h.SubtitlesMediaDetail))
	mux.HandleFunc("GET /subtitles/series/{id}", h.requireAuth(h.SubtitlesSeriesDetail))
	mux.HandleFunc("POST /subtitles/media/{id}/search-wanted", h.requireAuth(h.SubtitlesMediaSearchWanted))
	mux.HandleFunc("POST /subtitles/media/{id}/profile", h.requireAuth(h.SubtitlesMediaSetProfile))
	mux.HandleFunc("POST /subtitles/files/{id}/delete", h.requireAuth(h.SubtitlesFileDelete))
	mux.HandleFunc("POST /subtitles/test-arr", h.requireAuth(h.SubtitlesTestArr))
	mux.HandleFunc("GET /jellyfin", h.requireAuth(h.JellyfinStatusPage))
	mux.HandleFunc("POST /jellyfin/sync", h.requireAuth(h.JellyfinSync))
	mux.HandleFunc("POST /jellyfin/refresh", h.requireAuth(h.JellyfinRefresh))

	mux.HandleFunc("GET /request", h.requireAuth(h.RequestPage))
	mux.HandleFunc("POST /request", h.requireAuth(h.RequestCreate))
	mux.HandleFunc("POST /request/{id}/approve", h.requireAuth(h.RequestApprove))
	mux.HandleFunc("POST /request/{id}/deny", h.requireAuth(h.RequestDeny))

	mux.HandleFunc("GET /approvals", h.requireAuth(h.ApprovalsPage))
	mux.HandleFunc("POST /approvals/{id}/approve", h.requireAuth(h.ApprovalsApprove))
	mux.HandleFunc("POST /approvals/{id}/deny", h.requireAuth(h.ApprovalsDeny))
	mux.HandleFunc("GET /invites", h.requireAuth(h.InvitesPage))
	mux.HandleFunc("POST /invites", h.requireAuth(h.InvitesCreate))
	mux.HandleFunc("POST /invites/{id}/revoke", h.requireAuth(h.InvitesRevoke))
	// Household invite redemption — no auth; served to invited members before they have an account.
	mux.HandleFunc("GET /invite/redeem", h.HouseholdRedeemPage)
	mux.HandleFunc("POST /invite/redeem", h.HouseholdRedeemSubmit)
	mux.HandleFunc("GET /import", h.requireAuth(h.ManualImportPage))
	mux.HandleFunc("POST /import", h.requireAuth(h.ManualImportPost))

	mux.HandleFunc("GET /migrate", h.requireAuth(h.MigratePage))
	mux.HandleFunc("POST /migrate", h.requireAuth(h.MigratePost))

	mux.HandleFunc("GET /music", h.requireAuth(h.MusicListPage))
	mux.HandleFunc("GET /music/{id}", h.requireAuth(h.MusicDetailPage))

	mux.HandleFunc("GET /tagging", h.requireAuth(h.TaggingPage))
	mux.HandleFunc("POST /tagging/tags", h.requireAuth(h.TaggingCreateTag))
	mux.HandleFunc("POST /tagging/rules", h.requireAuth(h.TaggingCreateRule))
	mux.HandleFunc("POST /tagging/rules/{id}/delete", h.requireAuth(h.TaggingDeleteRule))

	mux.HandleFunc("GET /list-sync", h.requireAuth(h.ListSyncPage))
	mux.HandleFunc("GET /list-sync/history", h.requireAuth(h.ListSyncHistoryPage))
	mux.HandleFunc("GET /list-sync/items", h.requireAuth(h.ListSyncItemsPage))
	mux.HandleFunc("GET /list-sync/sources/{id}/edit", h.requireAuth(h.ListSyncEditPage))
	mux.HandleFunc("POST /list-sync/sync", h.requireAuth(h.ListSyncNow))
	mux.HandleFunc("POST /list-sync/sources", h.requireAuth(h.ListSyncAddSource))
	mux.HandleFunc("POST /list-sync/sources/{id}", h.requireAuth(h.ListSyncUpdateSource))
	mux.HandleFunc("POST /list-sync/sources/{id}/sync", h.requireAuth(h.ListSyncSourceNow))
	mux.HandleFunc("POST /list-sync/sources/{id}/toggle", h.requireAuth(h.ListSyncToggleSource))
	mux.HandleFunc("POST /list-sync/sources/{id}/test", h.requireAuth(h.ListSyncTestSource))
	mux.HandleFunc("POST /list-sync/sources/{id}/delete", h.requireAuth(h.ListSyncRemoveSource))

	mux.HandleFunc("GET /maintainer", h.requireAuth(h.MaintainerPage))
	mux.HandleFunc("POST /maintainer/scan", h.requireAuth(h.MaintainerScan))
	mux.HandleFunc("POST /maintainer/act", h.requireAuth(h.MaintainerAct))
	mux.HandleFunc("POST /maintainer/act/free-up", h.requireAuth(h.MaintainerFreeUp))
	mux.HandleFunc("POST /maintainer/exclusions", h.requireAuth(h.MaintainerAddExclusion))
	mux.HandleFunc("POST /maintainer/exclusions/sync", h.requireAuth(h.MaintainerSyncExclusions))
	mux.HandleFunc("POST /maintainer/exclusions/{id}/delete", h.requireAuth(h.MaintainerDeleteExclusion))
	mux.HandleFunc("POST /maintainer/rules", h.requireAuth(h.MaintainerAddRule))
	mux.HandleFunc("GET /maintainer/rules/export", h.requireAuth(h.MaintainerExportRules))
	mux.HandleFunc("POST /maintainer/rules/import", h.requireAuth(h.MaintainerImportRules))
	mux.HandleFunc("POST /maintainer/rules/{id}/delete", h.requireAuth(h.MaintainerDeleteRule))
	mux.HandleFunc("POST /maintainer/candidates/{id}/approve", h.requireAuth(h.MaintainerApproveCandidate))
	mux.HandleFunc("POST /maintainer/candidates/{id}/postpone", h.requireAuth(h.MaintainerPostponeCandidate))
	mux.HandleFunc("POST /maintainer/candidates/{id}/cancel", h.requireAuth(h.MaintainerCancelCandidate))
	mux.HandleFunc("POST /maintainer/collections", h.requireAuth(h.MaintainerAddCollection))
	mux.HandleFunc("POST /maintainer/collections/{id}/delete", h.requireAuth(h.MaintainerDeleteCollection))
	mux.HandleFunc("POST /maintainer/protections", h.requireAuth(h.MaintainerAddProtection))
	mux.HandleFunc("POST /maintainer/protections/{id}/delete", h.requireAuth(h.MaintainerDeleteProtection))

	mux.HandleFunc("GET /config", h.requireAuth(h.ConfigPage))

	mux.HandleFunc("GET /media/{moduleID}", h.requireAuth(h.MediaLibraryList))
	mux.HandleFunc("GET /media/{moduleID}/missing", h.requireAuth(h.MediaMissing))
	mux.HandleFunc("GET /media/{moduleID}/tags", h.requireAuth(h.MediaTags))
	mux.HandleFunc("POST /media/{moduleID}/tags", h.requireAuth(h.MediaTagsPost))
	mux.HandleFunc("GET /media/{moduleID}/collections", h.requireAuth(h.MediaCollections))
	mux.HandleFunc("GET /media/{moduleID}/collections/{collectionID}", h.requireAuth(h.MediaCollectionDetail))
	mux.HandleFunc("POST /media/{moduleID}/collections/{collectionID}/monitor", h.requireAuth(h.MediaCollectionMonitor))
	mux.HandleFunc("POST /media/{moduleID}/collections/{collectionID}/sync", h.requireAuth(h.MediaCollectionSync))
	mux.HandleFunc("GET /media/{moduleID}/calendar", h.requireAuth(h.MediaCalendar))
	mux.HandleFunc("GET /media/{moduleID}/item/{id}", h.requireAuth(h.MediaLibraryItem))
	mux.HandleFunc("POST /media/{moduleID}/item/{id}/dispatch", h.requireAuth(h.MediaItemDispatch))
	mux.HandleFunc("POST /media/{moduleID}/item/{id}/metadata", h.requireAuth(h.MediaLibraryUpdate))
	mux.HandleFunc("POST /media/{moduleID}/item/{id}/refresh", h.requireAuth(h.MediaItemRefresh))
	mux.HandleFunc("POST /media/{moduleID}/item/{id}/delete", h.requireAuth(h.MediaItemDelete))
	mux.HandleFunc("POST /media/{moduleID}/item/{id}/files/{fileID}/delete", h.requireAuth(h.MediaFileDelete))
	mux.HandleFunc("POST /media/{moduleID}/item/{id}/season/{seasonID}/monitor", h.requireAuth(h.MediaSeasonMonitor))
	mux.HandleFunc("POST /media/{moduleID}/item/{id}/episode/{episodeID}/monitor", h.requireAuth(h.MediaEpisodeMonitor))
	mux.HandleFunc("POST /media/{moduleID}/item/{id}/episode/{episodeID}/files/delete", h.requireAuth(h.MediaEpisodeFileDelete))
	mux.HandleFunc("POST /media/{moduleID}/item/{id}/titles", h.requireAuth(h.MediaAlternateTitleAdd))
	mux.HandleFunc("POST /media/{moduleID}/item/{id}/titles/{titleID}/delete", h.requireAuth(h.MediaAlternateTitleDelete))
	mux.HandleFunc("GET /media/{moduleID}/item/{id}/artwork", h.requireAuth(h.MediaLibraryArtwork))

	mux.HandleFunc("GET /formats", h.requireAuth(h.FormatsList))
	mux.HandleFunc("POST /formats/sync-trash", h.requireAuth(h.FormatsSyncTrash))
	mux.HandleFunc("GET /formats/new", h.requireAuth(h.FormatNew))
	mux.HandleFunc("POST /formats", h.requireAuth(h.FormatCreate))
	mux.HandleFunc("GET /formats/profiles", h.requireAuth(h.ProfilesList))
	mux.HandleFunc("GET /formats/profiles/new", h.requireAuth(h.ProfileNew))
	mux.HandleFunc("POST /formats/profiles", h.requireAuth(h.ProfileCreate))
	mux.HandleFunc("GET /formats/profiles/{id}", h.requireAuth(h.ProfileEdit))
	mux.HandleFunc("POST /formats/profiles/{id}", h.requireAuth(h.ProfileUpdate))
	mux.HandleFunc("POST /formats/profiles/{id}/delete", h.requireAuth(h.ProfileDelete))
	mux.HandleFunc("GET /formats/release-profiles", h.requireAuth(h.ReleaseProfilesList))
	mux.HandleFunc("POST /formats/release-profiles", h.requireAuth(h.ReleaseProfileUpsert))
	mux.HandleFunc("POST /formats/release-profiles/{id}/delete", h.requireAuth(h.ReleaseProfileDelete))
	mux.HandleFunc("GET /formats/item/{id}", h.requireAuth(h.FormatEdit))
	mux.HandleFunc("POST /formats/item/{id}", h.requireAuth(h.FormatUpdate))
	mux.HandleFunc("POST /formats/item/{id}/delete", h.requireAuth(h.FormatDelete))

	mux.HandleFunc("GET /roots", h.requireAuth(h.RootsList))
	mux.HandleFunc("GET /roots/new", h.requireAuth(h.RootNew))
	mux.HandleFunc("POST /roots", h.requireAuth(h.RootCreate))
	mux.HandleFunc("GET /roots/browse", h.requireAuth(h.RootsBrowse))
	mux.HandleFunc("GET /roots/{id}", h.requireAuth(h.RootEdit))
	mux.HandleFunc("POST /roots/{id}", h.requireAuth(h.RootUpdate))
	mux.HandleFunc("POST /roots/{id}/delete", h.requireAuth(h.RootDelete))

	mux.HandleFunc("GET /rename/templates", h.requireAuth(h.NamingTemplatesList))
	mux.HandleFunc("GET /rename/templates/new", h.requireAuth(h.NamingTemplateNew))
	mux.HandleFunc("POST /rename/templates", h.requireAuth(h.NamingTemplateCreate))
	mux.HandleFunc("GET /rename/templates/{id}", h.requireAuth(h.NamingTemplateEdit))
	mux.HandleFunc("POST /rename/templates/{id}", h.requireAuth(h.NamingTemplateUpdate))
	mux.HandleFunc("POST /rename/templates/{id}/delete", h.requireAuth(h.NamingTemplateDelete))
	mux.HandleFunc("GET /rename/organize", h.requireAuth(h.OrganizePage))
	mux.HandleFunc("POST /rename/organize", h.requireAuth(h.OrganizePost))

	mux.HandleFunc("GET /users", h.requireAuth(h.UsersPage))
	mux.HandleFunc("GET /users/create-form", h.requireAuth(h.UsersCreateForm))
	mux.HandleFunc("POST /users", h.requireAuth(h.UsersCreate))
	mux.HandleFunc("DELETE /users/{id}", h.requireAuth(h.UsersDelete))
	mux.HandleFunc("GET /users/{id}/detail", h.requireAuth(h.UsersDetail))
	mux.HandleFunc("POST /users/{id}/password", h.requireAuth(h.UsersSetPassword))
	mux.HandleFunc("POST /users/{id}/roles", h.requireAuth(h.UsersSetRoles))
	mux.HandleFunc("GET /users/{id}/totp", h.requireAuth(h.UsersTOTPStatus))
	mux.HandleFunc("POST /users/{id}/totp", h.requireAuth(h.UsersTOTP))
	mux.HandleFunc("GET /users/{id}/tokens", h.requireAuth(h.UsersTokens))
	mux.HandleFunc("POST /users/{id}/tokens", h.requireAuth(h.UsersTokens))
	mux.HandleFunc("DELETE /users/{id}/tokens/{tokenId}", h.requireAuth(h.UsersTokens))
	mux.HandleFunc("GET /users/{id}/passkeys", h.requireAuth(h.PasskeyList))
	mux.HandleFunc("DELETE /users/{id}/passkeys/{credId}", h.requireAuth(h.PasskeyDelete))
	mux.HandleFunc("GET /api/auth/passkey/register/{id}/begin", h.requireAuth(h.PasskeyBeginRegister))
	mux.HandleFunc("POST /api/auth/passkey/register/{id}/complete", h.requireAuth(h.PasskeyCompleteRegister))

	mux.HandleFunc("GET /devices", h.requireAuth(h.DevicesPage))
	mux.HandleFunc("POST /devices/{token}/revoke", h.requireAuth(h.DevicesRevoke))
	mux.HandleFunc("POST /devices/{token}/rename", h.requireAuth(h.DevicesRename))
	mux.HandleFunc("GET /logs", h.requireAuth(h.LogsPage))
	mux.HandleFunc("GET /logs/partial", h.requireAuth(h.LogsPartial))
	mux.HandleFunc("GET /branding", h.requireAuth(h.BrandingPage))
	mux.HandleFunc("POST /branding", h.requireAuth(h.BrandingSave))
	mux.HandleFunc("GET /branding.css", h.BrandingCSS)
	mux.HandleFunc("GET /networking", h.requireAuth(h.NetworkingPage))
	mux.HandleFunc("POST /networking", h.requireAuth(h.NetworkingSave))
	mux.HandleFunc("GET /keys", h.requireAuth(h.APIKeysPage))
	mux.HandleFunc("POST /keys/create", h.requireAuth(h.APIKeysCreate))
	mux.HandleFunc("POST /keys/{id}/rotate", h.requireAuth(h.APIKeysRotate))
	mux.HandleFunc("POST /keys/{id}/revoke", h.requireAuth(h.APIKeysRevoke))
	mux.HandleFunc("GET /backups", h.requireAuth(h.BackupsPage))
	mux.HandleFunc("POST /backups/create", h.requireAuth(h.BackupsCreate))
	mux.HandleFunc("POST /backups/{id}/delete", h.requireAuth(h.BackupsDelete))
	mux.HandleFunc("POST /backups/{id}/restore", h.requireAuth(h.BackupsRestore))
	mux.HandleFunc("GET /tasks", h.requireAuth(h.TasksPage))
	mux.HandleFunc("POST /tasks/{id}/cancel", h.requireAuth(h.TasksCancel))
	mux.HandleFunc("GET /playback", h.requireAuth(h.PlaybackAdminPage))
	mux.HandleFunc("POST /playback", h.requireAuth(h.PlaybackAdminSave))
	mux.HandleFunc("GET /streams", h.requireAuth(h.StreamsPage))
	mux.HandleFunc("GET /streams/events", h.requireAuth(h.StreamsLiveEvents))
	mux.HandleFunc("GET /streams/active.json", h.requireAuth(h.StreamsActiveJSON))
	mux.HandleFunc("GET /streams/history", h.requireAuth(h.StreamsHistoryPage))
	mux.HandleFunc("GET /streams/stats", h.requireAuth(h.StreamsStatsPage))
	mux.HandleFunc("GET /streams/libraries", h.requireAuth(h.StreamsLibrariesPage))
	mux.HandleFunc("GET /streams/users", h.requireAuth(h.StreamsUsersPage))
	mux.HandleFunc("GET /streams/guard", h.requireAuth(h.StreamsGuardPage))
	mux.HandleFunc("GET /streams/notifications", h.requireAuth(h.StreamsNotificationsPage))
	mux.HandleFunc("POST /streams/notifications/create", h.requireAuth(h.StreamsNotificationsCreate))
	mux.HandleFunc("POST /streams/notifications/delete", h.requireAuth(h.StreamsNotificationsDelete))
	mux.HandleFunc("POST /streams/notifications/destinations/create", h.requireAuth(h.StreamsNotificationsDestinationCreate))
	mux.HandleFunc("POST /streams/notifications/destinations/delete", h.requireAuth(h.StreamsNotificationsDestinationDelete))
	mux.HandleFunc("POST /streams/notifications/destinations/test", h.requireAuth(h.StreamsNotificationsDestinationTest))
	mux.HandleFunc("POST /streams/guard/acknowledge", h.requireAuth(h.StreamsGuardAcknowledge))
	mux.HandleFunc("POST /streams/guard/trust/reset", h.requireAuth(h.StreamsGuardResetTrust))
	mux.HandleFunc("POST /streams/guard/merge", h.requireAuth(h.StreamsGuardMerge))
	mux.HandleFunc("POST /streams/guard/terminate", h.requireAuth(h.StreamsGuardTerminate))
	mux.HandleFunc("GET /streams/servers", h.requireAuth(h.StreamsServersPage))
	mux.HandleFunc("GET /streams/map", h.requireAuth(h.StreamsMapPage))
	mux.HandleFunc("GET /streams/map/data", h.requireAuth(h.StreamsMapData))
	mux.HandleFunc("GET /transcode", h.requireAuth(h.TranscodePage))
	mux.HandleFunc("GET /transcode/edit", h.requireAuth(h.TranscodeEditPage))
	mux.HandleFunc("POST /transcode/save", h.requireAuth(h.TranscodeSave))
	mux.HandleFunc("POST /transcode/delete/{id}", h.requireAuth(h.TranscodeDelete))
	mux.HandleFunc("POST /transcode/scan", h.requireAuth(h.TranscodeScan))
	mux.HandleFunc("POST /transcode/review/{id}/approve", h.requireAuth(h.TranscodeReviewApprove))
	mux.HandleFunc("POST /transcode/review/{id}/reject", h.requireAuth(h.TranscodeReviewReject))
	mux.HandleFunc("POST /transcode/apply-template/{id}", h.requireAuth(h.TranscodeApplyTemplate))
	mux.HandleFunc("GET /libraries", h.requireAuth(h.LibrariesAdminPage))
	mux.HandleFunc("GET /plugins", h.requireAuth(h.PluginsPage))
	mux.HandleFunc("GET /metadata", h.requireAuth(h.MetadataManagerPage))
	mux.HandleFunc("GET /auth", h.requireAuth(h.AuthSSOPage))
	mux.HandleFunc("GET /livetv", h.requireAuth(h.LiveTVAdminPage))
	mux.HandleFunc("POST /livetv", h.requireAuth(h.LiveTVAdminSave))
	mux.HandleFunc("GET /users/{id}/parental", h.requireAuth(h.UsersParental))
	mux.HandleFunc("POST /users/{id}/parental", h.requireAuth(h.UsersParental))

	mux.HandleFunc("GET /auth/callback", h.AuthCallback)
	mux.HandleFunc("GET /auth/status", h.AuthStatus)
}

func (h *Handler) nav(currentPath string) templ.Component {
	links := make([]templates.NavLink, len(staticNavLinks), len(staticNavLinks)+4)
	copy(links, staticNavLinks)

	h.mediaMu.RLock()
	for _, mod := range h.mediaModules {
		links = append(links, templates.NavLink{
			Label: mod.GetName(),
			Path:  "/media/" + mod.GetId(),
			Icon:  "disc",
			Group: "Media",
		})
	}
	h.mediaMu.RUnlock()

	return templates.Nav(links, currentPath)
}

func (h *Handler) refreshMediaNavLinks(ctx context.Context) {
	if h.Core == nil {
		return
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaLibrary)
	if err != nil {
		slog.Warn("media: FindByCapability failed", "error", err)
		return
	}
	h.mediaMu.Lock()
	h.mediaModules = mods
	h.mediaMu.Unlock()
}

func (h *Handler) startMediaEventSubscription(ctx context.Context) {
	if h.Core == nil {
		return
	}

	if h.mediaSubCancel != nil {
		h.mediaSubCancel()
	}

	ch, cancel, err := h.Core.Events.Subscribe(ctx, "*")
	if err != nil {
		slog.Warn("media: subscribe to module.registered failed", "error", err)
		return
	}

	h.mediaSubCancel = cancel

	go func() {
		for ev := range ch {
			evType := ev.GetType()
			if evType != "module.registered" && evType != "module.unregistered" {
				continue
			}

			payload := ev.GetPayload()
			type moduleEventPayload struct {
				ModuleID     string   `json:"module_id"`
				Capabilities []string `json:"capabilities"`
			}
			var p moduleEventPayload
			if err := json.Unmarshal(payload, &p); err != nil {
				continue
			}

			if evType == "module.unregistered" {
				slog.Info("media: module unregistered, refreshing tabs", "module", p.ModuleID)
				h.refreshMediaNavLinks(ctx)
				continue
			}

			for _, cap := range p.Capabilities {
				if cap == capMediaLibrary {
					slog.Info("media: new media library module registered", "module", p.ModuleID)
					h.refreshMediaNavLinks(ctx)
					break
				}
			}
		}
	}()
}

func (h *Handler) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.coreConnected {
			nav := h.nav(r.URL.Path)
			content := templates.DashboardPage(templates.DashboardData{Disconnected: true})
			component := templates.Layout("Disconnected", nav, content)
			h.render(w, r, component)
			return
		}

		cookie, err := r.Cookie("session")
		if err != nil {
			redirectToLogin(w, r)
			return
		}

		sess, ok := h.Sessions.Get(cookie.Value)
		if !ok {
			redirectToLogin(w, r)
			return
		}

		if err := h.checkAuthorized(r.Context(), sess); err != nil {
			slog.Warn("authorization denied", "user", sess.Username, "path", r.URL.Path, "error", err)
			component := templates.Forbidden()
			h.render(w, r, component)
			return
		}

		ctx := context.WithValue(r.Context(), ctxSessionKey, sess)
		next(w, r.WithContext(ctx))
	}
}

func (h *Handler) requireNoAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.coreConnected {
			nav := h.nav(r.URL.Path)
			content := templates.DashboardPage(templates.DashboardData{Disconnected: true})
			component := templates.Layout("Disconnected", nav, content)
			h.render(w, r, component)
			return
		}

		if cookie, err := r.Cookie("session"); err == nil {
			if _, ok := h.Sessions.Get(cookie.Value); ok {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
		}
		next(w, r)
	}
}

func (h *Handler) checkAuthorized(ctx context.Context, sess *session.Session) error {
	if h.Core == nil {
		return fmt.Errorf("core not connected")
	}

	mod, err := h.findFirstModule(ctx, capAuthorizer)
	if err != nil {
		return fmt.Errorf("authorizer unavailable: %w", err)
	}

	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return fmt.Errorf("authorizer has no gRPC address")
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial authorizer %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	ac := authv1.NewAuthServiceClient(conn)
	cresp, err := ac.Can(ctx, &authv1.CanRequest{
		UserId:   sess.UserID,
		Action:   "admin.access",
		Resource: "admin.ui",
	})
	if err != nil {
		return fmt.Errorf("authz call failed: %w", err)
	}

	if !cresp.Allowed {
		return fmt.Errorf("access denied")
	}
	return nil
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) NotAuthHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.Sessions.GetFromRequest(r); ok {
		w.WriteHeader(http.StatusNotFound)
		nav := h.nav(r.URL.Path)
		content := templates.NotFound()
		component := templates.Layout("Not Found", nav, content)
		if err := component.Render(r.Context(), w); err != nil {
			slog.Error("render 404", "error", err)
		}
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, component templ.Component) {
	if err := component.Render(r.Context(), w); err != nil {
		slog.Error("template render failed", "path", r.URL.Path, "error", err)
	}
}

type contextKey string

const ctxSessionKey contextKey = "session"

func SessionFromContext(ctx context.Context) *session.Session {
	s, _ := ctx.Value(ctxSessionKey).(*session.Session)
	return s
}

// auditLog writes an audit entry asynchronously. Errors are logged but not returned
// to avoid blocking the request flow.
func (h *Handler) auditLog(ctx context.Context, actor, action, resource, resourceID string, details map[string]string) {
	if h.AuditHook != nil {
		h.AuditHook(actor, action, resource, resourceID, details)
	}
	if h.Core == nil || h.Core.Audit == nil {
		return
	}
	go func() {
		traceID := ""
		if tid, ok := ctx.Value("trace_id").(string); ok {
			traceID = tid
		}
		if _, err := h.Core.Audit.Log(ctx, actor, action, resource, resourceID, traceID, details); err != nil {
			slog.Warn("audit log failed", "action", action, "error", err)
		}
	}()
}
