package main

import "testing"

func TestNoCommandReturnsUsageInNonInteractiveContext(t *testing.T) {
	if err := run(t.Context(), nil); err != nil {
		t.Fatalf("run with no command: %v", err)
	}
}
