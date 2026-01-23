# Initial Concept
`terraui` is a beautiful, interactive terminal UI for reviewing Terraform plans and watching applies. It transforms the often-overwhelming "wall of text" from standard Terraform output into an organized, collapsible, and color-coded view, making infrastructure changes easier to identify and manage.

# Target Users
- **DevOps & Platform Engineers:** Professionals managing complex infrastructure who need to review large, multi-resource plans efficiently.
- **SREs:** Site Reliability Engineers performing production applies who need to watch real-time logs and verify changes with high confidence.
- **Infrastructure Developers:** Developers using Terraform/OpenTofu locally who want a more readable and interactive alternative to raw plan outputs.

# Primary Goals
- **Clarity & Readability:** Transform standard Terraform output into an intuitive, structured view where changes are immediately obvious through color-coding and clear layouts.
- **Operational Confidence:** Provide a safe and interactive environment for reviewing plans and approving applies directly within the TUI.
- **Efficiency at Scale:** Enable users to navigate and understand massive infrastructure plans (hundreds of resources) using collapsible blocks and efficient navigation.
- **Data Understandability:** Ensure that even in information-dense scenarios, the data remains functional and easy to interpret.

# Core Features
- **Interactive Plan Review:** Collapsible resource blocks with color-coded change types (create, update, destroy, replace, import) and attribute-level highlighting.
- **Interactive Apply Mode:** A PTY-wrapped environment to stream logs in real-time and handle interactive "yes/no" confirmation prompts.
- **Seamless Navigation:** Support for Vim-style keybindings, mouse interactions (scrolling, clicking), and quick toggling between Plan and Log views.

# Visual Aesthetic & User Experience
- **Modern & Polished:** A beautiful interface utilizing the Catppuccin theme to provide a premium infrastructure management experience.
- **Minimalist & Transparent:** A design that stays close to the native Terraform feel, avoiding unnecessary UI "chrome" while providing essential interactivity.
- **Information-Dense yet Functional:** A layout that maximizes visible information while maintaining strict clarity and high-contrast visuals for operational efficiency.
