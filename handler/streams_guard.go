package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capPlaybackGuard = "playback.guard"

func (h *Handler) playbackGuardAddr(ctx context.Context) (string, error) {
	if h.Core == nil {
		return "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capPlaybackGuard)
	if err != nil {
		return "", err
	}
	if len(mods) == 0 {
		return "", fmt.Errorf("no module with capability %s", capPlaybackGuard)
	}
	addr := normalizeDialAddr(mods[0].GetId(), mods[0].GetHttpAddr())
	if addr == "" {
		return "", fmt.Errorf("playback-guard has no dial address")
	}
	return addr, nil
}

func (h *Handler) withPlaybackGuardClient(ctx context.Context) (guardv1.PlaybackGuardServiceClient, func(), error) {
	addr, err := h.playbackGuardAddr(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return guardv1.NewPlaybackGuardServiceClient(conn), func() { _ = conn.Close() }, nil
}

func (h *Handler) StreamsGuardPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := templates.StreamsGuardPageData{}
	if msg := strings.TrimSpace(r.URL.Query().Get("success")); msg != "" {
		data.Success = msg
	}
	if msg := strings.TrimSpace(r.URL.Query().Get("error")); msg != "" {
		data.Error = msg
	}

	client, closer, err := h.withPlaybackGuardClient(ctx)
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderStreamsGuard(w, r, data)
		return
	}
	defer closer()

	h.populateGuardPage(ctx, client, &data)
	h.populateGuardActiveSessions(ctx, &data)
	h.renderStreamsGuard(w, r, data)
}

