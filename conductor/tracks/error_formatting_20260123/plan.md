# Implementation Plan - Correct Error Formatting (Bolds, Underlines, and Colors)

This plan follows the project's TDD-based workflow.

## Phase 1: Parser Enhancement and Formatting Detection
Update the diagnostic parser to identify structural and semantic formatting triggers.

- [~] Task: Update `Diagnostic` struct and `parseDiagnosticBlock`
    - [ ] Write tests for detecting "Error:" and "Warning:" in diagnostic lines
    - [ ] Update `Diagnostic` struct to store semantic flags (e.g., `IsHeader`, `IsMarkerLine`)
- [ ] Task: Implement pattern detection for code location markers (`^`, `~`)
    - [ ] Write tests for identifying markers and code underlines in diagnostic details
    - [ ] Implement detection logic in `parseDiagnosticBlock` or `readInputStream`
- [ ] Task: Conductor - User Manual Verification 'Phase 1: Parser Enhancement and Formatting Detection' (Protocol in workflow.md)

## Phase 2: Thematic Rendering Engine
Enhance the rendering logic to apply bold, underline, and color styles based on the theme.

- [ ] Task: Update `Theme` struct and styles for rich formatting
    - [ ] Write tests ensuring `Theme` provider returns bold/underline styles for errors
    - [ ] Add `BoldError`, `BoldWarning`, and `Underline` styles to the `Theme` struct
- [ ] Task: Implement rich rendering for Diagnostic Headers
    - [ ] Write tests for bolded and colored "Error:" summaries
    - [ ] Update `renderDiagnosticLine` to use the new rich styles
- [ ] Task: Implement contextual styling for Diagnostic Details
    - [ ] Write tests for guide line (`│`) and marker (`^`) styling
    - [ ] Update `renderDiagnosticDetailLine` to apply styles based on content patterns (e.g., bolding file paths)
- [ ] Task: Conductor - User Manual Verification 'Phase 2: Thematic Rendering Engine' (Protocol in workflow.md)

## Phase 3: Integration and Refinement [checkpoint: 1473ad1]
Verify the formatting with real-world examples and ensure cross-mode consistency.

- [x] Task: Verify with `reproduce_long_error.tf` (1473ad1)
    - [x] Run the reproduction script and verify that underlines and bolds match the expected output
- [x] Task: Cross-mode validation (1473ad1)
    - [x] Ensure formatting looks correct in both Dashboard and High-Contrast modes
- [x] Task: Conductor - User Manual Verification 'Phase 3: Integration and Refinement' (Protocol in workflow.md)
