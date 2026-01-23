# Visual Identity & Design
- **Theme:** Strict adherence to the **Catppuccin Mocha** palette. This ensures a modern, high-contrast, and beautiful interface.
    - **Colors:**
        - Green (`#a6e3a1`): Resource creation / Adding attributes.
        - Red (`#f38ba8`): Resource destruction / Removing attributes.
        - Yellow (`#f9e2af`): Resource updates / Modifying attributes.
        - Mauve (`#cba6f7`): Resource replacement.
        - Sky (`#89dceb`): Resource import.
        - Dim Gray / Surface: Unchanged context and UI borders.
- **Layout Philosophy:**
    - **Progressive Disclosure:** Large resource attribute lists should be collapsed by default to prevent cognitive overload and allow for quick scanning of resource names.
    - **Contextual Highlighting:** Use high-contrast colors for modifications while dimming unchanged attributes to guide the user's eye to significant changes.
- **Navigation Aids:**
    - **Top Hint Bar:** A persistent but unobtrusive header bar displaying context-sensitive keyboard shortcuts (e.g., `j/k: Navigate`, `Space: Expand`, `q: Quit`).

# Communication & Prose
- **Tone:** Direct and Technical.
- **Style:**
    - **Native Error Fidelity:** Errors must show the exact text that Terraform natively outputs, preserving all original formatting, including highlights, underlines, and ANSI escape codes.
    - Status updates must be concise and accurate, focusing on providing the "what" and "where" immediately.
    - Avoid conversational filler; value the user's time and focus on operational precision.
- **Information Density:** Prioritize functional clarity. Even when displaying dense infrastructure data, the layout should remain structured and understandable.
