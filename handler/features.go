package handler

import (
	"strings"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
)

// mediaFeatureEnabled reports whether a module advertises a named capability.
// name uses the legacy string labels ("missing", "tags", …) from v0.1.0.
func mediaFeatureEnabled(features []mediaadminv1.Feature, name string) bool {
	want := featureFromName(name)
	if want == mediaadminv1.Feature_FEATURE_UNSPECIFIED {
		return false
	}
	for _, f := range features {
		if f == want {
			return true
		}
	}
	return false
}

func featureFromName(name string) mediaadminv1.Feature {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "missing":
		return mediaadminv1.Feature_FEATURE_MISSING
	case "tags":
		return mediaadminv1.Feature_FEATURE_TAGS
	case "collections":
		return mediaadminv1.Feature_FEATURE_COLLECTIONS
	case "calendar":
		return mediaadminv1.Feature_FEATURE_CALENDAR
	default:
		return mediaadminv1.Feature_FEATURE_UNSPECIFIED
	}
}
