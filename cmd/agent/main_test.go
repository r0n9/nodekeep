package main

import "testing"

func TestAgentDefaultVersion(t *testing.T) {
	if version != "develop" {
		t.Fatalf("version = %q, want develop", version)
	}
}

func TestShouldSelfUpdate(t *testing.T) {
	oldVersion := version
	t.Cleanup(func() {
		version = oldVersion
	})

	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{name: "develop", version: "develop", want: false},
		{name: "empty", version: "", want: false},
		{name: "tag version", version: "v1.2.3", want: true},
		{name: "plain semver", version: "1.2.3", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version = tt.version
			if got := shouldSelfUpdate(); got != tt.want {
				t.Fatalf("shouldSelfUpdate() = %v, want %v", got, tt.want)
			}
		})
	}
}
