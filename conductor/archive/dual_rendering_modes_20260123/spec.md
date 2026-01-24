# Specification - Dual Plan Rendering Modes

## Overview
Currently, `terraui` provides a high-contrast, highlighted view for Terraform plans. This track introduces a second rendering mode that mimics the standard Terraform CLI color output (Dashboard mode). This "Dashboard" mode will become the default view, with the ability for users to toggle back to the high-contrast view.

## Functional Requirements
- **Default Dashboard Mode:** Upon starting `terraui`, the plan should be rendered using standard Terraform colors (Green for `+`, Red for `-`, Yellow for `~`, etc.).
- **High-Contrast Mode Toggle:** Users can switch between the Dashboard mode and the existing High-Contrast mode using a keyboard shortcut.
- **Persistent State (Session):** The selected view mode should persist while the application is running, even when switching between Plan and Log views.
- **Visual Fidelity:** The Dashboard mode must accurately replicate the standard Terraform CLI's color application to resource headers and attribute changes.

## Non-Functional Requirements
- **Performance:** Toggling between modes should be instantaneous without re-parsing the entire plan.
- **Code Quality:** Adhere to the Go style guide and maintain >80% test coverage for rendering logic.

## Acceptance Criteria
- [ ] Application starts in Dashboard mode by default.
- [ ] Pressing `m` (proposed toggle key) switches between Dashboard and High-Contrast modes.
- [ ] Dashboard mode colors match standard Terraform CLI output.
- [ ] High-Contrast mode maintains the Catppuccin-based styling previously implemented.
- [ ] Both modes support collapsible resource blocks.

## Out of Scope
- Persisting the mode selection across different application runs (configuration file).
- Customizing the colors used in either mode via external configuration.
