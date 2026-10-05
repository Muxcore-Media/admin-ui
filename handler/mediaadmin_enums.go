package handler

import (
	"strings"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
)

// historyEventTypeFromString maps the admin UI's lowercase activity filter
// values ("grab", "import", "delete_item", "delete_file") to the
// contracts-media-admin HistoryEventType enum. Unknown or empty values map to
// HISTORY_EVENT_TYPE_UNSPECIFIED, which means "all events".
func historyEventTypeFromString(s string) mediaadminv1.HistoryEventType {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_UNSPECIFIED
	}
	if v, ok := mediaadminv1.HistoryEventType_value["HISTORY_EVENT_TYPE_"+strings.ToUpper(s)]; ok {
		return mediaadminv1.HistoryEventType(v)
	}
	return mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_UNSPECIFIED
}

// historyEventTypeString is the inverse of historyEventTypeFromString; it
// returns "" for HISTORY_EVENT_TYPE_UNSPECIFIED and unknown values.
func historyEventTypeString(t mediaadminv1.HistoryEventType) string {
	if t == mediaadminv1.HistoryEventType_HISTORY_EVENT_TYPE_UNSPECIFIED {
		return ""
	}
	name, ok := mediaadminv1.HistoryEventType_name[int32(t)]
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(name, "HISTORY_EVENT_TYPE_"))
}

// sortFieldFromString maps a ?sort= query value ("title", "year",
// "created_at", "updated_at", "runtime", "rating") to the SortField enum.
// Unknown or empty values map to SORT_FIELD_UNSPECIFIED (module default).
func sortFieldFromString(s string) mediaadminv1.SortField {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return mediaadminv1.SortField_SORT_FIELD_UNSPECIFIED
	}
	if v, ok := mediaadminv1.SortField_value["SORT_FIELD_"+strings.ToUpper(s)]; ok {
		return mediaadminv1.SortField(v)
	}
	return mediaadminv1.SortField_SORT_FIELD_UNSPECIFIED
}
