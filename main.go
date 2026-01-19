package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/creack/pty"
)

type ResourceChange struct {
	Address    string
	Action     string
	ActionText string // Original text like "will be updated in-place", "must be replaced"
	Attributes []string
	Expanded   bool
}

type Diagnostic struct {
	Severity string // "error" or "warning"
	Summary  string
	Detail   []string
	Expanded bool
}

// Line represents a single display line
type Line struct {
	Type        string // "resource", "attribute", "diagnostic", "diagnostic_detail", "log"
	ResourceIdx int    // Which resource this belongs to (-1 if diagnostic)
	DiagIdx     int    // Which diagnostic this belongs to (-1 if resource)
	AttrIdx     int    // -1 for headers, >=0 for attribute/detail
	Content     string // Raw content
}

type StreamMsg struct {
	Resource   *ResourceChange
	Diagnostic *Diagnostic
	LogLine    *string
	Prompt     *string // New: Partial line that looks like a prompt
	Done       bool
}

// Optimization: Tick message for batched UI updates
type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*50, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func readInput(reader io.Reader) tea.Cmd {
	return func() tea.Msg {
		return doRead(reader)
	}
}

// Channel to coordinate the scanner goroutine
var streamChan = make(chan StreamMsg)

// Optimization: Pre-compile regexes
var (
	headerPattern  = regexp.MustCompile(`^\s*# (.+?) (will be created|will be destroyed|will be updated in-place|must be replaced|will be imported)`)
	errorPattern   = regexp.MustCompile(`Error:\s*(.+)`)
	warningPattern = regexp.MustCompile(`Warning:\s*(.+)`)
	promptPattern  = regexp.MustCompile(`Enter a value:\s*$`) // Detect prompt at end of buffer
)

func doRead(reader io.Reader) tea.Msg {
	go func() {
		// Use a larger buffer and manual processing to catch prompts (which don't end in \n)
		buf := make([]byte, 4096)
		var lineBuffer string

		var currentResource *ResourceChange
		var diagLines []string
		inResource := false
		inDiagnostic := false
		bracketDepth := 0

		processLine := func(rawLine string) {
			line := stripANSI(rawLine)

			// Diagnostic handling
			if strings.HasPrefix(line, "╷") {
				inDiagnostic = true
				diagLines = make([]string, 0)
				return
			}
			if strings.HasPrefix(line, "╵") {
				if inDiagnostic && len(diagLines) > 0 {
					diag := parseDiagnosticBlock(diagLines, errorPattern, warningPattern)
					if diag != nil {
						streamChan <- StreamMsg{Diagnostic: diag}
					}
				}
				diagLines = nil
				inDiagnostic = false
				return
			}
			if inDiagnostic {
				content := strings.TrimPrefix(line, "│")
				diagLines = append(diagLines, content)
				return
			}

			// Resource Header
			if match := headerPattern.FindStringSubmatch(line); match != nil {
				if currentResource != nil {
					res := *currentResource
					streamChan <- StreamMsg{Resource: &res}
					currentResource = nil
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
					ActionText: actionText,
					Attributes: make([]string, 0),
				}
				return
			}

			// Resource Body
			if currentResource != nil && strings.Contains(line, " resource \"") {
				inResource = true
				bracketDepth = strings.Count(line, "{") - strings.Count(line, "}")
				return
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
						res := *currentResource
						streamChan <- StreamMsg{Resource: &res}
						currentResource = nil
					}
					inResource = false
				}
				return
			}

			// Generic Log
			if strings.TrimSpace(line) != "" {
				l := line
				streamChan <- StreamMsg{LogLine: &l}
			}
		}

		for {
			n, err := reader.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				lineBuffer += chunk

				// Process complete lines
				for {
					idx := strings.Index(lineBuffer, "\n")
					if idx == -1 {
						break
					}
					line := lineBuffer[:idx]
					lineBuffer = lineBuffer[idx+1:]
					// Strip \r if present (common in PTY)
					line = strings.TrimSuffix(line, "\r")
					processLine(line)
				}

				// Check remaining buffer for Prompt (no newline)
				cleanBuffer := stripANSI(lineBuffer)
				if promptPattern.MatchString(cleanBuffer) {
					p := strings.TrimSpace(cleanBuffer)
					streamChan <- StreamMsg{Prompt: &p}
					// We don't clear lineBuffer; we'll process it when the user hits Enter (and \n comes)
				}
			}
			if err != nil {
				break
			}
		}

		if currentResource != nil {
			res := *currentResource
			streamChan <- StreamMsg{Resource: &res}
		}

		streamChan <- StreamMsg{Done: true}
	}()
	return nil
}

