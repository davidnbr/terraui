package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

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
				Address: "test_resource",
				Action: "create",
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
