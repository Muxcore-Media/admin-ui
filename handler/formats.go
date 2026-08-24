package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaFormats     = "media.formats"
	formatsDialTimeout  = 3 * time.Second
	formatsReadTimeout  = 5 * time.Second
	formatsPageTimeout  = formatsDialTimeout + formatsReadTimeout + time.Second
)

func (h *Handler) formatsModuleAddr(ctx context.Context) (string, error) {
	if h.Core == nil {
		return "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaFormats)
	if err != nil {
		return "", err
	}
	if len(mods) == 0 {
		return "", fmt.Errorf("no module with capability %s", capMediaFormats)
	}
	addr := normalizeDialAddr(mods[0].GetId(), mods[0].GetHttpAddr())
	if addr == "" {
		return "", fmt.Errorf("formats module has no HTTPAddr")
	}
	return addr, nil
}

func (h *Handler) dialFormatsModule(addr string) (*grpc.ClientConn, formatsv1.FormatServiceClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn, formatsv1.NewFormatServiceClient(conn), nil
}

func (h *Handler) withFormatsClient(ctx context.Context) (formatsv1.FormatServiceClient, func(), error) {
	if h.FormatsClient != nil {
		return h.FormatsClient, func() {}, nil
	}
	addr, err := h.formatsModuleAddr(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, client, err := h.dialFormatsModule(addr)
	if err != nil {
		return nil, nil, err
	}
	return client, func() { _ = conn.Close() }, nil
}

func (h *Handler) listProfileOptions(ctx context.Context) []templates.ProfileOption {
	client, closer, err := h.withFormatsClient(ctx)
	if err != nil {
		return nil
	}
	defer closer()
	resp, err := client.ListProfiles(ctx, &formatsv1.ListProfilesRequest{})
	if err != nil {
		slog.Warn("formats: ListProfiles failed", "error", err)
		return nil
	}
	out := make([]templates.ProfileOption, 0, len(resp.GetProfiles()))
	for _, p := range resp.GetProfiles() {
		out = append(out, templates.ProfileOption{ID: p.GetId(), Name: p.GetName()})
	}
	return out
}

func parseFormatScores(raw string) map[string]int32 {
	scores := make(map[string]int32)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || id == "" {
			continue
		}
		scores[id] = int32(n)
	}
	return scores
}

func (h *Handler) ProfilesList(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), formatsPageTimeout)
	defer cancel()

	var profiles []*formatsv1.QualityProfile
	errMsg := ""
	dialCtx, dialCancel := context.WithTimeout(pageCtx, formatsDialTimeout)
	client, closer, err := h.withFormatsClient(dialCtx)
	dialCancel()
	if err != nil {
		errMsg = "Formats module unavailable: " + err.Error()
	} else {
		defer closer()
		readCtx, readCancel := context.WithTimeout(pageCtx, formatsReadTimeout)
		resp, listErr := client.ListProfiles(readCtx, &formatsv1.ListProfilesRequest{})
		readCancel()
		if listErr != nil {
			errMsg = listErr.Error()
		} else {
			profiles = resp.GetProfiles()
		}
	}
	content := templates.ProfilesListPage(profiles, errMsg)
	h.render(w, r, templates.Layout("Quality Profiles", h.nav(r.URL.Path), content))
}

func (h *Handler) ProfileNew(w http.ResponseWriter, r *http.Request) {
	content := templates.ProfileEditPage(&formatsv1.QualityProfile{}, true, "")
	h.render(w, r, templates.Layout("New Profile", h.nav(r.URL.Path), content))
}

func (h *Handler) ProfileCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/formats/profiles/new", http.StatusSeeOther)
		return
	}
	client, closer, err := h.withFormatsClient(r.Context())
	if err != nil {
		content := templates.ProfileEditPage(profileFromForm(r), true, err.Error())
		h.render(w, r, templates.Layout("New Profile", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	resp, err := client.CreateProfile(r.Context(), &formatsv1.CreateProfileRequest{
		Name:                r.FormValue("name"),
		MinScore:            formInt32(r, "min_score"),
		CutoffScore:         formInt32(r, "cutoff_score"),
		UpgradeAllowed:      r.FormValue("upgrade_allowed") == "1",
		UpgradeDelayMinutes: formInt32(r, "upgrade_delay_minutes"),
		FormatScores:        parseFormatScores(r.FormValue("format_scores")),
	})
	if err != nil {
		content := templates.ProfileEditPage(profileFromForm(r), true, err.Error())
		h.render(w, r, templates.Layout("New Profile", h.nav(r.URL.Path), content))
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.format.profile.create", "format_profile", resp.GetProfile().GetId(), map[string]string{
			"name": r.FormValue("name"),
		})
	}
	http.Redirect(w, r, "/formats/profiles/"+resp.GetProfile().GetId(), http.StatusSeeOther)
}

