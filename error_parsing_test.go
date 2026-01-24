package main

import (
	"strings"
	"testing"
)

func TestDiagnosticLineSemanticParsing(t *testing.T) {
	input := []string{
		"Error: Invalid expression",
		"",
		"  on reproduce_long_error.tf line 7, in variable \"test_long_error\":",
		"   7:     error_message = \"foo\"",
		"",
	}
	
diag := parseDiagnosticBlock(input)
	
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	
	// Detail[0] should be empty line (skipped?)
	// Let's check what parseDiagnosticBlock does. It skips empty lines at start?
	// "trimmed == \"\"" -> continue.
	// So line 1 is skipped.
	// Line 2: "on reproduce..."
	
	if len(diag.Detail) == 0 {
		t.Fatal("expected details")
	}
	
	// Find the marker line
	foundMarker := false
	for _, line := range diag.Detail {
		if strings.Contains(line.Content, "on reproduce_long_error.tf") {
			if line.IsMarker {
				foundMarker = true
			}
		}
	}
	
	if !foundMarker {
		t.Error("marker line not found or not marked")
	}
}

func TestUnderlinePatternDetection(t *testing.T) {
	tests := []struct {
		line  string
		match bool
	}{
		{"    ^", true},
		{"    ~~~~", true},
		{"    error_message = ...", false},
		{"^", true},
	}
	
	for _, tt := range tests {
		if underlinePattern.MatchString(tt.line) != tt.match {
			t.Errorf("line %q match=%v, expected %v", tt.line, !tt.match, tt.match)
		}
	}
}

