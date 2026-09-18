package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	transcodev1 "github.com/Muxcore-Media/media-transcoder/proto/transcodev1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaTranscoderAdmin  = "media.transcoder"
	transcodeDialTimeout     = 3 * time.Second
	transcodeReadTimeout     = 5 * time.Second
	transcodePageTimeout     = transcodeDialTimeout + 7*transcodeReadTimeout + time.Second
	transcodeEditPageTimeout = transcodeDialTimeout + transcodeReadTimeout + time.Second
)

func (h *Handler) withTranscoderClient(ctx context.Context) (transcodev1.TranscodeServiceClient, func(), error) {
	if h.Core == nil {
		return nil, nil, fmt.Errorf("core unavailable")
	}
	for _, cap := range []string{capMediaTranscoderAdmin, capTranscoder} {
		mods, err := h.Core.Discovery.FindByCapability(ctx, cap)
		if err != nil {
			return nil, nil, err
		}
		if len(mods) == 0 {
			continue
		}
		mod := mods[0]
		addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
		if addr == "" {
			return nil, nil, fmt.Errorf("transcoder module has no dial address")
		}
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		return transcodev1.NewTranscodeServiceClient(conn), func() { _ = conn.Close() }, nil
	}
	return nil, nil, fmt.Errorf("no module with capability %s", capMediaTranscoderAdmin)
}

