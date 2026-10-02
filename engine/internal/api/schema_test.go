package api

import (
	"testing"
)

func validateOutput(t *testing.T, r map[string]any) {
	t.Helper()
	if err := ValidateOutput(r); err != nil {
		t.Fatal("contract violation", err, r)
	}
}
