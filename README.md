# terraui

A beautiful, interactive terminal UI for reviewing Terraform plans. Inspired by Terraform Cloud's visual interface, but right in your terminal.

## Why terraui?

Reviewing Terraform plans in the terminal can be overwhelming, especially with large infrastructure changes. `terraui` transforms the wall of text into an organized, collapsible, color-coded view that makes it easy to:

- **Quickly identify what's changing** - Color-coded actions (create, update, destroy, replace, import)
- **Focus on what matters** - Collapse unchanged resources, expand only what you need
- **Navigate large plans** - Scroll through hundreds of changes with keyboard or mouse
- **Review with confidence** - See exactly what will happen before you apply

## Features

- **Collapsible resource blocks** - Expand/collapse individual resources or all at once
- **Syntax highlighting** - Green for additions, red for deletions, yellow for changes
- **Line-by-line navigation** - Arrow through every attribute, not just resource headers
- **Mouse support** - Click to select, scroll wheel to navigate
- **Vim-style keybindings** - `j/k`, `Ctrl+u/d`, `g/G` for power users
- **Works with HCP Terraform** - Handles ANSI escape codes from remote plan output
- **Scroll indicators** - Always know how much content is above/below
- **Summary footer** - Quick count of creates, updates, destroys, etc.

## Installation

### From source

```bash
# Clone the repository
git clone https://github.com/yourusername/terraui.git
cd terraui

# Build
go build -o terraui .

# Optionally, move to your PATH
sudo mv terraui /usr/local/bin/
```

### Requirements

- Go 1.21 or later

## Usage

Pipe your Terraform plan output to `terraui`:

```bash
# Standard usage
terraform plan | terraui

# With HCP Terraform / Terraform Cloud
terraform plan 2>&1 | terraui

# Save plan and review
terraform plan -out=plan.tfplan
terraform show plan.tfplan | terraui
```

## Controls

### Keyboard

| Key | Action |
|-----|--------|
| `↑` / `k` | Move cursor up |
| `↓` / `j` | Move cursor down |
| `Enter` / `Space` | Expand/collapse resource (on header lines) |
| `Ctrl+u` | Scroll up half page |
| `Ctrl+d` | Scroll down half page |
| `PgUp` / `PgDn` | Scroll up/down half page |
| `g` / `Home` | Go to top |
| `G` / `End` | Go to bottom |
| `e` | Expand all resources |
| `c` | Collapse all resources |
| `q` / `Ctrl+c` | Quit |

### Mouse

| Action | Behavior |
|--------|----------|
| Scroll wheel up | Move cursor up (3 lines) |
| Scroll wheel down | Move cursor down (3 lines) |
| Left click | Select line |
| Click selected resource | Expand/collapse it |

## Color Coding

| Symbol | Color | Meaning |
|--------|-------|---------|
| `+` | Green | Resource will be created |
| `-` | Red | Resource will be destroyed |
| `~` | Yellow | Resource will be updated in-place |
| `±` | Magenta | Resource must be replaced (destroy + create) |
| `←` | Cyan | Resource will be imported |

Attributes within resources are also color-coded:
- **Green** - Attribute being added (`+ attribute = value`)
- **Red** - Attribute being removed (`- attribute = value`)
- **Yellow** - Attribute being changed (`~ attribute = old -> new`)
- **Dim gray** - Unchanged attributes (shown for context)

## Example

```
Terraform Plan Viewer ↑↓:navigate  ^u/^d:half-page  Enter:expand  e/c:expand/collapse all  q:quit

► ▾ ~ azurerm_postgresql_flexible_server.main
    ~ administrator_login = (sensitive value)
      id                  = "/subscriptions/.../flexibleServers/psql-staging"
      name                = "psql-staging"
    ~ tags = {
        "Environment" = "staging"
      + "Terraform"   = "true"
      }
    # (19 unchanged attributes hidden)
  ▸ + aws_s3_bucket.new_bucket
  ▸ - aws_instance.old_server
  ▸ ← azurerm_storage_container.media

  ↓ 12 more lines below

~1 update  +1 create  -1 destroy  ←1 import
```

## Supported Terraform Versions

`terraui` works with:
- Terraform CLI (all recent versions)
- OpenTofu
- HCP Terraform / Terraform Cloud (remote plan output)
- Terraform Enterprise

## How It Works

`terraui` parses the human-readable Terraform plan output by:
1. Detecting resource change headers (`# resource.name will be created/updated/destroyed`)
2. Capturing the resource block and all its attributes
3. Stripping ANSI escape codes (for HCP Terraform compatibility)
4. Rendering an interactive TUI using [Bubble Tea](https://github.com/charmbracelet/bubbletea)

## Contributing

Contributions are welcome! Feel free to:
- Report bugs
- Suggest features
- Submit pull requests

## License

MIT License - see [LICENSE](LICENSE) for details.

## Acknowledgments

Built with:
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - TUI framework
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) - Style definitions

Inspired by the Terraform Cloud plan UI.