func (h *Handler) TranscodePage(w http.ResponseWriter, r *http.Request) {
	data := templates.TranscodePageData{
		Flash: r.URL.Query().Get("ok"),
		Error: r.URL.Query().Get("error"),
	}
	pageCtx, pageCancel := context.WithTimeout(r.Context(), transcodePageTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, transcodeDialTimeout)
	client, closer, err := h.withTranscoderClient(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftEmpty = true
		data.Error = err.Error()
		h.renderTranscode(w, r, data)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, transcodeReadTimeout)
	jobs, err := client.ListJobs(readCtx, &transcodev1.ListJobsRequest{Page: 1, PageSize: 50, Status: "all"})
	readCancel()
	if err == nil {
		for _, j := range jobs.GetJobs() {
			data.Jobs = append(data.Jobs, mapJobRow(j))
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, transcodeReadTimeout)
	profs, err := client.ListProfiles(readCtx, &transcodev1.ListProfilesRequest{})
	readCancel()
	if err == nil {
		for _, p := range profs.GetProfiles() {
			data.Profiles = append(data.Profiles, mapProfileRow(p))
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, transcodeReadTimeout)
	hw, err := client.DetectHardware(readCtx, &transcodev1.DetectHardwareRequest{})
	readCancel()
	if err == nil {
		for _, d := range hw.GetDevices() {
			data.Hardware = append(data.Hardware, mapHardwareRow(d))
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, transcodeReadTimeout)
	setups, err := client.ListSetups(readCtx, &transcodev1.ListSetupsRequest{})
	readCancel()
	if err == nil {
		for _, s := range setups.GetSetups() {
			data.Setups = append(data.Setups, mapSetupRow(s))
		}
	} else if data.Error == "" {
		data.Error = err.Error()
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, transcodeReadTimeout)
	runs, err := client.ListPipelineRuns(readCtx, &transcodev1.ListPipelineRunsRequest{Page: 1, PageSize: 25})
	readCancel()
	if err == nil {
		for _, run := range runs.GetRuns() {
			row := mapPipelineRunRow(run)
			if run.GetStatus() == "pending_review" {
				data.ReviewRuns = append(data.ReviewRuns, row)
			} else {
				data.Runs = append(data.Runs, row)
			}
		}
	}

	if editID := strings.TrimSpace(r.URL.Query().Get("id")); editID != "" {
		readCtx, readCancel = context.WithTimeout(pageCtx, transcodeReadTimeout)
		resp, err := client.GetSetup(readCtx, &transcodev1.GetSetupRequest{Id: editID})
		readCancel()
		if err == nil && resp.GetSetup() != nil {
			row := mapSetupRow(resp.GetSetup())
			data.Edit = &row
		}
	}
	if data.Edit != nil {
		data.FlowInitJSON = flowInitJSON(data.Profiles, data.Edit)
	}
	if data.Edit != nil && data.Edit.ID != "" {
		readCtx, readCancel = context.WithTimeout(pageCtx, transcodeReadTimeout)
		tpls, err := client.ListStepTemplates(readCtx, &transcodev1.ListStepTemplatesRequest{})
		readCancel()
		if err == nil {
			for _, t := range tpls.GetTemplates() {
				data.StepTemplates = append(data.StepTemplates, templates.TranscodeStepTemplateRow{
					ID: t.GetId(), Name: t.GetName(), Description: t.GetDescription(),
				})
			}
		}
	}
	h.renderTranscode(w, r, data)
}

func (h *Handler) TranscodeEditPage(w http.ResponseWriter, r *http.Request) {
	editID := strings.TrimSpace(r.URL.Query().Get("id"))
	if editID != "" {
		r.URL.Path = "/transcode"
		r.URL.RawQuery = "id=" + url.QueryEscape(editID)
		h.TranscodePage(w, r)
		return
	}
	data := templates.TranscodePageData{
		Edit: &templates.TranscodeSetupRow{
			Enabled:           true,
			Trigger:           "on_import",
			SourceDisposition: "keep",
			LibraryPaths:      "/data/media",
			Outputs: []templates.TranscodeSetupOutputRow{
				{ProfileID: "hevc_gpu", Suffix: "-hevc", ReplaceExtension: true, Enabled: true},
			},
			Steps: []templates.TranscodeSetupStepRow{
				{StepType: "filter.skip_if_codec", ConfigJSON: `{"codecs":["hevc","h265"]}`, Enabled: true},
			},
		},
	}
	pageCtx, pageCancel := context.WithTimeout(r.Context(), transcodeEditPageTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, transcodeDialTimeout)
	client, closer, err := h.withTranscoderClient(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftEmpty = true
		data.Error = err.Error()
	} else {
		defer closer()
		readCtx, readCancel := context.WithTimeout(pageCtx, transcodeReadTimeout)
		profs, err := client.ListProfiles(readCtx, &transcodev1.ListProfilesRequest{})
		readCancel()
		if err == nil {
			for _, p := range profs.GetProfiles() {
				data.Profiles = append(data.Profiles, mapProfileRow(p))
			}
		}
	}
	data.FlowInitJSON = flowInitJSON(data.Profiles, data.Edit)
	h.renderTranscode(w, r, data)
}

func (h *Handler) TranscodeSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	setup, err := parseSetupForm(r)
	if err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), transcodeReadTimeout)
	defer cancel()
	client, closer, err := h.withTranscoderClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.UpsertSetup(ctx, &transcodev1.UpsertSetupRequest{Setup: setup}); err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/transcode?ok=setup+saved", http.StatusSeeOther)
}

func (h *Handler) TranscodeDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), transcodeReadTimeout)
	defer cancel()
	client, closer, err := h.withTranscoderClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.DeleteSetup(ctx, &transcodev1.DeleteSetupRequest{Id: id}); err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/transcode?ok=deleted", http.StatusSeeOther)
}

func (h *Handler) TranscodeCancelJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape("job id required"), http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), transcodeReadTimeout)
	defer cancel()
	client, closer, err := h.withTranscoderClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.CancelJob(ctx, &transcodev1.CancelJobRequest{JobId: id}); err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/transcode?ok=cancelled", http.StatusSeeOther)
}

func (h *Handler) TranscodeScan(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), transcodeReadTimeout)
	defer cancel()
	client, closer, err := h.withTranscoderClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.ScanSetups(ctx, &transcodev1.ScanSetupsRequest{})
	if err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("queued %d file(s)", resp.GetFilesQueued())
	http.Redirect(w, r, "/transcode?ok="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) TranscodeReviewApprove(w http.ResponseWriter, r *http.Request) {
	h.transcodeReviewAction(w, r, true)
}

