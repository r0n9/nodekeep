package dao

var (
	Version     = "develop"
	ReleaseDate string
)

func DisplayVersion() string {
	if ReleaseDate == "" {
		return Version
	}
	return Version + " (" + ReleaseDate + ")"
}
