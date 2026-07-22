package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capMediaFormats = "media.formats"

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
	addr := mods[0].GetHttpAddr()
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
	client, closer, err := h.withFormatsClient(r.Context())
	var profiles []*formatsv1.QualityProfile
	errMsg := ""
	if err != nil {
		errMsg = "Formats module unavailable: " + err.Error()
	} else {
		defer closer()
		resp, listErr := client.ListProfiles(r.Context(), &formatsv1.ListProfilesRequest{})
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
