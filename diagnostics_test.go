package main

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestLongDiagnosticMessage(t *testing.T) {
	m := &Model{
		streamChan: make(chan StreamMsg, 10),
	}

	// Construct a massive diagnostic message
	longLine := strings.Repeat("A", 10000)
	longSummary := "Error: " + longLine

	input := "╷\n│ " + longSummary + "\n│ \n│ Detail line 1\n│ " + longLine + "\n╵\n"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go m.readInputStream(ctx, strings.NewReader(input))

	var diagnostic *Diagnostic
	for {
		msg, ok := <-m.streamChan
		if !ok || msg.Done {
			break
		}
		if msg.Diagnostic != nil {
			diagnostic = msg.Diagnostic
		}
	}

	if diagnostic == nil {
		t.Fatal("expected diagnostic to be parsed")
	}

	if len(diagnostic.Summary) != 10000 {
		t.Errorf("expected summary length 10000, got %d", len(diagnostic.Summary))
	}

		// Check details
		foundLongDetail := false
		for _, d := range diagnostic.Detail {
			if len(d.Content) == 10000 {
				foundLongDetail = true
				break
			}
		}
		
		if !foundLongDetail {
			t.Error("expected to find long detail line of length 10000")
		}
	}
	
	func TestDiagnosticSummaryWrapping(t *testing.T) {	lipgloss.SetColorProfile(termenv.Ascii)
	m := &Model{
		width: 20,
		diagnostics: []Diagnostic{
			{
				Severity: "error",
				Summary:  "This is a very long summary that should be wrapped",
				Expanded: false,
			},
		},
	}

	m.rebuildLines()

	if len(m.lines) == 1 {
		t.Error("Diagnostic summary was not wrapped")
	}
}

func TestRealWorldDiagnosticParsing(t *testing.T) {
	m := &Model{
		streamChan: make(chan StreamMsg, 10),
	}
	// Simulated output from terraform plan with validation error
	input := "╷\n│ Error: Invalid value for variable\n│ \n│   on reproduce_long_error.tf line 1:\n│    1: variable \"test_long_error\" {\n│     ├────────────────\n│     │ var.test_long_error is \"trigger_failure\"\n│ \n│ Lorem ipsum dolor sit amet...\n│ \n│ This was checked by the validation rule.\n╵\n"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go m.readInputStream(ctx, strings.NewReader(input))

	var diagnostic *Diagnostic
	for {
		msg, ok := <-m.streamChan
		if !ok || msg.Done {
			break
		}
		if msg.Diagnostic != nil {
			diagnostic = msg.Diagnostic
		}
	}

	if diagnostic == nil {
		t.Fatal("expected diagnostic to be parsed")
	}

	if diagnostic.Summary != "Invalid value for variable" {
		t.Errorf("expected summary 'Invalid value for variable', got %q", diagnostic.Summary)
	}

	// Check that details contain the Lorem ipsum text
	foundLorem := false
	for _, d := range diagnostic.Detail {
		if strings.Contains(d.Content, "Lorem ipsum") {
			foundLorem = true
			break
		}
	}
	if !foundLorem {
		t.Error("expected details to contain Lorem ipsum line")
	}
	
	// Verify indentation preserved (roughly)
	// Input: "│   on reproduce..." -> Content: "  on reproduce..."
	foundOnLine := false
	for _, d := range diagnostic.Detail {
		if strings.Contains(d.Content, "on reproduce_long_error.tf") {
			// Check leading spaces
			if !strings.HasPrefix(d.Content, "  on") {
				t.Errorf("expected indentation preserved for 'on reproduce...', got %q", d.Content)
			}
			foundOnLine = true
			break
		}
	}
	if !foundOnLine {
		t.Error("expected to find 'on reproduce_long_error.tf' line")
	}
}
