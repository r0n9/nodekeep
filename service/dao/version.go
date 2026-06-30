package dao

var (
	Version     = "v1.0.0"
	ReleaseDate string
)

func DisplayVersion() string {
	if ReleaseDate == "" {
		return Version
	}
	return Version + " (" + ReleaseDate + ")"
}
