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
