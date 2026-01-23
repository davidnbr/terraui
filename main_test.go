package main

import (
	"testing"
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
