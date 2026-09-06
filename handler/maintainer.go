package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	maintainv1 "github.com/Muxcore-Media/media-library-maintainer/proto/maintainv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaLibraryMaintainer = "media.library.maintainer"

	maintainerDialTimeout   = 3 * time.Second
	maintainerReadTimeout   = 5 * time.Second
	maintainerPageTimeout   = maintainerDialTimeout + 6*maintainerReadTimeout + time.Second
	maintainerActionTimeout = maintainerDialTimeout + maintainerReadTimeout + time.Second
)

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

func (h *Handler) withMaintainerClient(pageCtx context.Context) (maintainv1.MaintainerServiceClient, func(), error) {
	dialCtx, dialCancel := context.WithTimeout(pageCtx, maintainerDialTimeout)
	addr, err := h.maintainerModuleAddr(dialCtx)
	dialCancel()
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
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerPageTimeout)
	defer pageCancel()

	data := templates.MaintainerPageData{
		Flash: r.URL.Query().Get("ok"),
		Error: r.URL.Query().Get("error"),
	}

	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderMaintainer(w, r, data)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	rules, err := client.ListRules(readCtx, &maintainv1.ListRulesRequest{})
	readCancel()
	if err == nil {
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

	readCtx, readCancel = context.WithTimeout(pageCtx, maintainerReadTimeout)
	cols, err := client.ListCollections(readCtx, &maintainv1.ListCollectionsRequest{})
	readCancel()
	if err == nil {
		for _, col := range cols.GetCollections() {
			label := col.GetLeavingSoonLabel()
			if !col.GetLeavingSoonEnabled() {
				label = "—"
			}
			data.Collections = append(data.Collections, templates.MaintainerCollectionRow{
				ID:           col.GetId(),
				Name:         col.GetName(),
				Enabled:      col.GetEnabled(),
				GraceDays:    int(col.GetGraceDays()),
				Action:       strings.TrimPrefix(col.GetArrAction().String(), "ARR_ACTION_"),
				LeavingSoon:  col.GetLeavingSoonEnabled(),
				LeavingLabel: label,
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, maintainerReadTimeout)
	cands, err := client.ListCandidates(readCtx, &maintainv1.ListCandidatesRequest{Page: 1, PageSize: 50})
	readCancel()
	if err == nil {
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

	readCtx, readCancel = context.WithTimeout(pageCtx, maintainerReadTimeout)
	prots, err := client.ListProtections(readCtx, &maintainv1.ListProtectionsRequest{})
	readCancel()
	if err == nil {
		for _, p := range prots.GetProtections() {
			data.Protections = append(data.Protections, templates.MaintainerProtectionRow{
				ID:        p.GetId(),
				Scope:     strings.TrimPrefix(p.GetScope().String(), "MEDIA_SCOPE_"),
				ItemID:    p.GetItemId(),
				Title:     p.GetTitle(),
				Reason:    p.GetReason(),
				ExpiresAt: p.GetExpiresAt(),
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, maintainerReadTimeout)
	runs, err := client.ListRuns(readCtx, &maintainv1.ListRunsRequest{Page: 1, PageSize: 20})
	readCancel()
	if err == nil {
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

	readCtx, readCancel = context.WithTimeout(pageCtx, maintainerReadTimeout)
	lists, err := client.ListExclusionLists(readCtx, &maintainv1.ListExclusionListsRequest{})
	readCancel()
	if err == nil {
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

	readCtx, readCancel = context.WithTimeout(pageCtx, maintainerReadTimeout)
	metrics, err := client.GetStorageMetrics(readCtx, &maintainv1.GetStorageMetricsRequest{})
	readCancel()
	if err == nil {
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

	readCtx, readCancel = context.WithTimeout(pageCtx, maintainerReadTimeout)
	users, err := client.ListPlaybackUsers(readCtx, &maintainv1.ListPlaybackUsersRequest{Limit: 100})
	readCancel()
	if err == nil {
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
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	resp, err := client.ScanNow(readCtx, &maintainv1.ScanNowRequest{DryRun: dryRun})
	readCancel()
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
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	resp, err := client.ActNow(readCtx, &maintainv1.ActNowRequest{
		FreeUp:            freeUp,
		TargetFreePercent: targetFreePercent,
	})
	readCancel()
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

	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.UpsertRule(readCtx, &maintainv1.UpsertRuleRequest{Rule: &maintainv1.RuleGroup{
		Name:             strings.TrimSpace(r.FormValue("name")),
		Enabled:          true,
		Scope:            scope,
		DefinitionJson:   strings.TrimSpace(r.FormValue("definition_json")),
		Outcome:          outcome,
		ArrAction:        action,
		AutoActEnabled:   r.FormValue("auto_act_enabled") == "1",
		AutoActDelayDays: int32(delay),
		MaxActionsPerRun: 50,
		QualityProfileId: strings.TrimSpace(r.FormValue("quality_profile_id")),
	}})
	readCancel()
	if err != nil {
		slog.Warn("maintainer: UpsertRule failed", "error", err)
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Rule added"), http.StatusSeeOther)
}

func (h *Handler) MaintainerExportRules(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	resp, err := client.ExportRules(readCtx, &maintainv1.ExportRulesRequest{})
	readCancel()
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
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	resp, err := client.ImportRules(readCtx, &maintainv1.ImportRulesRequest{
		RulesYaml: strings.TrimSpace(r.FormValue("rules_yaml")),
		Replace:   r.FormValue("replace") == "1",
	})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("Imported %d rules", resp.GetImported())
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) MaintainerToggleRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	resp, err := client.GetRule(readCtx, &maintainv1.GetRuleRequest{Id: id})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	rule := resp.GetRule()
	rule.Enabled = !rule.GetEnabled()

	readCtx, readCancel = context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.UpsertRule(readCtx, &maintainv1.UpsertRuleRequest{Rule: rule})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := "Rule disabled"
	if rule.GetEnabled() {
		msg = "Rule enabled"
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) MaintainerDeleteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.DeleteRule(readCtx, &maintainv1.DeleteRuleRequest{Id: id})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Rule deleted"), http.StatusSeeOther)
}

func (h *Handler) MaintainerApproveCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.ApproveCandidate(readCtx, &maintainv1.ApproveCandidateRequest{Id: id})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Candidate approved"), http.StatusSeeOther)
}

func (h *Handler) MaintainerCancelCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.CancelCandidate(readCtx, &maintainv1.CancelCandidateRequest{Id: id})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Candidate cancelled"), http.StatusSeeOther)
}

func (h *Handler) MaintainerPostponeCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}
	days, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("days")))
	if days <= 0 {
		days = 7
	}
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.PostponeCandidate(readCtx, &maintainv1.PostponeCandidateRequest{Id: id, Days: int32(days)})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape(fmt.Sprintf("Candidate postponed %d days", days)), http.StatusSeeOther)
}

func (h *Handler) MaintainerAddCollection(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}
	grace, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("grace_days")))
	action := maintainv1.ArrAction(maintainv1.ArrAction_value[strings.TrimSpace(r.FormValue("arr_action"))])
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.UpsertCollection(readCtx, &maintainv1.UpsertCollectionRequest{Collection: &maintainv1.Collection{
		Name:               strings.TrimSpace(r.FormValue("name")),
		Enabled:            true,
		GraceDays:          int32(grace),
		ArrAction:          action,
		LeavingSoonEnabled: r.FormValue("leaving_soon_enabled") == "1",
		LeavingSoonLabel:   strings.TrimSpace(r.FormValue("leaving_soon_label")),
	}})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Collection added"), http.StatusSeeOther)
}

func (h *Handler) MaintainerDeleteCollection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.DeleteCollection(readCtx, &maintainv1.DeleteCollectionRequest{Id: id})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Collection deleted"), http.StatusSeeOther)
}