func (h *Handler) ProfileEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	client, closer, err := h.withFormatsClient(r.Context())
	if err != nil {
		content := templates.ProfileEditPage(&formatsv1.QualityProfile{Id: id}, false, err.Error())
		h.render(w, r, templates.Layout("Edit Profile", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	resp, err := client.ListProfiles(r.Context(), &formatsv1.ListProfilesRequest{})
	if err != nil {
		content := templates.ProfileEditPage(&formatsv1.QualityProfile{Id: id}, false, err.Error())
		h.render(w, r, templates.Layout("Edit Profile", h.nav(r.URL.Path), content))
		return
	}
	var profile *formatsv1.QualityProfile
	for _, p := range resp.GetProfiles() {
		if p.GetId() == id {
			profile = p
			break
		}
	}
	if profile == nil {
		http.Redirect(w, r, "/formats/profiles", http.StatusSeeOther)
		return
	}
	content := templates.ProfileEditPage(profile, false, "")
	h.render(w, r, templates.Layout("Edit Profile", h.nav(r.URL.Path), content))
}

func (h *Handler) ProfileUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/formats/profiles/"+id, http.StatusSeeOther)
		return
	}
	client, closer, err := h.withFormatsClient(r.Context())
	if err != nil {
		p := profileFromForm(r)
		p.Id = id
		content := templates.ProfileEditPage(p, false, err.Error())
		h.render(w, r, templates.Layout("Edit Profile", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	_, err = client.UpdateProfile(r.Context(), &formatsv1.UpdateProfileRequest{
		Id:                  id,
		Name:                r.FormValue("name"),
		MinScore:            formInt32(r, "min_score"),
		CutoffScore:         formInt32(r, "cutoff_score"),
		UpgradeAllowed:      r.FormValue("upgrade_allowed") == "1",
		UpgradeDelayMinutes: formInt32(r, "upgrade_delay_minutes"),
		FormatScores:        parseFormatScores(r.FormValue("format_scores")),
	})
	if err != nil {
		p := profileFromForm(r)
		p.Id = id
		content := templates.ProfileEditPage(p, false, err.Error())
		h.render(w, r, templates.Layout("Edit Profile", h.nav(r.URL.Path), content))
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.format.profile.update", "format_profile", id, map[string]string{
			"name": r.FormValue("name"),
		})
	}
	http.Redirect(w, r, "/formats/profiles/"+id, http.StatusSeeOther)
}

func (h *Handler) ProfileDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	client, closer, err := h.withFormatsClient(r.Context())
	if err != nil {
		http.Redirect(w, r, "/formats/profiles", http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.DeleteProfile(r.Context(), &formatsv1.DeleteProfileRequest{Id: id}); err != nil {
		slog.Warn("formats: DeleteProfile failed", "id", id, "error", err)
		http.Redirect(w, r, "/formats/profiles", http.StatusSeeOther)
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.format.profile.delete", "format_profile", id, nil)
	}
	http.Redirect(w, r, "/formats/profiles", http.StatusSeeOther)
}

func formInt32(r *http.Request, key string) int32 {
	n, _ := strconv.Atoi(r.FormValue(key))
	return int32(n)
}

func profileFromForm(r *http.Request) *formatsv1.QualityProfile {
	return &formatsv1.QualityProfile{
		Name:                r.FormValue("name"),
		MinScore:            formInt32(r, "min_score"),
		CutoffScore:         formInt32(r, "cutoff_score"),
		UpgradeAllowed:      r.FormValue("upgrade_allowed") == "1",
		UpgradeDelayMinutes: formInt32(r, "upgrade_delay_minutes"),
		FormatScores:        parseFormatScores(r.FormValue("format_scores")),
	}
}

func (h *Handler) FormatsList(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), formatsPageTimeout)
	defer cancel()

	data := templates.FormatsListPageData{
		ScoreSet:       "default",
		Services:       []string{"radarr", "sonarr"},
		ImportProfiles: false,
	}
	dialCtx, dialCancel := context.WithTimeout(pageCtx, formatsDialTimeout)
	client, closer, err := h.withFormatsClient(dialCtx)
	dialCancel()
	if err != nil {
		data.Error = "Formats module unavailable: " + err.Error()
	} else {
		defer closer()
		readCtx, readCancel := context.WithTimeout(pageCtx, formatsReadTimeout)
		resp, listErr := client.ListFormats(readCtx, &formatsv1.ListFormatsRequest{})
		readCancel()
		if listErr != nil {
			data.Error = listErr.Error()
		} else {
			for _, f := range resp.GetFormats() {
				data.Formats = append(data.Formats, templates.FormatRow{
					ID: f.GetId(), Name: f.GetName(),
					Score: int(f.GetDefaultScore()), RuleCount: len(f.GetRules()),
				})
			}
		}
	}
	content := templates.FormatsListPage(data)
	h.render(w, r, templates.Layout("Custom Formats", h.nav(r.URL.Path), content))
}

