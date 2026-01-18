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

type Diagnostic struct {
	Severity string   // "error" or "warning"
	Summary  string
	Detail   []string
	Expanded bool
}

// Line represents a single display line
type Line struct {
	Type        string // "resource", "attribute", "diagnostic", "diagnostic_detail"
	ResourceIdx int    // Which resource this belongs to (-1 if diagnostic)
	DiagIdx     int    // Which diagnostic this belongs to (-1 if resource)
	AttrIdx     int    // -1 for headers, >=0 for attribute/detail
	Content     string // Raw content
}

type Model struct {
	resources   []ResourceChange
	diagnostics []Diagnostic
	lines       []Line // All visible lines
	cursor      int    // Current line index
	height      int
	offset      int
	ready       bool
}

var (
	createStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))   // Green
	updateStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))   // Yellow
	destroyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))   // Red
	replaceStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))   // Magenta
	importStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))   // Cyan
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // Bright red
	warningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // Orange
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("236"))
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
)

func (m Model) Init() tea.Cmd {
	return nil
}

// Rebuild the lines slice based on current expand state
func (m *Model) rebuildLines() {
	m.lines = nil

	// Add diagnostics first (errors and warnings)
	for i, diag := range m.diagnostics {
		m.lines = append(m.lines, Line{Type: "diagnostic", DiagIdx: i, ResourceIdx: -1, AttrIdx: -1})

		if diag.Expanded {
			for j, detail := range diag.Detail {
				m.lines = append(m.lines, Line{Type: "diagnostic_detail", DiagIdx: i, ResourceIdx: -1, AttrIdx: j, Content: detail})
			}
		}
	}

	// Add resources
	for i, rc := range m.resources {
		m.lines = append(m.lines, Line{Type: "resource", ResourceIdx: i, DiagIdx: -1, AttrIdx: -1})

		if rc.Expanded {
			for j := range rc.Attributes {
				m.lines = append(m.lines, Line{Type: "attribute", ResourceIdx: i, DiagIdx: -1, AttrIdx: j, Content: rc.Attributes[j]})
			}
		}
	}
}

func (m *Model) clampCursor() {
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.lines) {
		m.cursor = len(m.lines) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) ensureCursorVisible() {
	visibleHeight := m.height - 6
	if visibleHeight < 5 {
		visibleHeight = 5
	}

	if m.cursor < m.offset {
		m.offset = m.cursor
	} else if m.cursor >= m.offset+visibleHeight {
		m.offset = m.cursor - visibleHeight + 1
	}

	m.clampOffset()
}

func (m *Model) clampOffset() {
	visibleHeight := m.height - 6
	if visibleHeight < 5 {
		visibleHeight = 5
	}

	if m.offset < 0 {
		m.offset = 0
	}
	maxOffset := len(m.lines) - visibleHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.ready = true
		m.rebuildLines()
		m.clampOffset()
		return m, nil

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.cursor -= 3
			m.clampCursor()
			m.ensureCursorVisible()
		case tea.MouseButtonWheelDown:
			m.cursor += 3
			m.clampCursor()
			m.ensureCursorVisible()
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionPress {
				// Calculate which line was clicked (accounting for header and scroll indicator)
				headerOffset := 2 // Header + empty line
				if m.offset > 0 {
					headerOffset = 3 // +1 for "more above" indicator
				}
				clickedLine := m.offset + msg.Y - headerOffset
				if msg.Y >= headerOffset && clickedLine >= 0 && clickedLine < len(m.lines) {
					if m.cursor == clickedLine {
						// Double-click behavior: if already selected, toggle expand
						line := m.lines[clickedLine]
						if line.Type == "resource" {
							m.resources[line.ResourceIdx].Expanded = !m.resources[line.ResourceIdx].Expanded
							m.rebuildLines()
							m.clampCursor()
							m.clampOffset()
						} else if line.Type == "diagnostic" {
							m.diagnostics[line.DiagIdx].Expanded = !m.diagnostics[line.DiagIdx].Expanded
							m.rebuildLines()
							m.clampCursor()
							m.clampOffset()
						}
					} else {
						m.cursor = clickedLine
					}
				}
			}
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.ensureCursorVisible()
			}

		case "down", "j":
			if m.cursor < len(m.lines)-1 {
				m.cursor++
				m.ensureCursorVisible()
			}

		case "enter", " ":
			// Toggle expand on header lines
			if m.cursor < len(m.lines) {
				line := m.lines[m.cursor]
				if line.Type == "resource" {
					m.resources[line.ResourceIdx].Expanded = !m.resources[line.ResourceIdx].Expanded
					m.rebuildLines()
					m.clampCursor()
					m.clampOffset()
				} else if line.Type == "diagnostic" {
					m.diagnostics[line.DiagIdx].Expanded = !m.diagnostics[line.DiagIdx].Expanded
					m.rebuildLines()
					m.clampCursor()
					m.clampOffset()
				}
			}

		case "pgup", "ctrl+u":
			m.cursor -= m.height / 2
			m.clampCursor()
			m.ensureCursorVisible()

		case "pgdown", "ctrl+d":
			m.cursor += m.height / 2
			m.clampCursor()
			m.ensureCursorVisible()

		case "home", "g":
			m.cursor = 0
			m.offset = 0

		case "end", "G":
			m.cursor = len(m.lines) - 1
			m.ensureCursorVisible()

		case "e":
			// Expand all
			for i := range m.resources {
				m.resources[i].Expanded = true
			}
			for i := range m.diagnostics {
				m.diagnostics[i].Expanded = true
			}
			m.rebuildLines()
			m.clampCursor()
			m.clampOffset()

		case "c":
			// Collapse all
			for i := range m.resources {
				m.resources[i].Expanded = false
			}
			for i := range m.diagnostics {
				m.diagnostics[i].Expanded = false
			}
			m.rebuildLines()
			m.clampCursor()
			m.clampOffset()
		}
	}
	return m, nil
}

