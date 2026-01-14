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
	resources []ResourceChange
	cursor    int
}

var (
	createStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	updateStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	destroyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	replaceStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	importStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("240"))
)

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
	s := "Terraform Plan Viewer (↑/↓: navigate, Enter/Space: expand, q: quit)\n\n"

	for i, rc := range m.resources {
		symbol := getSymbol(rc.Action)
		style := getStyleForAction(rc.Action)

		line := fmt.Sprintf("%s %s", symbol, rc.Address)
		if i == m.cursor {
			line = selectedStyle.Render("► " + line)
		} else {
			line = "  " + line
		}
		s += style.Render(line) + "\n"

		if rc.Expanded && len(rc.Attributes) > 0 {
			for _, attr := range rc.Attributes {
				s += "    " + attr + "\n"
			}
		}
	}

	return s
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

func parsePlan(reader io.Reader) []ResourceChange {
	scanner := bufio.NewScanner(reader)
	resources := make([]ResourceChange, 0)

	// Pattern: # resource_address will be created/destroyed/etc
	headerPattern := regexp.MustCompile(`^\s*# (.+?) (will be created|will be destroyed|will be updated in-place|must be replaced|will be imported)`)

	var currentResource *ResourceChange
	inResource := false
	bracketDepth := 0

	for scanner.Scan() {
		line := scanner.Text()

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
	p := tea.NewProgram(m)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