func (h *Handler) FormatsSyncTrash(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/formats", http.StatusSeeOther)
		return
	}
	scoreSet := strings.TrimSpace(r.FormValue("score_set"))
	if scoreSet == "" {
		scoreSet = "default"
	}
	importProfiles := r.FormValue("import_profiles") == "1"
	var services []string
	if r.FormValue("service_radarr") == "1" {
		services = append(services, "radarr")
	}
	if r.FormValue("service_sonarr") == "1" {
		services = append(services, "sonarr")
	}

	data := templates.FormatsListPageData{
		ScoreSet:       scoreSet,
		Services:       services,
		ImportProfiles: importProfiles,
	}

	client, closer, err := h.withFormatsClient(r.Context())
	if err != nil {
		data.Error = "Formats module unavailable: " + err.Error()
		data.SyncResult = &templates.TrashSyncResult{Error: data.Error}
		content := templates.FormatsListPage(data)
		h.render(w, r, templates.Layout("Custom Formats", h.nav("/formats"), content))
		return
	}
	defer closer()

	resp, syncErr := client.SyncTrashGuides(r.Context(), &formatsv1.SyncTrashGuidesRequest{
		ScoreSet:        scoreSet,
		ImportProfiles:  importProfiles,
		Services:        services,
	})
	if syncErr != nil {
		data.SyncResult = &templates.TrashSyncResult{Error: syncErr.Error()}
	} else {
		data.SyncResult = &templates.TrashSyncResult{
			FormatsUpserted:  int(resp.GetFormatsUpserted()),
			FormatsSkipped:   int(resp.GetFormatsSkipped()),
			ProfilesUpserted: int(resp.GetProfilesUpserted()),
			GuidesPath:       resp.GetGuidesPath(),
			Warnings:         resp.GetWarnings(),
		}
		if sess := SessionFromContext(r.Context()); sess != nil {
			h.auditLog(r.Context(), sess.UserID, "admin.format.sync_trash", "format", "", map[string]string{
				"score_set":        scoreSet,
				"import_profiles":  strconv.FormatBool(importProfiles),
				"formats_upserted": strconv.Itoa(int(resp.GetFormatsUpserted())),
			})
		}
	}

	listResp, listErr := client.ListFormats(r.Context(), &formatsv1.ListFormatsRequest{})
	if listErr != nil {
		if data.Error == "" {
			data.Error = listErr.Error()
		}
	} else {
		for _, f := range listResp.GetFormats() {
			data.Formats = append(data.Formats, templates.FormatRow{
				ID: f.GetId(), Name: f.GetName(),
				Score: int(f.GetDefaultScore()), RuleCount: len(f.GetRules()),
			})
		}
	}
	content := templates.FormatsListPage(data)
	h.render(w, r, templates.Layout("Custom Formats", h.nav("/formats"), content))
}

func (h *Handler) FormatNew(w http.ResponseWriter, r *http.Request) {
	content := templates.FormatEditPage(templates.FormatEdit{}, true, "")
	h.render(w, r, templates.Layout("New Format", h.nav(r.URL.Path), content))
}

