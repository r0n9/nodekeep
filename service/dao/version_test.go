package dao

import "testing"

func TestDisplayVersion(t *testing.T) {
	oldVersion := Version
	oldReleaseDate := ReleaseDate
	t.Cleanup(func() {
		Version = oldVersion
		ReleaseDate = oldReleaseDate
	})

	Version = "v1.2.3"
	ReleaseDate = ""
	if got, want := DisplayVersion(), "v1.2.3"; got != want {
		t.Fatalf("DisplayVersion() = %q, want %q", got, want)
	}

	ReleaseDate = "2026-06-30"
	if got, want := DisplayVersion(), "v1.2.3 (2026-06-30)"; got != want {
		t.Fatalf("DisplayVersion() = %q, want %q", got, want)
	}
}
