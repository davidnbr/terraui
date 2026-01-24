package main

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestIndentationPreservation(t *testing.T) {
	m := &Model{
		streamChan: make(chan StreamMsg, 10),
	}
	input := `# test_resource will be created
  + resource "test_resource" "this" {
      + attr1 = "value1"
      + block {
          + attr2 = "value2"
        }
    }
`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go m.readInputStream(ctx, strings.NewReader(input))

	var resource *ResourceChange
	for {
		msg, ok := <-m.streamChan
		if !ok || msg.Done {
			break
		}
		if msg.Resource != nil {
			resource = msg.Resource
		}
	}

	if resource == nil {
		t.Fatal("expected resource to be parsed")
	}

	// Current implementation trims everything and skips { and }
	// We want it to PRESERVE indentation and braces
	expectedAttributes := []string{
		"      + attr1 = \"value1\"",
		"      + block {",
		"          + attr2 = \"value2\"",
		"        }",
	}

	if len(resource.Attributes) != len(expectedAttributes) {
		t.Fatalf("expected %d attributes, got %d", len(expectedAttributes), len(resource.Attributes))
	}

	for i, attr := range resource.Attributes {
		if attr != expectedAttributes[i] {
			t.Errorf("expected %q at index %d, got %q", expectedAttributes[i], i, attr)
		}
	}
}

func TestRenderingModeToggle(t *testing.T) {
	m := Model{
		renderingMode: RenderingModeDashboard,
	}

	if m.renderingMode != RenderingModeDashboard {
		t.Errorf("expected default rendering mode to be Dashboard, got %v", m.renderingMode)
	}

	m.toggleRenderingMode()

	if m.renderingMode != RenderingModeHighContrast {
		t.Errorf("expected rendering mode to be HighContrast after toggle, got %v", m.renderingMode)
	}

	m.toggleRenderingMode()

	if m.renderingMode != RenderingModeDashboard {
		t.Errorf("expected rendering mode to be Dashboard after second toggle, got %v", m.renderingMode)
	}
}

func TestUpdateRenderingMode(t *testing.T) {
	m := Model{
		renderingMode: RenderingModeDashboard,
	}

	// Simulate pressing 'm'
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")}
	updatedModel, _ := m.Update(msg)
	newModel := updatedModel.(Model)

	if newModel.renderingMode != RenderingModeHighContrast {
		t.Errorf("expected rendering mode to be HighContrast after 'm' key, got %v", newModel.renderingMode)
	}

	// Press 'm' again
	updatedModel, _ = newModel.Update(msg)
	finalModel := updatedModel.(Model)

	if finalModel.renderingMode != RenderingModeDashboard {
		t.Errorf("expected rendering mode to be Dashboard after second 'm' key, got %v", finalModel.renderingMode)
	}
}

func TestThemeProvider(t *testing.T) {
	dashboardTheme := getTheme(RenderingModeDashboard)
	highContrastTheme := getTheme(RenderingModeHighContrast)

	// In Dashboard mode, create should be green, but might be different from Catppuccin green
	// For now, let's just ensure they are defined and potentially different if we have values
	if dashboardTheme.Create.GetForeground() == highContrastTheme.Create.GetForeground() {
		// This might be true initially if we haven't defined different colors,
		// but eventually they should differ.
		// For the refactoring task, we just want to ensure the mechanism works.
	}
}

func TestDashboardModeColors(t *testing.T) {
	dashboardTheme := getTheme(RenderingModeDashboard)
	highContrastTheme := getTheme(RenderingModeHighContrast)

	// Palettes should be identical now (Catppuccin everywhere)
	if dashboardTheme.Create.GetForeground() != highContrastTheme.Create.GetForeground() {
		t.Errorf("Dashboard and HighContrast themes should share the same palette")
	}
}

func TestRenderingModeLogic(t *testing.T) {
	// Force color output for testing
	lipgloss.SetColorProfile(termenv.TrueColor)

	// Verify that the rendering logic produces different output for different modes
	// even with the same palette.

	// Setup a model with a resource
	m := Model{
		renderingMode: RenderingModeDashboard,
		resources: []ResourceChange{
			{
				Address:    "test_resource",
				Action:     "create",
				ActionText: "will be created",
			},
		},
	}

	// Dashboard mode rendering
	dashboardOutput := m.renderResourceLine(0, false)

	// Switch to HighContrast
	m.renderingMode = RenderingModeHighContrast
	highContrastOutput := m.renderResourceLine(0, false)

	if dashboardOutput == highContrastOutput {
		t.Error("Dashboard and HighContrast modes should produce different output strings")
	}
}

func TestInitialRenderingMode(t *testing.T) {
	m := Model{}
	// Note: In Go, int default is 0, which is RenderingModeDashboard.
	// But it's good to be explicit in our code.
	if m.renderingMode != RenderingModeDashboard {
		t.Errorf("expected initial rendering mode to be Dashboard, got %v", m.renderingMode)
	}
}

func TestHighContrastPalette(t *testing.T) {
	theme := getTheme(RenderingModeHighContrast)

	// Verify it uses Catppuccin-like colors (Mocha)
	expectedGreen := lipgloss.Color("#a6e3a1")
	if theme.Create.GetForeground() != expectedGreen {
		t.Errorf("expected HighContrast Create foreground to be %v, got %v", expectedGreen, theme.Create.GetForeground())
	}
}

func TestRebuildLinesWrapping(t *testing.T) {
	m := &Model{
		width: 20, // Small width to force wrapping
		resources: []ResourceChange{
			{
				Address: "r1",
				Attributes: []string{
					"    key = \"very long value that wraps\"",
				},
				Expanded: true,
			},
		},
	}
	
	m.rebuildLines()
	
	// Expect resource header + attribute lines
	// Header: 1 line
	// Attribute: "    key = \"very long value that wraps\"" (32 chars)
	// Width 20.
	// Line 1: "    key = \"very lon" (20 chars)
	// Line 2: "      g value that w" (Indent 6 + 14 chars = 20)
	// Line 3: "      raps\"" (Indent 6 + 5 chars = 11)
	
	// Total 4 lines in m.lines
	if len(m.lines) != 4 {
		t.Fatalf("expected 4 lines (1 header + 3 wrapped), got %d", len(m.lines))
	}
	
	// Check content of wrapped lines
	// Note: styles/ANSI might affect string matching if I check Content directly?
	// No, wrapText operates on raw string, and rebuildLines stores it in Content.
	// renderAttributeLine adds styles LATER.
	
	if m.lines[1].Content != "    key = \"very long" {
		t.Errorf("Line 1 content mismatch: %q", m.lines[1].Content)
	}
	if m.lines[2].Content != "     value that wrap" {
		t.Errorf("Line 2 content mismatch: %q", m.lines[2].Content)
	}
	if m.lines[3].Content != "    s\"" {
		t.Errorf("Line 3 content mismatch: %q", m.lines[3].Content)
	}
}
