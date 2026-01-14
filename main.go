package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ResourceChange struct {
	Address    string
	Action     string
	Attributes []string
	Expanded   bool
}

type Model struct {
	resources    []ResourceChange
	cursor       int
	height       int
	offset       int
	ready        bool
}

var (
	createStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))  // Green
	updateStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))  // Yellow
	destroyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))  // Red
	replaceStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))  // Magenta
	importStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))  // Cyan
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // Dim gray
	selectedStyle = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("236"))
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
)

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.resources)-1 {
				m.cursor++
			}
		case "enter", " ":
			if m.cursor < len(m.resources) {
				m.resources[m.cursor].Expanded = !m.resources[m.cursor].Expanded
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	var lines []string
	header := headerStyle.Render("Terraform Plan Viewer") + dimStyle.Render(" (↑/↓: navigate, Enter: expand, q: quit)")
	lines = append(lines, header, "")

	// Track which line the cursor is on for scrolling
	cursorLine := 0
	currentLine := 0

	for i, rc := range m.resources {
		symbol := getSymbol(rc.Action)
		style := getStyleForAction(rc.Action)

		// Build the resource line
		prefix := "  "
		if i == m.cursor {
			prefix = "► "
			cursorLine = currentLine
		}

		resourceLine := fmt.Sprintf("%s%s %s", prefix, symbol, rc.Address)
		if i == m.cursor {
			resourceLine = selectedStyle.Render(resourceLine)
		} else {
			resourceLine = style.Render(resourceLine)
		}
		lines = append(lines, resourceLine)
		currentLine++

		// Add expanded attributes
		if rc.Expanded && len(rc.Attributes) > 0 {
			for _, attr := range rc.Attributes {
				styledAttr := styleAttribute(attr)
				lines = append(lines, "    "+styledAttr)
				currentLine++
			}
		}
	}

	// Calculate visible area (reserve 2 lines for header)
	visibleHeight := m.height - 3
	if visibleHeight < 5 {
		visibleHeight = 5
	}

	// Adjust offset to keep cursor visible
	if cursorLine < m.offset {
		m.offset = cursorLine
	} else if cursorLine >= m.offset+visibleHeight-2 {
		m.offset = cursorLine - visibleHeight + 3
	}

	// Clamp offset
	if m.offset < 0 {
		m.offset = 0
	}
	maxOffset := len(lines) - visibleHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}

	// Slice visible lines (header is always visible)
	startLine := 2 + m.offset // Skip header lines
	endLine := startLine + visibleHeight
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > len(lines) {
		startLine = len(lines)
	}

	// Build output with header + visible content
	var output strings.Builder
	output.WriteString(lines[0] + "\n") // Header
	output.WriteString(lines[1] + "\n") // Empty line

	for i := startLine; i < endLine; i++ {
		output.WriteString(lines[i] + "\n")
	}

	// Add scroll indicators
	if m.offset > 0 {
		output.WriteString(dimStyle.Render("  ↑ more above\n"))
	}
	if endLine < len(lines) {
		output.WriteString(dimStyle.Render("  ↓ more below\n"))
	}

	// Footer with summary
	summary := fmt.Sprintf("\n%s", getSummary(m.resources))
	output.WriteString(dimStyle.Render(summary))

	return output.String()
}

func styleAttribute(attr string) string {
	trimmed := strings.TrimSpace(attr)

	// Check for "unchanged" comments
	if strings.Contains(trimmed, "unchanged") {
		return dimStyle.Render(attr)
	}

	// Style based on prefix
	if strings.HasPrefix(trimmed, "+") {
		return createStyle.Render(attr)
	} else if strings.HasPrefix(trimmed, "-") {
		return destroyStyle.Render(attr)
	} else if strings.HasPrefix(trimmed, "~") {
		return updateStyle.Render(attr)
	} else if strings.HasPrefix(trimmed, "#") {
		return dimStyle.Render(attr)
	}

	// Default: unchanged attributes shown dimmed
	return dimStyle.Render(attr)
}

