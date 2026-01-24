package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestRichFormattingRendering(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	
	m := &Model{
		renderingMode: RenderingModeDashboard,
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
