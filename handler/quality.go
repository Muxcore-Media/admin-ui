package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func toTemplDefs(defs []QualityDefinition) []templates.QualityDefinition {
	out := make([]templates.QualityDefinition, len(defs))
	for i, d := range defs {
		out[i] = templates.QualityDefinition{
			ID:         d.ID,
			Name:       d.Name,
			Resolution: d.Resolution,
			Allowed:    d.Allowed,
		}
	}
	return out
}

func toTemplProfiles(profiles []QualityProfile) []templates.QualityProfile {
	out := make([]templates.QualityProfile, len(profiles))
	for i, p := range profiles {
		out[i] = templates.QualityProfile{
			ID:             p.ID,
			Name:           p.Name,
			QualityIDs:     p.QualityIDs,
			UpgradeAllowed: p.UpgradeAllowed,
			CutoffQuality:  p.CutoffQuality,
		}
	}
	return out
}

func toTemplProfile(p QualityProfile) templates.QualityProfile {
	return templates.QualityProfile{
		ID:             p.ID,
		Name:           p.Name,
		QualityIDs:     p.QualityIDs,
		UpgradeAllowed: p.UpgradeAllowed,
		CutoffQuality:  p.CutoffQuality,
	}
}

func (h *Handler) QualityProfilesPage(w http.ResponseWriter, r *http.Request) {
	defs := toTemplDefs(h.Quality.ListDefinitions())
	profiles := toTemplProfiles(h.Quality.ListProfiles())

	content := templates.QualityProfilesPage(defs, profiles)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Quality Profiles", nav, content)
	h.render(w, r, component)
}

func (h *Handler) QualityProfileCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		toast(w, "error", "Invalid form")
		http.Redirect(w, r, "/quality-profiles", http.StatusSeeOther)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		toast(w, "error", "Name is required")
		http.Redirect(w, r, "/quality-profiles", http.StatusSeeOther)
		return
	}

	qualityIDs := r.Form["quality_ids"]
	if len(qualityIDs) == 0 {
		toast(w, "error", "At least one quality required")
		http.Redirect(w, r, "/quality-profiles", http.StatusSeeOther)
		return
	}

	h.Quality.SaveProfile(QualityProfile{
		Name:           name,
		QualityIDs:     qualityIDs,
		UpgradeAllowed: r.FormValue("upgrade_allowed") == "on",
		CutoffQuality:  r.FormValue("cutoff_quality"),
	})

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "quality_profile.create", "quality", name, nil)
	toast(w, "success", "Profile created")
	http.Redirect(w, r, "/quality-profiles", http.StatusSeeOther)
}

func (h *Handler) QualityProfileUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		toast(w, "error", "Invalid form")
		http.Redirect(w, r, "/quality-profiles", http.StatusSeeOther)
		return
	}

	qualityIDs := r.Form["quality_ids"]
	if len(qualityIDs) == 0 {
		toast(w, "error", "At least one quality required")
		http.Redirect(w, r, "/quality-profiles", http.StatusSeeOther)
		return
	}

	h.Quality.SaveProfile(QualityProfile{
		ID:             id,
		Name:           r.FormValue("name"),
		QualityIDs:     qualityIDs,
		UpgradeAllowed: r.FormValue("upgrade_allowed") == "on",
		CutoffQuality:  r.FormValue("cutoff_quality"),
	})

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "quality_profile.update", "quality", id, nil)
	toast(w, "success", "Profile updated")
	http.Redirect(w, r, "/quality-profiles", http.StatusSeeOther)
}

func (h *Handler) QualityProfileDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "default" {
		toast(w, "error", "Cannot delete default profile")
		http.Redirect(w, r, "/quality-profiles", http.StatusSeeOther)
		return
	}

	h.Quality.DeleteProfile(id)
	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "quality_profile.delete", "quality", id, nil)
	toast(w, "success", "Profile deleted")
	http.Redirect(w, r, "/quality-profiles", http.StatusSeeOther)
}

func (h *Handler) QualityDefUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		toast(w, "error", "Invalid form")
		http.Redirect(w, r, "/quality-profiles", http.StatusSeeOther)
		return
	}

	res, _ := strconv.Atoi(r.FormValue("resolution"))
	h.Quality.UpdateDefinition(QualityDefinition{
		ID:         id,
		Name:       r.FormValue("name"),
		Resolution: res,
		Allowed:    r.FormValue("allowed") == "on",
	})

	toast(w, "success", "Quality updated")
	http.Redirect(w, r, "/quality-profiles", http.StatusSeeOther)
}

func (h *Handler) QualityProfileEditForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := h.Quality.GetProfile(id)
	if !ok {
		toast(w, "error", "Profile not found")
		return
	}
	defs := toTemplDefs(h.Quality.ListDefinitions())
	component := templates.QualityProfileEditForm(id, toTemplProfile(p), defs)
	h.render(w, r, component)
}

func (h *Handler) QualityProfilesJSON(w http.ResponseWriter, r *http.Request) {
	defs := h.Quality.ListDefinitions()
	profiles := h.Quality.ListProfiles()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"definitions": defs,
		"profiles":    profiles,
	})
}
