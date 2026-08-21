package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	maintainv1 "github.com/Muxcore-Media/media-library-maintainer/proto/maintainv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capMediaLibraryMaintainer = "media.library.maintainer"

func (h *Handler) maintainerModuleAddr(ctx context.Context) (string, error) {
	if h.Core == nil {
		return "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaLibraryMaintainer)
	if err != nil {
		return "", err
	}
	if len(mods) == 0 {
		return "", fmt.Errorf("no module with capability %s", capMediaLibraryMaintainer)
	}
	addr := normalizeDialAddr(mods[0].GetId(), mods[0].GetHttpAddr())
	if addr == "" {
		return "", fmt.Errorf("maintainer module has no dial address")
	}
	return addr, nil
}

func (h *Handler) withMaintainerClient(ctx context.Context) (maintainv1.MaintainerServiceClient, func(), error) {
	addr, err := h.maintainerModuleAddr(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return maintainv1.NewMaintainerServiceClient(conn), func() { _ = conn.Close() }, nil
}

func (h *Handler) MaintainerPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := templates.MaintainerPageData{
		Flash: r.URL.Query().Get("ok"),
		Error: r.URL.Query().Get("error"),
	}

	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		data.Error = err.Error()
		h.renderMaintainer(w, r, data)
		return
	}
	defer closer()

	if rules, err := client.ListRules(ctx, &maintainv1.ListRulesRequest{}); err == nil {
		for _, rule := range rules.GetRules() {
			data.Rules = append(data.Rules, templates.MaintainerRuleRow{
				ID:      rule.GetId(),
				Name:    rule.GetName(),
				Scope:   strings.TrimPrefix(rule.GetScope().String(), "MEDIA_SCOPE_"),
				Enabled: rule.GetEnabled(),
				Outcome: strings.TrimPrefix(rule.GetOutcome().String(), "RULE_OUTCOME_"),
				Action:  strings.TrimPrefix(rule.GetArrAction().String(), "ARR_ACTION_"),
				AutoAct: rule.GetAutoActEnabled(),
			})
		}
	}

	if cands, err := client.ListCandidates(ctx, &maintainv1.ListCandidatesRequest{Page: 1, PageSize: 50}); err == nil {
		for _, c := range cands.GetCandidates() {
			data.Candidates = append(data.Candidates, templates.MaintainerCandidateRow{
				ID:       c.GetId(),
				Title:    c.GetTitle(),
				Scope:    strings.TrimPrefix(c.GetScope().String(), "MEDIA_SCOPE_"),
				Status:   strings.TrimPrefix(c.GetStatus().String(), "CANDIDATE_STATUS_"),
				Action:   strings.TrimPrefix(c.GetArrAction().String(), "ARR_ACTION_"),
				ActAfter: c.GetActAfter(),
			})
		}
	}

	if runs, err := client.ListRuns(ctx, &maintainv1.ListRunsRequest{Page: 1, PageSize: 20}); err == nil {
		for _, run := range runs.GetRuns() {
			data.Runs = append(data.Runs, templates.MaintainerRunRow{
				ID:          run.GetId(),
				Kind:        run.GetKind(),
				Status:      run.GetStatus(),
				Found:       int(run.GetCandidatesFound()),
				Taken:       int(run.GetActionsTaken()),
				Failed:      int(run.GetActionsFailed()),
				DryRun:      run.GetDryRun(),
				StartedAt:   run.GetStartedAt(),
				CompletedAt: run.GetCompletedAt(),
				Error:       run.GetError(),
			})
		}
	}

	if lists, err := client.ListExclusionLists(ctx, &maintainv1.ListExclusionListsRequest{}); err == nil {
		for _, list := range lists.GetLists() {
			data.Exclusions = append(data.Exclusions, templates.MaintainerExclusionRow{
				ID:         list.GetId(),
				Name:       list.GetName(),
				Type:       list.GetType(),
				ListURL:    list.GetListUrl(),
				LastSynced: list.GetLastSynced(),
				Count:      len(list.GetTmdbIds()),
			})
		}
	}

	if metrics, err := client.GetStorageMetrics(ctx, &maintainv1.GetStorageMetricsRequest{}); err == nil {
		for _, p := range metrics.GetPaths() {
			data.Storage = append(data.Storage, templates.MaintainerStorageRow{
				Path:         p.GetPath(),
				FreePercent:  fmt.Sprintf("%.1f%%", p.GetFreePercent()),
				FreeBytes:    formatMaintainerBytes(p.GetFreeBytes()),
				TotalBytes:   formatMaintainerBytes(p.GetTotalBytes()),
				LibraryBytes: formatMaintainerBytes(p.GetLibraryBytes()),
				ItemCount:    fmt.Sprintf("%d", p.GetItemCount()),
			})
		}
	}

	if users, err := client.ListPlaybackUsers(ctx, &maintainv1.ListPlaybackUsersRequest{Limit: 100}); err == nil {
		for _, u := range users.GetUsers() {
			if name := strings.TrimSpace(u.GetUsername()); name != "" {
				data.PlaybackUsers = append(data.PlaybackUsers, name)
			}
		}
	}

	h.renderMaintainer(w, r, data)
}

func formatMaintainerBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func (h *Handler) renderMaintainer(w http.ResponseWriter, r *http.Request, data templates.MaintainerPageData) {
	content := templates.MaintainerPage(data)
	nav := h.nav("/maintainer")
	component := templates.Layout("Library Maintainer", nav, content)
	h.render(w, r, component)
}

func (h *Handler) MaintainerScan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}
	dryRun := r.FormValue("dry_run") == "true"
	ctx := r.Context()
	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	resp, err := client.ScanNow(ctx, &maintainv1.ScanNowRequest{DryRun: dryRun})
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("Scan complete: %d candidates", resp.GetCandidatesFound())
	if dryRun {
		msg = "Dry-run scan: " + msg
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) MaintainerAct(w http.ResponseWriter, r *http.Request) {
	h.maintainerActWithOptions(w, r, false, 0)
}

func (h *Handler) MaintainerFreeUp(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}
	target, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("target_free_percent")), 64)
	if target <= 0 {
		target = 15
	}
	h.maintainerActWithOptions(w, r, true, target)
}

func (h *Handler) maintainerActWithOptions(w http.ResponseWriter, r *http.Request, freeUp bool, targetFreePercent float64) {
	ctx := r.Context()
	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	resp, err := client.ActNow(ctx, &maintainv1.ActNowRequest{
		FreeUp:            freeUp,
		TargetFreePercent: targetFreePercent,
	})
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("Act complete: %d actions, %d failed", resp.GetActionsTaken(), resp.GetRun().GetActionsFailed())
	if freeUp {
		msg = fmt.Sprintf("Free-up complete: %d actions, %d failed (target %.1f%%)", resp.GetActionsTaken(), resp.GetRun().GetActionsFailed(), targetFreePercent)
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) MaintainerAddRule(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}
	delay, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("auto_act_delay_days")))
	scope := maintainv1.MediaScope(maintainv1.MediaScope_value[strings.TrimSpace(r.FormValue("scope"))])
	outcome := maintainv1.RuleOutcome(maintainv1.RuleOutcome_value[strings.TrimSpace(r.FormValue("outcome"))])
	action := maintainv1.ArrAction(maintainv1.ArrAction_value[strings.TrimSpace(r.FormValue("arr_action"))])

	ctx := r.Context()
	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	_, err = client.UpsertRule(ctx, &maintainv1.UpsertRuleRequest{Rule: &maintainv1.RuleGroup{
		Name:              strings.TrimSpace(r.FormValue("name")),
		Enabled:           true,
		Scope:             scope,
		DefinitionJson:    strings.TrimSpace(r.FormValue("definition_json")),
		Outcome:           outcome,
		ArrAction:         action,
		AutoActEnabled:    r.FormValue("auto_act_enabled") == "1",
		AutoActDelayDays:  int32(delay),
		MaxActionsPerRun:  50,
		QualityProfileId:  strings.TrimSpace(r.FormValue("quality_profile_id")),
	}})
	if err != nil {
		slog.Warn("maintainer: UpsertRule failed", "error", err)
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Rule added"), http.StatusSeeOther)
}

func (h *Handler) MaintainerExportRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.ExportRules(ctx, &maintainv1.ExportRulesRequest{})
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	body := resp.GetRulesYaml()
	if body == "" {
		body = resp.GetRulesJson()
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", `attachment; filename="maintainer-rules.yaml"`)
	_, _ = w.Write([]byte(body))
}

func (h *Handler) MaintainerImportRules(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.ImportRules(ctx, &maintainv1.ImportRulesRequest{
		RulesYaml: strings.TrimSpace(r.FormValue("rules_yaml")),
		Replace:   r.FormValue("replace") == "1",
	})
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("Imported %d rules", resp.GetImported())
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) MaintainerDeleteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.DeleteRule(ctx, &maintainv1.DeleteRuleRequest{Id: id}); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Rule deleted"), http.StatusSeeOther)
}

func (h *Handler) MaintainerApproveCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.ApproveCandidate(ctx, &maintainv1.ApproveCandidateRequest{Id: id}); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Candidate approved"), http.StatusSeeOther)
}

func (h *Handler) MaintainerCancelCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.CancelCandidate(ctx, &maintainv1.CancelCandidateRequest{Id: id}); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Candidate cancelled"), http.StatusSeeOther)
}

func (h *Handler) MaintainerAddExclusion(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	_, err = client.UpsertExclusionList(ctx, &maintainv1.UpsertExclusionListRequest{List: &maintainv1.ExclusionList{
		Name:    strings.TrimSpace(r.FormValue("name")),
		Type:    strings.TrimSpace(r.FormValue("type")),
		ListUrl: strings.TrimSpace(r.FormValue("list_url")),
		ApiKey:  strings.TrimSpace(r.FormValue("api_key")),
	}})
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Exclusion list added"), http.StatusSeeOther)
}

func (h *Handler) MaintainerDeleteExclusion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()
	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.DeleteExclusionList(ctx, &maintainv1.DeleteExclusionListRequest{Id: id}); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Exclusion list deleted"), http.StatusSeeOther)
}

func (h *Handler) MaintainerSyncExclusions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	client, closer, err := h.withMaintainerClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.SyncExclusionLists(ctx, &maintainv1.SyncExclusionListsRequest{})
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("Synced %d lists (%d TMDB ids)", resp.GetListsSynced(), resp.GetIdsLoaded())
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape(msg), http.StatusSeeOther)
}