func (h *Handler) TranscodeReviewReject(w http.ResponseWriter, r *http.Request) {
	h.transcodeReviewAction(w, r, false)
}

func (h *Handler) transcodeReviewAction(w http.ResponseWriter, r *http.Request, approve bool) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), transcodeReadTimeout)
	defer cancel()
	client, closer, err := h.withTranscoderClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if approve {
		if _, err := client.ApprovePipelineRun(ctx, &transcodev1.ApprovePipelineRunRequest{Id: id}); err != nil {
			http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/transcode?ok=review+approved", http.StatusSeeOther)
		return
	}
	if _, err := client.RejectPipelineRun(ctx, &transcodev1.RejectPipelineRunRequest{Id: id}); err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/transcode?ok=review+rejected", http.StatusSeeOther)
}

func (h *Handler) TranscodeApplyTemplate(w http.ResponseWriter, r *http.Request) {
	setupID := strings.TrimSpace(r.PathValue("id"))
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	templateID := strings.TrimSpace(r.FormValue("template_id"))
	if setupID == "" || templateID == "" {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape("setup and template required"), http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), transcodeReadTimeout)
	defer cancel()
	client, closer, err := h.withTranscoderClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/transcode?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.ApplyStepTemplate(ctx, &transcodev1.ApplyStepTemplateRequest{
		SetupId: setupID, TemplateId: templateID, ReplaceSteps: r.FormValue("replace_steps") == "1",
	}); err != nil {
		http.Redirect(w, r, "/transcode?id="+url.QueryEscape(setupID)+"&error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/transcode?id="+url.QueryEscape(setupID)+"&ok=template+applied", http.StatusSeeOther)
}

func mapPipelineRunRow(run *transcodev1.PipelineRun) templates.TranscodePipelineRunRow {
	row := templates.TranscodePipelineRunRow{
		ID: run.GetId(), SetupName: run.GetSetupName(), InputPath: run.GetInputPath(),
		Status: run.GetStatus(), SkipReason: run.GetSkipReason(), Error: run.GetError(),
		CreatedAt: run.GetCreatedAt(), PendingReview: run.GetStatus() == "pending_review",
	}
	for _, out := range run.GetOutputs() {
		row.Outputs = append(row.Outputs, out.GetOutputPath())
	}
	return row
}

func (h *Handler) renderTranscode(w http.ResponseWriter, r *http.Request, data templates.TranscodePageData) {
	content := templates.TranscodeAdminPage(data)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Transcoder", nav, content))
}

func mapProfileRow(p *transcodev1.TranscodeProfile) templates.TranscodeProfileRow {
	return templates.TranscodeProfileRow{
		ID: p.GetId(), Name: p.GetName(), VideoCodec: p.GetVideoCodec(),
		AudioCodec: p.GetAudioCodec(), Preset: p.GetPreset(), UseGPU: p.GetUseGpu(),
		Container: p.GetContainer(),
	}
}

func mapJobRow(j *transcodev1.TranscodeJob) templates.TranscodeJobRow {
	st := strings.ToLower(j.GetStatus())
	return templates.TranscodeJobRow{
		ID: j.GetId(), InputPath: j.GetInputPath(), ProfileName: j.GetProfileName(),
		Status: j.GetStatus(), ProgressPct: fmt.Sprintf("%.0f%%", j.GetProgress()*100),
		Error: j.GetError(), CreatedAt: j.GetCreatedAt(),
		Cancellable: st == "queued" || st == "running",
	}
}

func mapHardwareRow(d *transcodev1.HardwareDevice) templates.TranscodeHardwareRow {
	return templates.TranscodeHardwareRow{
		Name: d.GetName(), Type: d.GetType(), Encoder: d.GetEncoder(), Available: d.GetAvailable(),
	}
}