func waitForActivity() tea.Cmd {
	return func() tea.Msg {
		return <-streamChan
	}
}

type Model struct {
	resources   []ResourceChange
	diagnostics []Diagnostic
	logs        []string
	lines       []Line
	cursor      int
	height      int
	offset      int
	ready       bool
	showLogs    bool
	autoScroll  bool
	done        bool
	needsSync   bool

	ptyFile   *os.File
	inputMode bool
	userInput string // Local echo of what user is typing
	prompt    string // Active prompt detected from stream
}

// Styles
var (
	headerPlanStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#89b4fa")).Padding(0, 1) // Blue Bg
	headerLogStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#cba6f7")).Padding(0, 1) // Mauve Bg
	inputModeStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#a6e3a1")).Padding(0, 1) // Green for Input

	// Resource header colors (Catppuccin Mocha + Vibrant Red)
	createStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Bold(true) // Green
	updateStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af")).Bold(true) // Yellow
	destroyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true) // Vibrant Red
	replaceStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7")).Bold(true) // Mauve
	importStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#89dceb")).Bold(true) // Sky
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true) // Vibrant Red
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")).Bold(true) // Peach
	promptStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f5c2e7")).Bold(true) // Pink/Fuchsia for prompts

	// Attribute colors
	addAttrStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))            // Green
	removeAttrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))            // Vibrant Red
	changeAttrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af"))            // Yellow
	forcesStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true) // Vibrant Red

	// UI colors
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#7f849c"))            // Overlay1
	defaultStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4"))            // Text
	selectedStyle = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("#45475a")) // Surface1
)

func (m Model) Init() tea.Cmd {
	// If we have a PTY, we are responsible for closing it eventually,
	// but mostly we just read from it until EOF.
	var reader io.Reader = os.Stdin
	if m.ptyFile != nil {
		reader = m.ptyFile
	}

	return tea.Batch(
		readInput(reader),
		waitForActivity(),
		tickCmd(),
	)
}