func (h *Handler) StreamsGuardAcknowledge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids := r.Form["violation_id"]
	if len(ids) == 0 {
		http.Redirect(w, r, "/streams/guard?success=No+violations+selected", http.StatusSeeOther)
		return
	}

	client, closer, err := h.withPlaybackGuardClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/streams/guard", http.StatusSeeOther)
		return
	}
	defer closer()

	resp, err := client.AcknowledgeViolations(ctx, &guardv1.AcknowledgeViolationsRequest{ViolationIds: ids})
	if err != nil {
		http.Redirect(w, r, "/streams/guard", http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("Acknowledged %d violation(s)", resp.GetUpdated())
	http.Redirect(w, r, "/streams/guard?success="+strings.ReplaceAll(msg, " ", "+"), http.StatusSeeOther)
}

func (h *Handler) StreamsGuardResetTrust(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID := strings.TrimSpace(r.FormValue("user_id"))
	userName := strings.TrimSpace(r.FormValue("user_name"))

	client, closer, err := h.withPlaybackGuardClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/streams/guard", http.StatusSeeOther)
		return
	}
	defer closer()

	if _, err := client.ResetTrustScore(ctx, &guardv1.ResetTrustScoreRequest{
		UserId:   userID,
		UserName: userName,
	}); err != nil {
		http.Redirect(w, r, "/streams/guard", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/streams/guard?success=Trust+score+reset", http.StatusSeeOther)
}

func (h *Handler) StreamsGuardMerge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/streams/guard", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	client, closer, err := h.withPlaybackGuardClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/streams/guard", http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.MergeUsers(ctx, &guardv1.MergeUsersRequest{
		SourceUserId:   r.FormValue("source_user_id"),
		SourceUserName: r.FormValue("source_user_name"),
		TargetUserId:   r.FormValue("target_user_id"),
		TargetUserName: r.FormValue("target_user_name"),
	})
	if err != nil {
		http.Redirect(w, r, "/streams/guard?error="+strings.ReplaceAll(err.Error(), " ", "+"), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("Merged users (%d violations, %d sessions updated)", resp.GetViolationsUpdated(), resp.GetSessionsUpdated())
	http.Redirect(w, r, "/streams/guard?success="+strings.ReplaceAll(msg, " ", "+"), http.StatusSeeOther)
}

func (h *Handler) StreamsGuardTerminate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/streams/guard", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	client, closer, err := h.withPlaybackGuardClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/streams/guard?error=playback-guard+unavailable", http.StatusSeeOther)
		return
	}
	defer closer()

	resp, err := client.TerminateSession(ctx, &guardv1.TerminateSessionRequest{
		SessionId:  strings.TrimSpace(r.FormValue("session_id")),
		ServerType: strings.TrimSpace(r.FormValue("server_type")),
		Reason:     "operator terminate from admin-ui",
	})
	if err != nil {
		http.Redirect(w, r, "/streams/guard?error="+strings.ReplaceAll(err.Error(), " ", "+"), http.StatusSeeOther)
		return
	}
	if !resp.GetOk() {
		msg := resp.GetError()
		if msg == "" {
			msg = "terminate failed"
		}
		http.Redirect(w, r, "/streams/guard?error="+strings.ReplaceAll(msg, " ", "+"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/streams/guard?success=Session+terminated", http.StatusSeeOther)
}

func (h *Handler) populateGuardActiveSessions(ctx context.Context, data *templates.StreamsGuardPageData) {
	client, closer, err := h.withPlaybackMonitorClient(ctx)
	if err != nil {
		data.MonitorSoftNote = true
		return
	}
	defer closer()
	resp, err := client.ListActiveSessions(ctx, &monitorv1.ListActiveSessionsRequest{Limit: 50})
	if err != nil {
		data.MonitorSoftNote = true
		return
	}
	for _, s := range resp.GetSessions() {
		user := s.GetUserName()
		if user == "" {
			user = s.GetUserId()
		}
		data.ActiveSessions = append(data.ActiveSessions, templates.GuardActiveSessionRow{
			ExternalSessionID: s.GetExternalSessionId(),
			ServerType:        s.GetServerType(),
			User:              user,
			Title:             s.GetTitle(),
			Platform:          s.GetPlatform(),
		})
	}
}

func (h *Handler) populateGuardPage(ctx context.Context, client guardv1.PlaybackGuardServiceClient, data *templates.StreamsGuardPageData) {
	if rules, err := client.ListRules(ctx, &guardv1.ListRulesRequest{}); err == nil {
		for _, rule := range rules.GetRules() {
			data.Rules = append(data.Rules, templates.GuardRuleRow{
				Name:          rule.GetName(),
				TypeLabel:     guardRuleTypeLabel(rule.GetType()),
				Enabled:       boolLabel(rule.GetEnabled()),
				ParamsSummary: guardParamsSummary(rule.GetParams()),
			})
		}
	}
	if violations, err := client.ListViolations(ctx, &guardv1.ListViolationsRequest{Limit: 100}); err == nil {
		for _, v := range violations.GetViolations() {
			user := v.GetUserName()
			if user == "" {
				user = v.GetUserId()
			}
			created := ""
			if v.GetCreatedAtUnix() > 0 {
				created = time.Unix(v.GetCreatedAtUnix(), 0).UTC().Format("2006-01-02 15:04")
			}
			data.Violations = append(data.Violations, templates.GuardViolationRow{
				ID:       v.GetId(),
				User:     user,
				Summary:  v.GetSummary(),
				Severity: v.GetSeverity(),
				Created:  created,
			})
		}
	}
	if trust, err := client.ListTrustScores(ctx, &guardv1.ListTrustScoresRequest{Limit: 50}); err == nil {
		for _, ts := range trust.GetScores() {
			user := ts.GetUserName()
			if user == "" {
				user = ts.GetUserId()
			}
			updated := ""
			if ts.GetUpdatedAtUnix() > 0 {
				updated = time.Unix(ts.GetUpdatedAtUnix(), 0).UTC().Format("2006-01-02 15:04")
			}
			data.TrustScores = append(data.TrustScores, templates.GuardTrustRow{
				UserID:   ts.GetUserId(),
				UserName: ts.GetUserName(),
				User:     user,
				Score:    ts.GetScore(),
				Updated:  updated,
			})
		}
	}
}

func guardRuleTypeLabel(t guardv1.RuleType) string {
	switch t {
	case guardv1.RuleType_RULE_TYPE_IMPOSSIBLE_TRAVEL:
		return "Impossible travel"
	case guardv1.RuleType_RULE_TYPE_SIMULTANEOUS_LOCATIONS:
		return "Simultaneous locations"
	case guardv1.RuleType_RULE_TYPE_DEVICE_VELOCITY:
		return "Device velocity"
	case guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS:
		return "Concurrent streams"
	case guardv1.RuleType_RULE_TYPE_GEO_RESTRICTION:
		return "Geo restriction"
	case guardv1.RuleType_RULE_TYPE_ACCOUNT_INACTIVITY:
		return "Account inactivity"
	default:
		return "Unknown"
	}
}

func guardParamsSummary(params map[string]string) string {
	if len(params) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(params))
	for k, v := range params {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ", ")
}

func boolLabel(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}

func (h *Handler) renderStreamsGuard(w http.ResponseWriter, r *http.Request, data templates.StreamsGuardPageData) {
	content := templates.StreamsGuardPage(data)
	nav := h.nav("/streams/guard")
	h.render(w, r, templates.Layout("Playback guard", nav, content))
}