func mapSetupRow(s *transcodev1.TranscodeSetup) templates.TranscodeSetupRow {
	row := templates.TranscodeSetupRow{
		ID: s.GetId(), Name: s.GetName(), Enabled: s.GetEnabled(), HoldForReview: s.GetHoldForReview(),
		LibraryPaths: strings.Join(s.GetLibraryPaths(), "\n"), Trigger: s.GetTrigger(),
		SourceDisposition: s.GetSourceDisposition(), ArchivePath: s.GetArchivePath(),
	}
	for _, o := range s.GetOutputs() {
		row.Outputs = append(row.Outputs, templates.TranscodeSetupOutputRow{
			ID: o.GetId(), ProfileID: o.GetProfileId(), Suffix: o.GetSuffix(),
			ReplaceExtension: o.GetReplaceExtension(), Enabled: o.GetEnabled(),
		})
	}
	for _, st := range s.GetSteps() {
		row.Steps = append(row.Steps, templates.TranscodeSetupStepRow{
			ID: st.GetId(), StepType: st.GetStepType(), ConfigJSON: st.GetConfigJson(), Enabled: st.GetEnabled(),
		})
	}
	return row
}

func parseSetupForm(r *http.Request) (*transcodev1.TranscodeSetup, error) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	var paths []string
	for _, line := range strings.Split(r.FormValue("library_paths"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	setup := &transcodev1.TranscodeSetup{
		Id:                strings.TrimSpace(r.FormValue("id")),
		Name:              name,
		Enabled:           r.FormValue("enabled") == "1",
		HoldForReview:     r.FormValue("hold_for_review") == "1",
		LibraryPaths:      paths,
		Trigger:           strings.TrimSpace(r.FormValue("trigger")),
		SourceDisposition: strings.TrimSpace(r.FormValue("source_disposition")),
		ArchivePath:       strings.TrimSpace(r.FormValue("archive_path")),
	}
	for _, line := range strings.Split(r.FormValue("outputs"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 1 {
			continue
		}
		suffix := ""
		replace := false
		if len(parts) > 1 {
			suffix = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 {
			replace = strings.EqualFold(strings.TrimSpace(parts[2]), "true")
		}
		setup.Outputs = append(setup.Outputs, &transcodev1.SetupOutput{
			ProfileId: strings.TrimSpace(parts[0]), Suffix: suffix, ReplaceExtension: replace, Enabled: true,
		})
	}
	if len(setup.Outputs) == 0 {
		return nil, fmt.Errorf("at least one output is required")
	}
	for _, line := range strings.Split(r.FormValue("steps"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		if len(parts) < 2 {
			continue
		}
		setup.Steps = append(setup.Steps, &transcodev1.PipelineStep{
			StepType: strings.TrimSpace(parts[0]), ConfigJson: strings.TrimSpace(parts[1]), Enabled: true,
		})
	}
	return setup, nil
}

func flowInitJSON(profiles []templates.TranscodeProfileRow, edit *templates.TranscodeSetupRow) string {
	type profile struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type step struct {
		ID         string `json:"id"`
		StepType   string `json:"stepType"`
		ConfigJSON string `json:"configJSON"`
		Enabled    bool   `json:"enabled"`
	}
	type output struct {
		ID               string `json:"id"`
		ProfileID        string `json:"profileId"`
		Suffix           string `json:"suffix"`
		ReplaceExtension bool   `json:"replaceExtension"`
		Enabled          bool   `json:"enabled"`
	}
	type payload struct {
		Profiles []profile `json:"profiles"`
		Steps    []step    `json:"steps"`
		Outputs  []output  `json:"outputs"`
	}
	p := payload{}
	for _, prof := range profiles {
		p.Profiles = append(p.Profiles, profile{ID: prof.ID, Name: prof.Name})
	}
	if edit != nil {
		for _, s := range edit.Steps {
			p.Steps = append(p.Steps, step{
				ID: s.ID, StepType: s.StepType, ConfigJSON: s.ConfigJSON, Enabled: s.Enabled,
			})
		}
		for _, o := range edit.Outputs {
			p.Outputs = append(p.Outputs, output{
				ID: o.ID, ProfileID: o.ProfileID, Suffix: o.Suffix,
				ReplaceExtension: o.ReplaceExtension, Enabled: o.Enabled,
			})
		}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "{}"
	}
	return string(b)
}
