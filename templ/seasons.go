package templates

// SeasonView is a TV season row for the admin detail season tree.
type SeasonView struct {
	ID        string
	Number    int
	Name      string
	Monitored bool
	Episodes  []EpisodeView
}

// EpisodeView is a TV episode row for the admin detail season tree.
type EpisodeView struct {
	ID        string
	Number    int
	Absolute  int
	Name      string
	AirDate   string
	Monitored bool
	HasFile   bool
}

// MediaFileView is a library file row on item detail.
type MediaFileView struct {
	ID        string
	Path      string
	Quality   string
	Size      int64
	Container string
}

// AlternateTitleView is an alternate title row on item detail.
type AlternateTitleView struct {
	ID     string
	Title  string
	Source string
}

// TrailerView is a TMDB video / trailer link on movie detail.
type TrailerView struct {
	Name string
	Type string
	URL  string
}