func (h *Handler) MaintainerAddProtection(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}
	scope := maintainv1.MediaScope(maintainv1.MediaScope_value[strings.TrimSpace(r.FormValue("scope"))])
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.UpsertProtection(readCtx, &maintainv1.UpsertProtectionRequest{Protection: &maintainv1.Protection{
		Scope:     scope,
		ItemId:    strings.TrimSpace(r.FormValue("item_id")),
		Title:     strings.TrimSpace(r.FormValue("title")),
		Reason:    strings.TrimSpace(r.FormValue("reason")),
		ExpiresAt: strings.TrimSpace(r.FormValue("expires_at")),
	}})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Protection added"), http.StatusSeeOther)
}

func (h *Handler) MaintainerDeleteProtection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.DeleteProtection(readCtx, &maintainv1.DeleteProtectionRequest{Id: id})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Protection deleted"), http.StatusSeeOther)
}

func (h *Handler) MaintainerAddExclusion(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.UpsertExclusionList(readCtx, &maintainv1.UpsertExclusionListRequest{List: &maintainv1.ExclusionList{
		Name:    strings.TrimSpace(r.FormValue("name")),
		Type:    strings.TrimSpace(r.FormValue("type")),
		ListUrl: strings.TrimSpace(r.FormValue("list_url")),
		ApiKey:  strings.TrimSpace(r.FormValue("api_key")),
	}})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Exclusion list added"), http.StatusSeeOther)
}

func (h *Handler) MaintainerDeleteExclusion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	_, err = client.DeleteExclusionList(readCtx, &maintainv1.DeleteExclusionListRequest{Id: id})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape("Exclusion list deleted"), http.StatusSeeOther)
}

func (h *Handler) MaintainerSyncExclusions(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), maintainerActionTimeout)
	defer pageCancel()
	client, closer, err := h.withMaintainerClient(pageCtx)
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, maintainerReadTimeout)
	resp, err := client.SyncExclusionLists(readCtx, &maintainv1.SyncExclusionListsRequest{})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/maintainer?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("Synced %d lists (%d TMDB ids)", resp.GetListsSynced(), resp.GetIdsLoaded())
	http.Redirect(w, r, "/maintainer?ok="+url.QueryEscape(msg), http.StatusSeeOther)
}
