package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// TestAllContentPreserved verifies that meaningful input text appears in output
// Note: Structural characters (╷, │, ╵) are stripped during parsing, but content is preserved
func TestAllContentPreserved(t *testing.T) {
	testCases := []struct {
		name            string
		input           string
		expectedContent string // Content that should be preserved (not structural chars)
	}{
		{"empty", "", ""},
		{"single_line", "Hello world\n", "Hello world"},
		{"multiple_lines", "Line 1\nLine 2\nLine 3\n", "Line 1"},
		{"diagnostic_block", "╷\n│ Error: test message\n╵\n", "test message"},
		{"long_line", strings.Repeat("A", 10000) + "\n", strings.Repeat("A", 100)},
		{"many_lines", strings.Repeat("Line\n", 1000), "Line"},
		{"mixed_content", "Log line\n╷\n│ Error: msg\n╵\nAnother log\n", "msg"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{streamChan: make(chan StreamMsg, 100)}
			diagnostics, logs, _, _ := collectStreamMsgs(m, tc.input)

			var output strings.Builder
			for _, d := range diagnostics {
				output.WriteString(d.Summary)
				for _, detail := range d.Detail {
					output.WriteString(detail.Content)
				}
			}
			for _, l := range logs {
				output.WriteString(l)
			}

			outputStr := output.String()

			// Check that expected content is preserved
			if tc.expectedContent != "" && !strings.Contains(outputStr, tc.expectedContent) {
				t.Errorf("Lost content: expected %q in output\nGot:\n%s", tc.expectedContent, outputStr)
			}
		})
	}
}

// TestNoPanicOnRandomInput verifies parser handles random bytes without crashing
func TestNoPanicOnRandomInput(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for i := 0; i < 100; i++ {
		length := rng.Intn(10000)
		data := make([]byte, length)
		for j := range data {
			data[j] = byte(rng.Intn(256))
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Panic on random input %d: %v", i, r)
				}
			}()

			m := &Model{streamChan: make(chan StreamMsg, 100)}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			go m.readInputStream(ctx, bytes.NewReader(data))

			for {
				select {
				case _, ok := <-m.streamChan:
					if !ok {
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
}

// TestNoPanicOnMalformedBlocks verifies parser handles broken diagnostic blocks
func TestNoPanicOnMalformedBlocks(t *testing.T) {
	malformedInputs := []string{
		"╷",
		"╵",
		"╷╵",
		"╷\n│\n╵",
		"╷\n│ Error:\n╵",
		"╷\n│\n│\n│\n╵",
		"╷" + strings.Repeat("\n│", 1000) + "\n╵",
		"╷\n│ " + strings.Repeat("A", 100000) + "\n╵",
	}

	for i, input := range malformedInputs {
		t.Run(fmt.Sprintf("malformed_%d", i), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Panic on malformed input %d: %v", i, r)
				}
			}()

			m := &Model{streamChan: make(chan StreamMsg, 100)}
			collectStreamMsgs(m, input)
		})
	}
}

// TestExitCodeDeterminesErrorState verifies exit code drives hasError field
func TestExitCodeDeterminesErrorState(t *testing.T) {
	testCases := []struct {
		exitCode    int
		expectError bool
	}{
		{0, false},
		{1, true},
		{2, true},
		{127, true},
		{255, true},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("exit_code_%d", tc.exitCode), func(t *testing.T) {
			m := Model{}
			msg := exitCodeMsg{exitCode: tc.exitCode, hasError: tc.exitCode != 0}

			// Call the actual logic
			updatedM, _ := m.Update(msg)
			finalM := updatedM.(Model)

			if finalM.hasError != tc.expectError {
				t.Errorf("exitCode=%d: expected hasError=%v, got %v",
					tc.exitCode, tc.expectError, finalM.hasError)
			}
		})
	}
}

// TestAutoSwitchToLogViewOnError verifies view switches on error
func TestAutoSwitchToLogViewOnError(t *testing.T) {
	m := Model{
		showLogs: false,
		exitCode: 0,
		hasError: false,
	}

	msg := exitCodeMsg{exitCode: 1, hasError: true}

	// Call the actual logic under test
	updatedM, _ := m.Update(msg)
	finalM := updatedM.(Model)

	if !finalM.showLogs {
		t.Error("Expected view to auto-switch to LOG on error")
	}
	if !finalM.hasError {
		t.Error("Expected hasError to be true")
	}
}

// TestLogViewOrder verifies logs appear before diagnostics in LOG view
func TestLogViewOrder(t *testing.T) {
	m := &Model{
		showLogs: true,
		width:    80,
		logs: []string{
			"Initializing the backend...",
			"Refreshing state...",
			"Planning...",
		},
		diagnostics: []Diagnostic{
			{
				Severity: "error",
				Summary:  "Error: something failed",
				Detail:   []DiagnosticLine{{Content: "detail line"}},
				Expanded: true,
			},
		},
	}

	m.rebuildLines()

	firstDiagIdx := -1
	for i, line := range m.lines {
		if line.Type == LineTypeDiagnostic {
			firstDiagIdx = i
			break
		}
	}

	lastLogIdx := -1
	for i, line := range m.lines {
		if line.Type == LineTypeLog {
			lastLogIdx = i
		}
	}

	if firstDiagIdx == -1 {
		t.Fatal("No diagnostic lines found")
	}
	if lastLogIdx == -1 {
		t.Fatal("No log lines found")
	}

	if lastLogIdx > firstDiagIdx {
		t.Error("Logs should appear BEFORE diagnostics in LOG view")
	}
}

// TestPlanViewHasNoDiagnostics verifies PLAN view excludes diagnostics
func TestPlanViewHasNoDiagnostics(t *testing.T) {
	m := &Model{
		showLogs: false,
		width:    80,
		resources: []ResourceChange{
			{Address: "test_resource", Action: "create"},
		},
		diagnostics: []Diagnostic{
			{
				Severity: "error",
				Summary:  "Error: should not appear in PLAN view",
			},
		},
	}

	m.rebuildLines()

	hasResources := false
	for _, line := range m.lines {
		if line.Type == LineTypeResource {
			hasResources = true
		}
	}

	if !hasResources {
		t.Error("PLAN view should have resource lines")
	}

	for _, line := range m.lines {
		if line.Type == LineTypeDiagnostic || line.Type == LineTypeDiagnosticDetail {
			t.Error("PLAN view should NOT have diagnostic lines")
		}
	}
}
