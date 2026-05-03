package main

import (
	"os"
	"strings"
	"testing"
)

func TestPRDDocumentsFocusedMonitorScopeAndMultiMonitorSpanningOutOfScope(t *testing.T) {
	data, err := os.ReadFile("PRD.md")
	if err != nil {
		t.Fatalf("read PRD.md: %v", err)
	}
	prd := string(data)
	for _, want := range []string{
		"focused monitor only",
		"Multi-monitor coordinate spanning",
		"Multi-monitor selection / spanning is out of scope",
	} {
		if !strings.Contains(prd, want) {
			t.Fatalf("PRD.md does not contain %q", want)
		}
	}
}
