package main

import "testing"

func TestReleaseVersion(t *testing.T) {
	if appVersion != "v0.5.1" {
		t.Fatalf("release version = %s, want v0.5.1", appVersion)
	}
}
