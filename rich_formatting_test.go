package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestRichFormattingRendering(t *testing.T) {
	// Use a predictable profile for assertion matching
	lipgloss.SetColorProfile(termenv.ANSI)

	m := &Model{renderingMode: RenderingModeDashboard,
		showLogs: true, // Diagnostics are now shown in LOG view only
		diagnostics: []Diagnostic{
			{
				Severity: "error",
				Summary:  "Invalid expression",
				Detail: []DiagnosticLine{
					{Content: "  on main.tf line 1:", IsMarker: true},
					{Content: "    ^", IsMarker: false},
				},
				Expanded: true,
			},
		},
	}
	m.rebuildLines()

	// Check that diagnostic is rendered as log line
	// Line 0 should be the error summary
	if len(m.lines) == 0 {
		t.Fatal("Expected lines to be rendered")
	}

	header := m.renderLogLine(m.lines[0].Content, false)
	// Should contain "Error:"
	if !strings.Contains(header, "Error:") {
		t.Error("Header should contain 'Error:'")
	}
	// Check for ANSI styling (Error: lines get styled)
	if !strings.Contains(header, "\x1b[") {
		t.Error("Error header should contain ANSI styling codes")
	}

	// Check Marker line (line 1)
	if len(m.lines) < 2 {
		t.Fatal("Expected at least 2 lines")
	}
	markerLine := m.renderLogLine(m.lines[1].Content, false)
	// Should contain the marker text
	if !strings.Contains(markerLine, "on main.tf line 1:") {
		t.Error("Marker line should contain 'on main.tf line 1:'")
	}

	// Check Underline line (line 2)
	if len(m.lines) < 3 {
		t.Fatal("Expected at least 3 lines")
	}
	underlineLine := m.renderLogLine(m.lines[2].Content, false)
	// Should contain ^
	if !strings.Contains(underlineLine, "^") {
		t.Error("Underline line should contain ^")
	}
	// Check for ANSI codes (renderLogLine applies styling)
	if !strings.Contains(underlineLine, "\x1b[") {
		t.Error("Underline line should contain ANSI codes")
	}
}

func TestANSIUnderlinePreservation(t *testing.T) {
	input := "   3: provider \"aws\" \x1b[4m{\x1b[0m"
	sanitized := sanitizeTerraformANSI(input)

	// Should contain [4m and NOT [0m
	if !strings.Contains(sanitized, "\x1b[4m") {
		t.Errorf("Expected preserved underline code [4m, got %q", sanitized)
	}
	if strings.Contains(sanitized, "\x1b[0m") {
		t.Errorf("Expected stripped reset code [0m, got %q", sanitized)
	}
}

func TestSanitizeBytes(t *testing.T) {
	input := "\x1b[0m"
	got := sanitizeTerraformANSI(input)
	expected := ""
	if got != expected {
		t.Errorf("Expected empty string for reset code, got %x", got)
	}
}

func TestANSISequencePreservation(t *testing.T) {
	// \x1b[H is cursor to top-left (CSI, but not SGR)
	input := "\x1b[HHello"
	got := stripANSI(input)

	if !strings.Contains(got, "\x1b[H") {
		t.Errorf("Expected non-SGR sequence to be preserved, got %q", got)
	}

	// SGR should still be stripped
	input = "\x1b[31mRed\x1b[0m"
	got = stripANSI(input)
	if strings.Contains(got, "\x1b") {
		t.Errorf("Expected SGR sequences to be stripped, got %q", got)
	}
}
