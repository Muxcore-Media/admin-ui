package templates

type FormatRow struct {
	ID        string
	Name      string
	Score     int
	RuleCount int
}

type FormatEdit struct {
	ID        string
	Name      string
	Score     int
	RulesText string
}

// TrashSyncResult is shown after POST /formats/sync-trash.
type TrashSyncResult struct {
	FormatsUpserted  int
	FormatsSkipped   int
	ProfilesUpserted int
	GuidesPath       string
	Warnings         []string
	Error            string
}

type FormatsListPageData struct {
	Formats        []FormatRow
	Error          string
	SyncResult     *TrashSyncResult
	ScoreSet       string
	Services       []string // selected: radarr, sonarr
	ImportProfiles bool
}

func DefaultScoreSet(s string) string {
	if s == "" {
		return "default"
	}
	return s
}

func ServiceSelected(services []string, name string) bool {
	if len(services) == 0 {
		return true
	}
	for _, s := range services {
		if s == name {
			return true
		}
	}
	return false
}
