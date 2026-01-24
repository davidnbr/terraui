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

	// Check Header (Error: ...)
	// Line 0 is header.
	header := m.renderDiagnosticLine(m.lines[0], false)
	// Should contain "Error:"
	if !strings.Contains(header, "Error:") {
		t.Error("Header should contain 'Error:'")
	}

	// Check Marker (Bold)
	// Line 1 is detail 0.
	markerLine := m.renderDiagnosticDetailLine(m.lines[1], false)
	// Should contain bold sequence? Lipgloss bold is usually "\x1b[1m"
	// Or check if it's NOT just plain text.
	if markerLine == "    "+m.lines[1].Content {
		t.Error("Marker line should be styled (bold)")
	}

	// Check Underline (^)
	// Line 2 is detail 1.
	underlineLine := m.renderDiagnosticDetailLine(m.lines[2], false)
	// Should contain colored ^.
	// Since we replace ^ with styled ^, output should be longer/different.
	if !strings.Contains(underlineLine, "^") {
		t.Error("Underline line should contain ^")
	}
	// Check for ANSI
	if !strings.Contains(underlineLine, "\x1b") {
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
