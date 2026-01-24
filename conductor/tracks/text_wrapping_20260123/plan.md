# Implementation Plan - Implement Text Wrapping for Large Plan Outputs

This plan follows the project's TDD-based workflow.

## Phase 1: Logic Implementation [checkpoint: 71b80a4]
Implement the text wrapping logic with hanging indents.

- [x] Task: Create wrapping helper function (verified with tests) (71b80a4)
    - [x] Write tests for a `wrapText` function that takes text, width, and indent, and returns wrapped lines
    - [x] Implement `wrapText` using a word-wrapping library or custom logic that respects hanging indentation
- [x] Task: Integrate wrapping into `renderAttributeLine` and `renderResourceLine` (Integrated in `rebuildLines`) (71b80a4)
    - [x] Write tests ensuring long attribute values are split into multiple lines
    - [x] Update `renderAttributeLine` to calculate available width and apply wrapping (done in `rebuildLines`)
    - [x] Update `Model` to recalculate line wrapping on resize (`WindowSizeMsg`)
- [x] Task: Conductor - User Manual Verification 'Phase 1: Logic Implementation' (Protocol in workflow.md)

## Phase 2: UI Integration and Refinement [checkpoint: 71b80a4]
Ensure wrapping works seamlessly with the UI and resize events.

- [x] Task: Handle multi-line rendering in `View` (71b80a4)
    - [x] Update `View` loop to handle logical lines that now span multiple display lines
    - [x] Ensure scrolling logic (`visibleHeight`, `offset`) accounts for variable-height lines (or flattened wrapped lines)
- [x] Task: Verify interaction with braces and structure (71b80a4)
    - [x] Write tests for wrapped lines inside nested blocks to ensure structural alignment isn't broken
- [x] Task: Conductor - User Manual Verification 'Phase 2: UI Integration and Refinement' (Protocol in workflow.md)
