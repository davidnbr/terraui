# Implementation Plan - Dual Plan Rendering Modes

This plan follows the project's TDD-based workflow.

## Phase 1: Infrastructure and State Management [checkpoint: f41d3ef]
Define the state and the toggle mechanism for the rendering modes.

- [x] Task: Define rendering mode types and application state (cff8169)
    - [x] Write tests for state initialization and mode toggling logic
    - [x] Implement `RenderingMode` type and update the main model state
- [x] Task: Implement toggle keyboard shortcut (582af53)
    - [x] Write tests for handling the `m` key (or selected key) to toggle mode
    - [x] Update the `Update` function in the Bubble Tea model
- [x] Task: Conductor - User Manual Verification 'Phase 1: Infrastructure and State Management' (Protocol in workflow.md)

## Phase 2: Dashboard Mode Implementation
Implement the standard Terraform color scheme and set it as default.

- [x] Task: Refactor styling logic to support multiple palettes (e298fb3)
    - [x] Write tests for a style provider that returns different styles based on the active mode
    - [x] Implement a style provider or theme manager
- [ ] Task: Implement Dashboard (Standard Terraform) palette
    - [ ] Write tests to verify correct color application for all resource change types in Dashboard mode
    - [ ] Define the standard ANSI-like color palette for Dashboard mode
- [ ] Task: Set Dashboard mode as the default starting mode
    - [ ] Write tests to verify the initial state is Dashboard mode
    - [ ] Update initialization logic
- [ ] Task: Conductor - User Manual Verification 'Phase 2: Dashboard Mode Implementation' (Protocol in workflow.md)

## Phase 3: High-Contrast Mode Preservation and Final Polish
Ensure the existing mode is preserved and the UI feels cohesive.

- [ ] Task: Integrate High-Contrast palette into the new styling system
    - [ ] Write tests to verify Catppuccin colors are applied when High-Contrast mode is active
    - [ ] Map existing styling logic to the new theme manager
- [ ] Task: Update Top Hint Bar to reflect the new toggle
    - [ ] Write tests to verify the Hint Bar displays the toggle key
    - [ ] Update the UI rendering of the header/hint bar
- [ ] Task: Conductor - User Manual Verification 'Phase 3: High-Contrast Mode Preservation and Final Polish' (Protocol in workflow.md)