func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	// Calculate visible area
	visibleHeight := m.height - 6
	if visibleHeight < 5 {
		visibleHeight = 5
	}

	// Slice visible lines
	startLine := m.offset
	endLine := startLine + visibleHeight
	if startLine > len(m.lines) {
		startLine = len(m.lines)
	}
	if endLine > len(m.lines) {
		endLine = len(m.lines)
	}
	if startLine < 0 {
		startLine = 0
	}

	// Build output
	var output strings.Builder

	// Header
	header := headerStyle.Render("Terraform Plan Viewer")
	controls := dimStyle.Render(" ↑↓:navigate  ^u/^d:half-page  Enter:expand  e/c:expand/collapse all  q:quit")
	output.WriteString(header + controls + "\n\n")

	// Scroll indicator (top)
	if startLine > 0 {
		output.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more lines above\n", startLine)))
	}

	// Content
	for i := startLine; i < endLine; i++ {
		line := m.lines[i]
		isSelected := i == m.cursor

		switch line.Type {
		case "diagnostic":
			diag := m.diagnostics[line.DiagIdx]
			var style lipgloss.Style
			var symbol string
			if diag.Severity == "error" {
				style = errorStyle
				symbol = "✗"
			} else {
				style = warningStyle
				symbol = "⚠"
			}

			expandIcon := "▸"
			if diag.Expanded {
				expandIcon = "▾"
			}

			content := fmt.Sprintf("%s %s %s", expandIcon, symbol, diag.Summary)
			if isSelected {
				content = selectedStyle.Render("► " + content)
			} else {
				content = "  " + style.Render(content)
			}
			output.WriteString(content + "\n")

		case "diagnostic_detail":
			diag := m.diagnostics[line.DiagIdx]
			var style lipgloss.Style
			if diag.Severity == "error" {
				style = errorStyle
			} else {
				style = warningStyle
			}
			if isSelected {
				output.WriteString(selectedStyle.Render("►   " + line.Content) + "\n")
			} else {
				output.WriteString("    " + style.Render(line.Content) + "\n")
			}

		case "resource":
			rc := m.resources[line.ResourceIdx]
			symbol := getSymbol(rc.Action)
			style := getStyleForAction(rc.Action)

			expandIcon := "▸"
			if rc.Expanded {
				expandIcon = "▾"
			}

			content := fmt.Sprintf("%s %s %s", expandIcon, symbol, rc.Address)
			if isSelected {
				content = selectedStyle.Render("► " + content)
			} else {
				content = "  " + style.Render(content)
			}
			output.WriteString(content + "\n")

		case "attribute":
			attr := line.Content
			styledAttr := styleAttribute(attr)
			if isSelected {
				output.WriteString(selectedStyle.Render("►   " + attr) + "\n")
			} else {
				output.WriteString("    " + styledAttr + "\n")
			}
		}
	}

	// Scroll indicator (bottom)
	remaining := len(m.lines) - endLine
	if remaining > 0 {
		output.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more lines below\n", remaining)))
	}

	// Footer with summary
	output.WriteString("\n" + getSummary(m.resources, m.diagnostics))

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

