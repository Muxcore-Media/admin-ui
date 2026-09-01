package handler

import (
	"net/http"
	"strconv"
	"strings"

	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	}) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func joinCSV(parts []string) string {
	return strings.Join(parts, ", ")
}

func (h *Handler) ReleaseProfilesList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := []templates.ReleaseProfileRow{}
	errMsg := ""
	client, closer, err := h.withFormatsClient(ctx)
	if err != nil {
		errMsg = err.Error()
	} else {
		defer closer()
		resp, err := client.ListReleaseProfiles(ctx, &formatsv1.ListReleaseProfilesRequest{})
		if err != nil {
			errMsg = err.Error()
		} else {
			for _, p := range resp.GetProfiles() {
				data = append(data, templates.ReleaseProfileRow{
					ID: p.GetId(), Name: p.GetName(),
					Preferred:      joinCSV(p.GetPreferred()),
					MustContain:    joinCSV(p.GetMustContain()),
					MustNotContain: joinCSV(p.GetMustNotContain()),
					PreferredScore: int(p.GetPreferredScore()),
					Enabled:        p.GetEnabled(),
				})
			}
		}
	}
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Release profiles", nav, templates.ReleaseProfilesPage(data, errMsg)))
}

func (h *Handler) ReleaseProfileUpsert(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/formats/release-profiles", http.StatusSeeOther)
		return
	}
	client, closer, err := h.withFormatsClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/formats/release-profiles?error="+err.Error(), http.StatusSeeOther)
		return
	}
	defer closer()
	score, _ := strconv.Atoi(r.FormValue("preferred_score"))
	enabled := r.FormValue("enabled") == "1"
	_, err = client.UpsertReleaseProfile(ctx, &formatsv1.UpsertReleaseProfileRequest{
		Id: r.FormValue("id"), Name: r.FormValue("name"),
		Preferred:      splitCSV(r.FormValue("preferred")),
		MustContain:    splitCSV(r.FormValue("must_contain")),
		MustNotContain: splitCSV(r.FormValue("must_not_contain")),
		PreferredScore: int32(score),
		Enabled:        &enabled,
	})
	if err != nil {
		http.Redirect(w, r, "/formats/release-profiles?error="+err.Error(), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/formats/release-profiles", http.StatusSeeOther)
}

func (h *Handler) ReleaseProfileDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	client, closer, err := h.withFormatsClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/formats/release-profiles?error="+err.Error(), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.DeleteReleaseProfile(ctx, &formatsv1.DeleteReleaseProfileRequest{Id: id}); err != nil {
		http.Redirect(w, r, "/formats/release-profiles?error="+err.Error(), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/formats/release-profiles", http.StatusSeeOther)
}