func getSummary(resources []ResourceChange) string {
	counts := make(map[string]int)
	for _, r := range resources {
		counts[r.Action]++
	}

	var parts []string
	if c := counts["create"]; c > 0 {
		parts = append(parts, createStyle.Render(fmt.Sprintf("+%d create", c)))
	}
	if c := counts["update"]; c > 0 {
		parts = append(parts, updateStyle.Render(fmt.Sprintf("~%d update", c)))
	}
	if c := counts["destroy"]; c > 0 {
		parts = append(parts, destroyStyle.Render(fmt.Sprintf("-%d destroy", c)))
	}
	if c := counts["replace"]; c > 0 {
		parts = append(parts, replaceStyle.Render(fmt.Sprintf("±%d replace", c)))
	}
	if c := counts["import"]; c > 0 {
		parts = append(parts, importStyle.Render(fmt.Sprintf("←%d import", c)))
	}

	if len(parts) == 0 {
		return "No changes"
	}
	return strings.Join(parts, "  ")
}

func getSymbol(action string) string {
	switch action {
	case "create":
		return "+"
	case "destroy":
		return "-"
	case "update":
		return "~"
	case "replace":
		return "±"
	case "import":
		return "←"
	default:
		return "·"
	}
}

func getStyleForAction(action string) lipgloss.Style {
	switch action {
	case "create":
		return createStyle
	case "update":
		return updateStyle
	case "destroy":
		return destroyStyle
	case "replace":
		return replaceStyle
	case "import":
		return importStyle
	default:
		return lipgloss.NewStyle()
	}
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func parsePlan(reader io.Reader) []ResourceChange {
	scanner := bufio.NewScanner(reader)
	resources := make([]ResourceChange, 0)

	// Pattern: # resource_address will be created/destroyed/etc
	headerPattern := regexp.MustCompile(`^\s*# (.+?) (will be created|will be destroyed|will be updated in-place|must be replaced|will be imported)`)

	var currentResource *ResourceChange
	inResource := false
	bracketDepth := 0

	for scanner.Scan() {
		line := stripANSI(scanner.Text())

		// Check for resource header comment
		if match := headerPattern.FindStringSubmatch(line); match != nil {
			// Save previous resource if exists
			if currentResource != nil {
				resources = append(resources, *currentResource)
			}

			address := match[1]
			actionText := match[2]

			var action string
			switch actionText {
			case "will be created":
				action = "create"
			case "will be updated in-place":
				action = "update"
			case "will be destroyed":
				action = "destroy"
			case "must be replaced":
				action = "replace"
			case "will be imported":
				action = "import"
			}

			currentResource = &ResourceChange{
				Address:    address,
				Action:     action,
				Attributes: make([]string, 0),
			}
			continue
		}

		// Check for resource block start (e.g., "-/+ resource", "+ resource")
		if currentResource != nil && strings.Contains(line, " resource \"") {
			inResource = true
			// Count brackets on this line (typically has opening {)
			bracketDepth = strings.Count(line, "{") - strings.Count(line, "}")
			continue
		}

		// Track bracket depth when inside resource
		if inResource {
			bracketDepth += strings.Count(line, "{")
			bracketDepth -= strings.Count(line, "}")

			// Capture attributes (skip the opening brace line)
			if currentResource != nil && !strings.Contains(line, " resource \"") {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" && trimmed != "{" && trimmed != "}" {
					currentResource.Attributes = append(currentResource.Attributes, trimmed)
				}
			}

			// End of resource block
			if bracketDepth == 0 && strings.Contains(line, "}") {
				if currentResource != nil {
					resources = append(resources, *currentResource)
					currentResource = nil
				}
				inResource = false
			}
		}
	}

	// Add last resource if exists
	if currentResource != nil {
		resources = append(resources, *currentResource)
	}

	return resources
}

func main() {
	resources := parsePlan(os.Stdin)

	if len(resources) == 0 {
		fmt.Fprintln(os.Stderr, "No resource changes found in plan")
		os.Exit(1)
	}

	m := Model{resources: resources}
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
