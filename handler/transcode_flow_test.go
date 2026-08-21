package handler

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func TestFlowInitJSON(t *testing.T) {
	raw := flowInitJSON(
		[]templates.TranscodeProfileRow{{ID: "hevc_gpu", Name: "HEVC"}},
		&templates.TranscodeSetupRow{
			Steps:   []templates.TranscodeSetupStepRow{{StepType: "filter.skip_if_codec", ConfigJSON: `{"codecs":["hevc"]}`}},
			Outputs: []templates.TranscodeSetupOutputRow{{ProfileID: "hevc_gpu", Suffix: "-hevc", ReplaceExtension: true}},
		},
	)
	if !strings.Contains(raw, `"hevc_gpu"`) || !strings.Contains(raw, `"filter.skip_if_codec"`) {
		t.Fatalf("unexpected flow json: %s", raw)
	}
}

func TestParseSetupFormFromFlowHiddenFields(t *testing.T) {
	form := url.Values{}
	form.Set("name", "Flow test")
	form.Set("enabled", "1")
	form.Set("library_paths", "/data/media")
	form.Set("trigger", "manual")
	form.Set("source_disposition", "keep")
	form.Set("steps", `filter.skip_if_codec,{"codecs":["hevc"]}`)
	form.Set("outputs", "hevc_gpu,-hevc,true")
	req := httptest.NewRequest("POST", "/transcode/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = req.ParseForm()

	setup, err := parseSetupForm(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.GetSteps()) != 1 || setup.GetSteps()[0].GetStepType() != "filter.skip_if_codec" {
		t.Fatalf("steps: %+v", setup.GetSteps())
	}
	if len(setup.GetOutputs()) != 1 || setup.GetOutputs()[0].GetProfileId() != "hevc_gpu" {
		t.Fatalf("outputs: %+v", setup.GetOutputs())
	}
}
