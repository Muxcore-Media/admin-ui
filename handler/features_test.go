package handler

import (
	"testing"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
)

func TestMediaFeatureEnabledEnumFeatures(t *testing.T) {
	features := []mediaadminv1.Feature{
		mediaadminv1.Feature_FEATURE_MISSING,
		mediaadminv1.Feature_FEATURE_TAGS,
	}
	if !mediaFeatureEnabled(features, "missing") {
		t.Fatal("expected FEATURE_MISSING to enable missing gate")
	}
	if !mediaFeatureEnabled(features, "tags") {
		t.Fatal("expected FEATURE_TAGS to enable tags gate")
	}
	if mediaFeatureEnabled(features, "collections") {
		t.Fatal("collections should not be enabled")
	}
	if mediaFeatureEnabled(nil, "missing") {
		t.Fatal("nil features should not enable missing")
	}
	if mediaFeatureEnabled(features, "unknown") {
		t.Fatal("unknown feature name should not match")
	}
}