// Rebuild the lines slice based on current expand state
func (m *Model) rebuildLines() {
	m.lines = nil

	if m.showLogs {
		for i, log := range m.logs {
			m.lines = append(m.lines, Line{Type: "log", Content: log, AttrIdx: i})
		}
		return
	}

	// Plan View
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

	// Adjust visible height if we are showing a pinned prompt
	if m.prompt != "" {
		visibleHeight -= 2 // Newline + Prompt line
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

	// Adjust visible height if we are showing a pinned prompt
	if m.prompt != "" {
		visibleHeight -= 2
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
	case tickMsg:
		if m.needsSync {
			m.rebuildLines()
			if m.autoScroll || (!m.ready) {
				m.cursor = len(m.lines) - 1
				m.clampCursor()
				m.ensureCursorVisible()
			}
			m.needsSync = false
		}
		return m, tickCmd()

	case StreamMsg:
		if msg.Done {
			m.done = true
			m.needsSync = true
			return m, nil
		}

		if msg.Resource != nil {
			m.resources = append(m.resources, *msg.Resource)
			m.showLogs = false
			m.needsSync = true
		}
		if msg.Diagnostic != nil {
			m.diagnostics = append(m.diagnostics, *msg.Diagnostic)
			m.needsSync = true
		}
		if msg.LogLine != nil {
			m.logs = append(m.logs, *msg.LogLine)
			m.needsSync = true
		}
		if msg.Prompt != nil {
			m.prompt = *msg.Prompt
			m.needsSync = true
		}

		return m, waitForActivity()

	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.ready = true
		m.needsSync = true
		return m, nil

	case tea.MouseMsg:
		m.autoScroll = false
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
				headerOffset := 2
				if m.offset > 0 {
					headerOffset = 3
				}
				clickedLine := m.offset + msg.Y - headerOffset
				if msg.Y >= headerOffset && clickedLine >= 0 && clickedLine < len(m.lines) {
					if m.cursor == clickedLine {
						line := m.lines[clickedLine]
						if !m.showLogs {
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
					} else {
						m.cursor = clickedLine
					}
				}
			}
		}
		return m, nil

	case tea.KeyMsg:
		m.autoScroll = false

		// INPUT MODE LOGIC
		if m.inputMode && m.ptyFile != nil {
			if msg.Type == tea.KeyEsc {
				m.inputMode = false
				return m, nil
			}

			// Local Echo Logic
			switch msg.Type {
			case tea.KeyBackspace, tea.KeyDelete:
				if len(m.userInput) > 0 {
					m.userInput = m.userInput[:len(m.userInput)-1]
				}
			case tea.KeyRunes:
				m.userInput += string(msg.Runes)
			case tea.KeySpace:
				m.userInput += " "
			case tea.KeyEnter:
				m.userInput = "" // Clear on enter
				m.prompt = ""    // Assume prompt is satisfied
			}

			// Forward input to PTY
			var payload []byte
			switch msg.Type {
			case tea.KeyRunes:
				payload = []byte(string(msg.Runes))
			case tea.KeyEnter:
				payload = []byte("\n")
			case tea.KeySpace:
				payload = []byte(" ")
			case tea.KeyBackspace, tea.KeyDelete:
				payload = []byte{8}
			}
			if len(payload) > 0 {
				m.ptyFile.Write(payload)
			}
			return m, nil
		}

		// NORMAL NAVIGATION MODE
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "i":
			if m.ptyFile != nil {
				m.inputMode = true
				return m, nil
			}

		case "l", "L":
			m.showLogs = !m.showLogs
			m.rebuildLines()
			m.cursor = 0
			m.offset = 0
			m.autoScroll = false
			return m, nil

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
			if m.cursor < len(m.lines) && !m.showLogs {
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
			if !m.showLogs {
				for i := range m.resources {
					m.resources[i].Expanded = true
				}
				for i := range m.diagnostics {
					m.diagnostics[i].Expanded = true
				}
				m.rebuildLines()
				m.clampCursor()
				m.clampOffset()
			}

		case "c":
			if !m.showLogs {
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
	}
	return m, nil
}

func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	visibleHeight := m.height - 6
	if visibleHeight < 5 {
		visibleHeight = 5
	}

	// Calculate if we need to reserve space for the pinned prompt
	if m.prompt != "" {
		visibleHeight -= 2 // Newline + Prompt line
	}

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

	var output strings.Builder

	// Header Logic
	var header string
	if m.inputMode {
		// Show local echo in header
		inputDisplay := m.userInput
		if inputDisplay == "" {
			inputDisplay = "(type here)"
		}
		header = inputModeStyle.Render("INPUT") + " " + dimStyle.Render("Typing: ") + createStyle.Render(inputDisplay)
	} else if m.showLogs {
		header = headerLogStyle.Render("LOGS") + " " + dimStyle.Render("Terraform Output")
	} else {
		header = headerPlanStyle.Render("PLAN") + " " + dimStyle.Render("Terraform Viewer")
	}

	status := ""
	if !m.done {
		status = dimStyle.Render(" ● Live")
	}

	controls := dimStyle.Render(" ↑↓:navigate  q:quit  L:mode")
	if m.ptyFile != nil {
		if m.inputMode {
			controls += dimStyle.Render("  Esc:exit input")
		} else {
			controls += dimStyle.Render("  i:enter input")
		}
	}
	output.WriteString(header + status + "  " + controls + "\n\n")

	if startLine > 0 {
		output.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more lines above\n", startLine)))
	}

	for i := startLine; i < endLine; i++ {
		line := m.lines[i]
		isSelected := i == m.cursor

		if line.Type == "log" {
			content := line.Content
			var style lipgloss.Style
			if strings.Contains(content, "Error:") {
				style = errorStyle
			} else if strings.Contains(content, "Warning:") {
				style = warningStyle
			} else if strings.HasPrefix(content, "Initializing") {
				style = importStyle
			} else if strings.Contains(content, "Success!") || strings.Contains(content, "Creation complete") || strings.Contains(content, "Complete!") {
				style = createStyle
			} else if strings.Contains(content, "Enter a value:") {
				style = forcesStyle // Highlight prompts
			} else if strings.Contains(content, "Creating...") || strings.Contains(content, "Destruction complete") {
				style = updateStyle
			} else {
				style = defaultStyle
			}

			if isSelected {
				output.WriteString(selectedStyle.Render("► "+content) + "\n")
			} else {
				output.WriteString("  " + style.Render(content) + "\n")
			}
			continue
		}

		// Resource View
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
				output.WriteString(selectedStyle.Render("►   "+line.Content) + "\n")
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
			if isSelected {
				selBg := lipgloss.Color("#45475a")
				arrowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")).Background(selBg).Bold(true)
				prefixStyle := style.Copy().Background(selBg)
				prefix := prefixStyle.Render(fmt.Sprintf("%s %s %s", expandIcon, symbol, rc.Address))
				suffixStyle := dimStyle.Copy().Background(selBg)
				suffix := suffixStyle.Render(rc.ActionText)
				output.WriteString(fmt.Sprintf("%s%s %s\n", arrowStyle.Render("► "), prefix, suffix))
			} else {
				prefix := style.Render(fmt.Sprintf("%s %s %s", expandIcon, symbol, rc.Address))
				suffix := dimStyle.Render(rc.ActionText)
				output.WriteString(fmt.Sprintf("  %s %s\n", prefix, suffix))
			}

		case "attribute":
			attr := line.Content
			styledAttr := styleAttribute(attr)
			if isSelected {
				output.WriteString(selectedStyle.Render("►   "+attr) + "\n")
			} else {
				output.WriteString("    " + styledAttr + "\n")
			}
		}
	}

	remaining := len(m.lines) - endLine
	if remaining > 0 {
		output.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more lines below\n", remaining)))
	}

	// Pinned Prompt
	if m.prompt != "" {
		output.WriteString("\n" + promptStyle.Render(">> "+m.prompt))
	}

	if !m.showLogs {
		output.WriteString("\n" + getSummary(m.resources, m.diagnostics))
	} else {
		output.WriteString(dimStyle.Render(fmt.Sprintf("\n%d lines", len(m.lines))))
	}

	return output.String()
}