func (h *Handler) FormatCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/formats/new", http.StatusSeeOther)
		return
	}
	edit := formatEditFromForm(r)
	client, closer, err := h.withFormatsClient(r.Context())
	if err != nil {
		content := templates.FormatEditPage(edit, true, err.Error())
		h.render(w, r, templates.Layout("New Format", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	resp, err := client.CreateFormat(r.Context(), &formatsv1.CreateFormatRequest{
		Name:         edit.Name,
		DefaultScore: int32(edit.Score),
		Rules:        parseFormatRules(edit.RulesText),
	})
	if err != nil {
		content := templates.FormatEditPage(edit, true, err.Error())
		h.render(w, r, templates.Layout("New Format", h.nav(r.URL.Path), content))
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.format.create", "format", resp.GetFormat().GetId(), map[string]string{
			"name": edit.Name,
		})
	}
	http.Redirect(w, r, "/formats/item/"+resp.GetFormat().GetId(), http.StatusSeeOther)
}

func (h *Handler) FormatEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	client, closer, err := h.withFormatsClient(r.Context())
	if err != nil {
		content := templates.FormatEditPage(templates.FormatEdit{ID: id}, false, err.Error())
		h.render(w, r, templates.Layout("Edit Format", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	resp, err := client.ListFormats(r.Context(), &formatsv1.ListFormatsRequest{})
	if err != nil {
		content := templates.FormatEditPage(templates.FormatEdit{ID: id}, false, err.Error())
		h.render(w, r, templates.Layout("Edit Format", h.nav(r.URL.Path), content))
		return
	}
	var found *formatsv1.CustomFormat
	for _, f := range resp.GetFormats() {
		if f.GetId() == id {
			found = f
			break
		}
	}
	if found == nil {
		http.Redirect(w, r, "/formats", http.StatusSeeOther)
		return
	}
	content := templates.FormatEditPage(formatToEdit(found), false, "")
	h.render(w, r, templates.Layout("Edit Format", h.nav(r.URL.Path), content))
}

func (h *Handler) FormatUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/formats/item/"+id, http.StatusSeeOther)
		return
	}
	edit := formatEditFromForm(r)
	edit.ID = id
	client, closer, err := h.withFormatsClient(r.Context())
	if err != nil {
		content := templates.FormatEditPage(edit, false, err.Error())
		h.render(w, r, templates.Layout("Edit Format", h.nav(r.URL.Path), content))
		return
	}
	defer closer()
	_, err = client.UpdateFormat(r.Context(), &formatsv1.UpdateFormatRequest{
		Id:           id,
		Name:         edit.Name,
		DefaultScore: int32(edit.Score),
		Rules:        parseFormatRules(edit.RulesText),
	})
	if err != nil {
		content := templates.FormatEditPage(edit, false, err.Error())
		h.render(w, r, templates.Layout("Edit Format", h.nav(r.URL.Path), content))
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.format.update", "format", id, map[string]string{
			"name": edit.Name,
		})
	}
	http.Redirect(w, r, "/formats/item/"+id, http.StatusSeeOther)
}

func (h *Handler) FormatDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	client, closer, err := h.withFormatsClient(r.Context())
	if err != nil {
		http.Redirect(w, r, "/formats", http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.DeleteFormat(r.Context(), &formatsv1.DeleteFormatRequest{Id: id}); err != nil {
		slog.Warn("formats: DeleteFormat failed", "id", id, "error", err)
		http.Redirect(w, r, "/formats", http.StatusSeeOther)
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.format.delete", "format", id, nil)
	}
	http.Redirect(w, r, "/formats", http.StatusSeeOther)
}

func formatEditFromForm(r *http.Request) templates.FormatEdit {
	return templates.FormatEdit{
		Name:      r.FormValue("name"),
		Score:     int(formInt32(r, "default_score")),
		RulesText: r.FormValue("rules"),
	}
}

func formatToEdit(f *formatsv1.CustomFormat) templates.FormatEdit {
	var lines []string
	for _, rule := range f.GetRules() {
		lines = append(lines, rule.GetField()+"|"+rule.GetOp()+"|"+rule.GetValue())
	}
	return templates.FormatEdit{
		ID: f.GetId(), Name: f.GetName(),
		Score: int(f.GetDefaultScore()), RulesText: strings.Join(lines, "\n"),
	}
}

func parseFormatRules(raw string) []*formatsv1.FormatRule {
	var out []*formatsv1.FormatRule
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		out = append(out, &formatsv1.FormatRule{
			Field: strings.TrimSpace(parts[0]),
			Op:    strings.TrimSpace(parts[1]),
			Value: strings.TrimSpace(parts[2]),
		})
	}
	return out
}