func getSummary(resources []ResourceChange, diagnostics []Diagnostic) string {
	var parts []string

	// Count diagnostics
	errorCount := 0
	warningCount := 0
	for _, d := range diagnostics {
		if d.Severity == "error" {
			errorCount++
		} else {
			warningCount++
		}
	}

	if errorCount > 0 {
		parts = append(parts, errorStyle.Render(fmt.Sprintf("✗%d error", errorCount)))
	}
	if warningCount > 0 {
		parts = append(parts, warningStyle.Render(fmt.Sprintf("⚠%d warning", warningCount)))
	}

	// Count resource changes
	counts := make(map[string]int)
	for _, r := range resources {
		counts[r.Action]++
	}

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

func parsePlan(reader io.Reader) ([]ResourceChange, []Diagnostic) {
	scanner := bufio.NewScanner(reader)
	resources := make([]ResourceChange, 0)
	diagnostics := make([]Diagnostic, 0)

	headerPattern := regexp.MustCompile(`^\s*# (.+?) (will be created|will be destroyed|will be updated in-place|must be replaced|will be imported)`)
	errorPattern := regexp.MustCompile(`^│\s*Error:\s*(.+)$`)
	warningPattern := regexp.MustCompile(`^│\s*Warning:\s*(.+)$`)

	var currentResource *ResourceChange
	var currentDiag *Diagnostic
	inResource := false
	inDiagnostic := false
	bracketDepth := 0

	for scanner.Scan() {
		line := stripANSI(scanner.Text())

		// Check for diagnostic block start (╷)
		if strings.HasPrefix(line, "╷") {
			inDiagnostic = true
			continue
		}

		// Check for diagnostic block end (╵)
		if strings.HasPrefix(line, "╵") {
			if currentDiag != nil {
				diagnostics = append(diagnostics, *currentDiag)
				currentDiag = nil
			}
			inDiagnostic = false
			continue
		}

		// Parse error/warning inside diagnostic block
		if inDiagnostic {
			if match := errorPattern.FindStringSubmatch(line); match != nil {
				currentDiag = &Diagnostic{
					Severity: "error",
					Summary:  match[1],
					Detail:   make([]string, 0),
				}
				continue
			}
			if match := warningPattern.FindStringSubmatch(line); match != nil {
				currentDiag = &Diagnostic{
					Severity: "warning",
					Summary:  match[1],
					Detail:   make([]string, 0),
				}
				continue
			}
			// Capture detail lines
			if currentDiag != nil && strings.HasPrefix(line, "│") {
				detail := strings.TrimPrefix(line, "│")
				detail = strings.TrimSpace(detail)
				if detail != "" {
					currentDiag.Detail = append(currentDiag.Detail, detail)
				}
			}
			continue
		}

		// Check for resource header
		if match := headerPattern.FindStringSubmatch(line); match != nil {
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

		if currentResource != nil && strings.Contains(line, " resource \"") {
			inResource = true
			bracketDepth = strings.Count(line, "{") - strings.Count(line, "}")
			continue
		}

		if inResource {
			bracketDepth += strings.Count(line, "{")
			bracketDepth -= strings.Count(line, "}")

			if currentResource != nil && !strings.Contains(line, " resource \"") {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" && trimmed != "{" && trimmed != "}" {
					currentResource.Attributes = append(currentResource.Attributes, trimmed)
				}
			}

			if bracketDepth == 0 && strings.Contains(line, "}") {
				if currentResource != nil {
					resources = append(resources, *currentResource)
					currentResource = nil
				}
				inResource = false
			}
		}
	}

	if currentResource != nil {
		resources = append(resources, *currentResource)
	}

	return resources, diagnostics
}

func main() {
	resources, diagnostics := parsePlan(os.Stdin)

	if len(resources) == 0 && len(diagnostics) == 0 {
		fmt.Fprintln(os.Stderr, "No resource changes or diagnostics found in plan")
		os.Exit(1)
	}

	m := Model{resources: resources, diagnostics: diagnostics}
	m.rebuildLines()

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