func styleAttribute(attr string) string {
	// Check for "# forces replacement" - bold red like Terraform
	if strings.Contains(attr, "# forces replacement") {
		idx := strings.Index(attr, "# forces replacement")
		before := attr[:idx]
		forces := "# forces replacement"
		after := attr[idx+len(forces):]
		// Style the attribute part normally, then forces in bold red
		return styleAttributePrefix(before) + forcesStyle.Render(forces) + defaultStyle.Render(after)
	}

	return styleAttributePrefix(attr)
}

func styleAttributePrefix(attr string) string {
	trimmed := strings.TrimSpace(attr)

	// Style based on prefix
	if strings.HasPrefix(trimmed, "+") {
		return addAttrStyle.Render(attr)
	} else if strings.HasPrefix(trimmed, "-") {
		return removeAttrStyle.Render(attr)
	} else if strings.HasPrefix(trimmed, "~") {
		return changeAttrStyle.Render(attr)
	} else if strings.HasPrefix(trimmed, "#") {
		// Comments in gray
		return dimStyle.Render(attr)
	}

	// Unchanged attributes - gray/dim to reduce noise
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

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`) // Note: Backslash needs to be escaped in JSON string

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func parseDiagnosticBlock(lines []string, errorPattern, warningPattern *regexp.Regexp) *Diagnostic {
	if len(lines) == 0 {
		return nil
	}

	var severity, summary string
	var details []string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Check for Error: or Warning: to determine severity and get summary
		if match := errorPattern.FindStringSubmatch(trimmed); match != nil {
			if severity == "" {
				// First error becomes the summary
				severity = "error"
				summary = match[1]
			} else {
				// Additional errors go into details
				details = append(details, trimmed)
			}
			continue
		}

		if match := warningPattern.FindStringSubmatch(trimmed); match != nil {
			if severity == "" {
				// First warning becomes the summary
				severity = "warning"
				summary = match[1]
			} else {
				// Additional warnings go into details
				details = append(details, trimmed)
			}
			continue
		}

		// After we have a severity, collect remaining lines as details
		if severity != "" && i > 0 {
			details = append(details, trimmed)
		}
	}

	if severity == "" || summary == "" {
		return nil
	}

	return &Diagnostic{
		Severity: severity,
		Summary:  summary,
		Detail:   details,
	}
}

func main() {
	var ptyFile *os.File

	// 1. Interactive Mode: terraui terraform apply ...
	if len(os.Args) > 1 {
		// Run command in PTY
		cmd := exec.Command(os.Args[1], os.Args[2:]...)
		var err error
		ptyFile, err = pty.Start(cmd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error starting PTY: %v\n", err)
			os.Exit(1)
		}
		defer func() {
			ptyFile.Close() // Best effort close
			cmd.Process.Kill()
		}()
	}

	// 2. Default Mode: Pipe input
	// If ptyFile is nil, Model will use os.Stdin

	// Default: Show logs initially, autoscroll on
	m := Model{
		showLogs:   true,
		autoScroll: true,
		ptyFile:    ptyFile,
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
