package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
