package main

import "testing"

func TestReleaseVersion(t *testing.T) {
	if appVersion != "v0.5.0" {
		t.Fatalf("release version = %s, want v0.5.0", appVersion)
	}
}
