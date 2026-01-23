// Package main provides a terminal UI for viewing Terraform plan output.
// It supports both piped input (terraform plan | terraui) and interactive mode
// (terraui terraform apply) with PTY support for handling prompts.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/creack/pty"
)

// UI constants for layout calculations and behavior
const (
	headerFooterHeight     = 6 // Lines reserved for header, footer, and margins
	minVisibleHeight       = 5 // Minimum lines to show in viewport
	mouseScrollLines       = 3 // Lines to scroll per mouse wheel tick
	uiTickRate             = 50 * time.Millisecond
	streamBufferSize       = 100 // Buffer size for stream channel
	processShutdownTimeout = 5 * time.Second
)

// LineType represents the type of a display line
type LineType int

const (
	LineTypeResource LineType = iota
	LineTypeAttribute
	LineTypeDiagnostic
	LineTypeDiagnosticDetail
	LineTypeLog
)

// RenderingMode represents the active color palette
type RenderingMode int

const (
	RenderingModeDashboard RenderingMode = iota
	RenderingModeHighContrast
)

// ResourceChange represents a single resource change from terraform plan
type ResourceChange struct {
	Address    string   // Resource address (e.g., "aws_instance.web")
	Action     string   // Action type: create, update, destroy, replace, import
	ActionText string   // Original text like "will be updated in-place", "must be replaced"
	Attributes []string // List of attribute changes
	Expanded   bool     // Whether details are expanded in UI
}

// Diagnostic represents an error or warning from Terraform
type Diagnostic struct {
	Severity string   // "error" or "warning"
	Summary  string   // Main message
	Detail   []string // Additional detail lines
	Expanded bool     // Whether details are expanded in UI
}

// Line represents a single display line in the UI
type Line struct {
	Type        LineType // Type of line content
	ResourceIdx int      // Index into resources slice (-1 if not applicable)
	DiagIdx     int      // Index into diagnostics slice (-1 if not applicable)
	AttrIdx     int      // Index into attributes/details (-1 for headers)
	Content     string   // Raw content for display
}

// StreamMsg carries parsed content from the input stream to the UI
type StreamMsg struct {
	Resource   *ResourceChange
	Diagnostic *Diagnostic
	LogLine    *string
	Prompt     *string // Partial line that looks like a prompt (no trailing newline)
	Done       bool    // Signals end of input stream
}

// tickMsg triggers periodic UI updates for batched rendering
type tickMsg time.Time

// Model holds the application state for the Bubble Tea framework
type Model struct {
	// Data
	resources   []ResourceChange
	diagnostics []Diagnostic
	logs        []string
	lines       []Line // Computed display lines based on expand state

	// UI state
	cursor     int  // Current line index
	height     int  // Terminal height
	offset     int  // Scroll offset
	ready      bool // Whether initial size is known
	showLogs      bool // Toggle between log view and plan view
	autoScroll    bool // Auto-scroll to bottom on new content
	renderingMode RenderingMode
	done          bool // Input stream finished
	needsSync     bool // Pending rebuild of lines slice

	// PTY/Interactive mode
	ptyFile   *os.File
	inputMode bool   // Currently accepting user input
	userInput string // Buffer for user typing
	prompt    string // Active prompt from stream

	// Concurrency
	streamChan chan StreamMsg     // Channel for receiving parsed content
	cancelFunc context.CancelFunc // For signaling goroutine shutdown
}

// Styles using Catppuccin Mocha color palette
var (
	// Header badges
	headerPlanStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#89b4fa")).Padding(0, 1)
	headerLogStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#cba6f7")).Padding(0, 1)
	inputModeStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#a6e3a1")).Padding(0, 1)

	// Resource action colors (bold for headers)
	createStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Bold(true) // Green
	updateStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af")).Bold(true) // Yellow
	destroyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true) // Red
	replaceStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7")).Bold(true) // Mauve
	importStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#89dceb")).Bold(true) // Sky

	// Diagnostic colors
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true) // Red
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")).Bold(true) // Peach
	promptStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f5c2e7")).Bold(true) // Pink

	// Attribute colors (non-bold for content)
	addAttrStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))            // Green
	removeAttrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))            // Red
	changeAttrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af"))            // Yellow
	forcesStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true) // Red bold

	// UI chrome colors
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#7f849c"))            // Gray
	defaultStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4"))            // Text
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#cdd6f4")).Background(lipgloss.Color("#45475a")) // Highlight
)

// Pre-compiled regex patterns for parsing
var (
	headerPattern  = regexp.MustCompile(`^\s*# (.+?) (will be created|will be destroyed|will be updated in-place|must be replaced|will be imported)`)
	errorPattern   = regexp.MustCompile(`Error:\s*(.+)`)
	warningPattern = regexp.MustCompile(`Warning:\s*(.+)`)
	promptPattern  = regexp.MustCompile(`Enter a value:\s*$`)
	ansiPattern    = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
)

// Init implements tea.Model. Starts input reading and periodic ticks.
func (m Model) Init() tea.Cmd {
	var reader io.Reader = os.Stdin
	if m.ptyFile != nil {
		reader = m.ptyFile
	}

	// Start the input reading goroutine
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	go m.readInputStream(ctx, reader)

	return tea.Batch(
		m.waitForStreamMsg(),
		tickCmd(),
	)
}

// readInputStream reads from the input and sends parsed messages to streamChan.
// Runs in a separate goroutine and respects context cancellation.
func (m *Model) readInputStream(ctx context.Context, reader io.Reader) {
	defer close(m.streamChan)

	buf := make([]byte, 4096)
	var lineBuffer string
	var currentResource *ResourceChange
	var diagLines []string
	inResource := false
	inDiagnostic := false
	bracketDepth := 0

	processLine := func(rawLine string) {
		line := stripANSI(rawLine)

		// Diagnostic block handling
		if strings.HasPrefix(line, "╷") {
			inDiagnostic = true
			diagLines = make([]string, 0)
			return
		}
		if strings.HasPrefix(line, "╵") {
			if inDiagnostic && len(diagLines) > 0 {
				diag := parseDiagnosticBlock(diagLines)
				if diag != nil {
					select {
					case m.streamChan <- StreamMsg{Diagnostic: diag}:
					case <-ctx.Done():
						return
					}
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

		// Resource header detection
		if match := headerPattern.FindStringSubmatch(line); match != nil {
			if currentResource != nil {
				res := *currentResource
				select {
				case m.streamChan <- StreamMsg{Resource: &res}:
				case <-ctx.Done():
					return
				}
				currentResource = nil
			}
			currentResource = &ResourceChange{
				Address:    match[1],
				Action:     parseAction(match[2]),
				ActionText: match[2],
				Attributes: make([]string, 0),
			}
			return
		}

		// Resource body parsing
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
					select {
					case m.streamChan <- StreamMsg{Resource: &res}:
					case <-ctx.Done():
						return
					}
					currentResource = nil
				}
				inResource = false
			}
			return
		}

		// Generic log line
		if strings.TrimSpace(line) != "" {
			l := line
			select {
			case m.streamChan <- StreamMsg{LogLine: &l}:
			case <-ctx.Done():
				return
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

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
				line := strings.TrimSuffix(lineBuffer[:idx], "\r")
				lineBuffer = lineBuffer[idx+1:]
				processLine(line)
			}

			// Check for prompt (no trailing newline)
			cleanBuffer := stripANSI(lineBuffer)
			if promptPattern.MatchString(cleanBuffer) {
				p := strings.TrimSpace(cleanBuffer)
				select {
				case m.streamChan <- StreamMsg{Prompt: &p}:
				case <-ctx.Done():
					return
				}
			}
		}
		if err != nil {
			break
		}
	}

	// Flush any remaining resource
	if currentResource != nil {
		res := *currentResource
		select {
		case m.streamChan <- StreamMsg{Resource: &res}:
		case <-ctx.Done():
		}
	}

	select {
	case m.streamChan <- StreamMsg{Done: true}:
	case <-ctx.Done():
	}
}

// waitForStreamMsg returns a command that waits for the next stream message
func (m Model) waitForStreamMsg() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-m.streamChan
		if !ok {
			return StreamMsg{Done: true}
		}
		return msg
	}
}

// tickCmd returns a command for periodic UI updates
func tickCmd() tea.Cmd {
	return tea.Tick(uiTickRate, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// visibleHeight calculates the number of content lines visible in the viewport
func (m *Model) visibleHeight() int {
	h := m.height - headerFooterHeight
	if m.prompt != "" {
		h -= 2 // Reserve space for pinned prompt
	}
	if h < minVisibleHeight {
		h = minVisibleHeight
	}
	return h
}

// rebuildLines reconstructs the display lines based on current expand state
func (m *Model) rebuildLines() {
	m.lines = nil

	if m.showLogs {
		for i, log := range m.logs {
			m.lines = append(m.lines, Line{
				Type:    LineTypeLog,
				Content: log,
				AttrIdx: i,
			})
		}
		return
	}

	// Plan view: diagnostics first, then resources
	for i, diag := range m.diagnostics {
		m.lines = append(m.lines, Line{
			Type:        LineTypeDiagnostic,
			DiagIdx:     i,
			ResourceIdx: -1,
			AttrIdx:     -1,
		})
		if diag.Expanded {
			for j, detail := range diag.Detail {
				m.lines = append(m.lines, Line{
					Type:        LineTypeDiagnosticDetail,
					DiagIdx:     i,
					ResourceIdx: -1,
					AttrIdx:     j,
					Content:     detail,
				})
			}
		}
	}

	for i, rc := range m.resources {
		m.lines = append(m.lines, Line{
			Type:        LineTypeResource,
			ResourceIdx: i,
			DiagIdx:     -1,
			AttrIdx:     -1,
		})
		if rc.Expanded {
			for j, attr := range rc.Attributes {
				m.lines = append(m.lines, Line{
					Type:        LineTypeAttribute,
					ResourceIdx: i,
					DiagIdx:     -1,
					AttrIdx:     j,
					Content:     attr,
				})
			}
		}
	}
}

// clampCursor ensures cursor stays within valid bounds
func (m *Model) clampCursor() {
	if m.cursor < 0 {
		m.cursor = 0
	}
	maxCursor := len(m.lines) - 1
	if maxCursor < 0 {
		maxCursor = 0
	}
	if m.cursor > maxCursor {
		m.cursor = maxCursor
	}
}

// ensureCursorVisible adjusts offset to keep cursor in view
func (m *Model) ensureCursorVisible() {
	vh := m.visibleHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	} else if m.cursor >= m.offset+vh {
		m.offset = m.cursor - vh + 1
	}
	m.clampOffset()
}

// clampOffset ensures scroll offset stays within valid bounds
func (m *Model) clampOffset() {
	if m.offset < 0 {
		m.offset = 0
	}
	maxOffset := len(m.lines) - m.visibleHeight()
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
}

// toggleRenderingMode switches between Dashboard and HighContrast modes
func (m *Model) toggleRenderingMode() {
	if m.renderingMode == RenderingModeDashboard {
		m.renderingMode = RenderingModeHighContrast
	} else {
		m.renderingMode = RenderingModeDashboard
	}
}

// Update implements tea.Model. Handles all messages and user input.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if m.needsSync {
			m.rebuildLines()
			if m.autoScroll || !m.ready {
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
		return m, m.waitForStreamMsg()

	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.ready = true
		m.needsSync = true
		return m, nil

	case tea.MouseMsg:
		return m.handleMouseMsg(msg)

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	}

	return m, nil
}

// handleMouseMsg processes mouse events
func (m Model) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.autoScroll = false

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.cursor -= mouseScrollLines
		m.clampCursor()
		m.ensureCursorVisible()

	case tea.MouseButtonWheelDown:
		m.cursor += mouseScrollLines
		m.clampCursor()
		m.ensureCursorVisible()

	case tea.MouseButtonLeft:
		if msg.Action == tea.MouseActionPress {
			headerOffset := 2
			if m.offset > 0 {
				headerOffset = 3 // Account for "more above" indicator
			}
			clickedLine := m.offset + msg.Y - headerOffset
			if msg.Y >= headerOffset && clickedLine >= 0 && clickedLine < len(m.lines) {
				if m.cursor == clickedLine {
					m.toggleExpand(clickedLine)
				} else {
					m.cursor = clickedLine
				}
			}
		}
	}

	return m, nil
}

// handleKeyMsg processes keyboard input
func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.autoScroll = false

	// Input mode: handle typing
	if m.inputMode && m.ptyFile != nil {
		return m.handleInputMode(msg)
	}

	// Normal navigation mode
	switch msg.String() {
	case "q", "ctrl+c":
		if m.cancelFunc != nil {
			m.cancelFunc()
		}
		return m, tea.Quit

	case "i":
		if m.ptyFile != nil {
			m.inputMode = true
		}

	case "l", "L":
		m.showLogs = !m.showLogs
		m.rebuildLines()
		m.cursor = 0
		m.offset = 0
		m.autoScroll = false

	case "m":
		m.toggleRenderingMode()

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
		m.toggleExpand(m.cursor)

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
		m.expandAll(true)

	case "c":
		m.expandAll(false)
	}

	return m, nil
}

// handleInputMode processes keyboard input when in input mode
func (m Model) handleInputMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.inputMode = false

	case tea.KeyCtrlC:
		if _, err := m.ptyFile.Write([]byte{3}); err != nil {
			// Log error but continue - user wants to quit anyway
		}
		if m.cancelFunc != nil {
			m.cancelFunc()
		}
		return m, tea.Quit

	case tea.KeyBackspace, tea.KeyDelete:
		if len(m.userInput) > 0 {
			m.userInput = m.userInput[:len(m.userInput)-1]
		}

	case tea.KeyRunes:
		m.userInput += string(msg.Runes)

	case tea.KeySpace:
		m.userInput += " "

	case tea.KeyEnter:
		payload := m.userInput + "\n"
		if _, err := m.ptyFile.Write([]byte(payload)); err != nil {
			// PTY write failed - could set error state here
		}
		m.userInput = ""
		m.prompt = ""
		m.inputMode = false
		m.showLogs = true
		m.autoScroll = true
		m.rebuildLines()
	}

	return m, nil
}

// toggleExpand toggles the expanded state of a resource or diagnostic at lineIdx
func (m *Model) toggleExpand(lineIdx int) {
	if lineIdx < 0 || lineIdx >= len(m.lines) || m.showLogs {
		return
	}

	line := m.lines[lineIdx]
	switch line.Type {
	case LineTypeResource:
		if line.ResourceIdx >= 0 && line.ResourceIdx < len(m.resources) {
			m.resources[line.ResourceIdx].Expanded = !m.resources[line.ResourceIdx].Expanded
			m.rebuildLines()
			m.clampCursor()
			m.clampOffset()
		}
	case LineTypeDiagnostic:
		if line.DiagIdx >= 0 && line.DiagIdx < len(m.diagnostics) {
			m.diagnostics[line.DiagIdx].Expanded = !m.diagnostics[line.DiagIdx].Expanded
			m.rebuildLines()
			m.clampCursor()
			m.clampOffset()
		}
	}
}

// expandAll sets the expanded state of all resources and diagnostics
func (m *Model) expandAll(expanded bool) {
	if m.showLogs {
		return
	}
	for i := range m.resources {
		m.resources[i].Expanded = expanded
	}
	for i := range m.diagnostics {
		m.diagnostics[i].Expanded = expanded
	}
	m.rebuildLines()
	m.clampCursor()
	m.clampOffset()
}

// View implements tea.Model. Renders the UI.
func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	vh := m.visibleHeight()
	startLine := m.offset
	endLine := startLine + vh

	// Clamp bounds
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

	// Header
	output.WriteString(m.renderHeader())
	output.WriteString("\n\n")

	// Scroll indicator (top)
	if startLine > 0 {
		output.WriteString(dimStyle.Render(fmt.Sprintf("  ↑ %d more lines above\n", startLine)))
	}

	// Content lines
	for i := startLine; i < endLine; i++ {
		output.WriteString(m.renderLine(i))
		output.WriteString("\n")
	}

	// Scroll indicator (bottom)
	if remaining := len(m.lines) - endLine; remaining > 0 {
		output.WriteString(dimStyle.Render(fmt.Sprintf("  ↓ %d more lines below\n", remaining)))
	}

	// Pinned prompt
	if m.prompt != "" {
		output.WriteString("\n")
		output.WriteString(m.renderPrompt())
	}

	// Footer
	output.WriteString("\n")
	output.WriteString(m.renderFooter())

	return output.String()
}

// renderHeader renders the header bar with mode, status, and controls
func (m Model) renderHeader() string {
	var header string
	if m.inputMode {
		header = inputModeStyle.Render("INPUT") + " " + dimStyle.Render("Interactive Mode")
	} else if m.showLogs {
		header = headerLogStyle.Render("LOGS") + " " + dimStyle.Render("Terraform Output")
	} else {
		header = headerPlanStyle.Render("PLAN") + " " + dimStyle.Render("Terraform Viewer")
	}

	var status string
	if m.prompt != "" {
		status = warningStyle.Render(" ● WAITING FOR INPUT")
	} else if !m.done {
		status = dimStyle.Render(" ● Live")
	} else {
		status = dimStyle.Render(" ● Done")
	}

	controls := dimStyle.Render(" ↑↓:navigate  q:quit  L:mode")
	if m.ptyFile != nil {
		if m.inputMode {
			controls += dimStyle.Render("  Esc:exit input")
		} else {
			controls += dimStyle.Render("  i:enter input")
		}
	}

	return header + status + "  " + controls
}

// renderLine renders a single content line
func (m Model) renderLine(idx int) string {
	if idx < 0 || idx >= len(m.lines) {
		return ""
	}

	line := m.lines[idx]
	isSelected := idx == m.cursor

	switch line.Type {
	case LineTypeLog:
		return m.renderLogLine(line.Content, isSelected)
	case LineTypeDiagnostic:
		return m.renderDiagnosticLine(line.DiagIdx, isSelected)
	case LineTypeDiagnosticDetail:
		return m.renderDiagnosticDetailLine(line.DiagIdx, line.Content, isSelected)
	case LineTypeResource:
		return m.renderResourceLine(line.ResourceIdx, isSelected)
	case LineTypeAttribute:
		return m.renderAttributeLine(line.Content, isSelected)
	}

	return ""
}

// renderLogLine renders a log line with contextual styling
func (m Model) renderLogLine(content string, isSelected bool) string {
	var style lipgloss.Style

	switch {
	case strings.Contains(content, "Error:"):
		style = errorStyle
	case strings.Contains(content, "Warning:"):
		style = warningStyle
	case strings.HasPrefix(content, "Initializing"):
		style = importStyle
	case strings.Contains(content, "Success!"),
		strings.Contains(content, "Creation complete"),
		strings.Contains(content, "Complete!"):
		style = createStyle
	case strings.Contains(content, "Enter a value:"):
		style = forcesStyle
	case strings.Contains(content, "Creating..."),
		strings.Contains(content, "Destroying..."),
		strings.Contains(content, "Modifying..."):
		style = updateStyle
	default:
		style = defaultStyle
	}

	if isSelected {
		return selectedStyle.Render("► " + content)
	}
	return "  " + style.Render(content)
}

// renderDiagnosticLine renders a diagnostic header line
func (m Model) renderDiagnosticLine(diagIdx int, isSelected bool) string {
	if diagIdx < 0 || diagIdx >= len(m.diagnostics) {
		return ""
	}

	diag := m.diagnostics[diagIdx]
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
		return selectedStyle.Render("► " + content)
	}
	return "  " + style.Render(content)
}

// renderDiagnosticDetailLine renders a diagnostic detail line
func (m Model) renderDiagnosticDetailLine(diagIdx int, content string, isSelected bool) string {
	if diagIdx < 0 || diagIdx >= len(m.diagnostics) {
		return ""
	}

	diag := m.diagnostics[diagIdx]
	var style lipgloss.Style
	if diag.Severity == "error" {
		style = errorStyle
	} else {
		style = warningStyle
	}

	if isSelected {
		return selectedStyle.Render("►   " + content)
	}
	return "    " + style.Render(content)
}

// renderResourceLine renders a resource header line
func (m Model) renderResourceLine(resIdx int, isSelected bool) string {
	if resIdx < 0 || resIdx >= len(m.resources) {
		return ""
	}

	rc := m.resources[resIdx]
	symbol := getSymbol(rc.Action)
	style := getStyleForAction(rc.Action)

	expandIcon := "▸"
	if rc.Expanded {
		expandIcon = "▾"
	}

	if isSelected {
		selBg := lipgloss.Color("#45475a")
		arrowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")).Background(selBg).Bold(true)
		// Create new styles with background - don't modify originals
		prefixStyle := lipgloss.NewStyle().Foreground(style.GetForeground()).Background(selBg).Bold(true)
		suffixStyle := lipgloss.NewStyle().Foreground(dimStyle.GetForeground()).Background(selBg)

		prefix := prefixStyle.Render(fmt.Sprintf("%s %s %s", expandIcon, symbol, rc.Address))
		suffix := suffixStyle.Render(rc.ActionText)
		return fmt.Sprintf("%s%s %s", arrowStyle.Render("► "), prefix, suffix)
	}

	prefix := style.Render(fmt.Sprintf("%s %s %s", expandIcon, symbol, rc.Address))
	suffix := dimStyle.Render(rc.ActionText)
	return fmt.Sprintf("  %s %s", prefix, suffix)
}

// renderAttributeLine renders an attribute line with syntax highlighting
func (m Model) renderAttributeLine(content string, isSelected bool) string {
	if isSelected {
		return selectedStyle.Render("►   " + content)
	}
	return "    " + styleAttribute(content)
}

// renderPrompt renders the pinned prompt with optional input cursor
func (m Model) renderPrompt() string {
	promptLine := promptStyle.Render(">> " + m.prompt)
	if m.inputMode {
		promptLine += " " + createStyle.Render(m.userInput) + dimStyle.Render("█")
	}
	return promptLine
}

// renderFooter renders the summary footer
func (m Model) renderFooter() string {
	if m.showLogs {
		return dimStyle.Render(fmt.Sprintf("%d lines", len(m.lines)))
	}
	return getSummary(m.resources, m.diagnostics)
}

// styleAttribute applies syntax highlighting to an attribute line
func styleAttribute(attr string) string {
	// Special handling for "# forces replacement"
	if idx := strings.Index(attr, "# forces replacement"); idx != -1 {
		before := attr[:idx]
		forces := "# forces replacement"
		after := attr[idx+len(forces):]
		return styleAttributePrefix(before) + forcesStyle.Render(forces) + defaultStyle.Render(after)
	}
	return styleAttributePrefix(attr)
}

// styleAttributePrefix styles an attribute based on its prefix (+/-/~/etc)
func styleAttributePrefix(attr string) string {
	trimmed := strings.TrimSpace(attr)

	switch {
	case strings.HasPrefix(trimmed, "+"):
		return addAttrStyle.Render(attr)
	case strings.HasPrefix(trimmed, "-"):
		return removeAttrStyle.Render(attr)
	case strings.HasPrefix(trimmed, "~"):
		return changeAttrStyle.Render(attr)
	case strings.HasPrefix(trimmed, "#"):
		return dimStyle.Render(attr)
	default:
		return dimStyle.Render(attr)
	}
}

// getSummary generates the summary line showing change counts
func getSummary(resources []ResourceChange, diagnostics []Diagnostic) string {
	var parts []string

	// Count diagnostics
	var errorCount, warningCount int
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
		return dimStyle.Render("No changes")
	}
	return strings.Join(parts, "  ")
}

// getSymbol returns the symbol for a given action type
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

// getStyleForAction returns the style for a given action type
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

// parseAction converts Terraform action text to internal action type
func parseAction(actionText string) string {
	switch actionText {
	case "will be created":
		return "create"
	case "will be updated in-place":
		return "update"
	case "will be destroyed":
		return "destroy"
	case "must be replaced":
		return "replace"
	case "will be imported":
		return "import"
	default:
		return ""
	}
}

// stripANSI removes ANSI escape codes from a string
func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// parseDiagnosticBlock parses a diagnostic block into a Diagnostic struct
func parseDiagnosticBlock(lines []string) *Diagnostic {
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

		if match := errorPattern.FindStringSubmatch(trimmed); match != nil {
			if severity == "" {
				severity = "error"
				summary = match[1]
			} else {
				details = append(details, trimmed)
			}
			continue
		}

		if match := warningPattern.FindStringSubmatch(trimmed); match != nil {
			if severity == "" {
				severity = "warning"
				summary = match[1]
			} else {
				details = append(details, trimmed)
			}
			continue
		}

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
	var cmd *exec.Cmd

	// Interactive mode: terraui terraform apply ...
	if len(os.Args) > 1 {
		cmd = exec.Command(os.Args[1], os.Args[2:]...)
		var err error
		ptyFile, err = pty.Start(cmd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error starting PTY: %v\n", err)
			os.Exit(1)
		}
	}

	// Create model with buffered channel
	m := Model{
		showLogs:      true,
		autoScroll:    true,
		renderingMode: RenderingModeDashboard,
		ptyFile:       ptyFile,
		streamChan:    make(chan StreamMsg, streamBufferSize),
	}

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Cleanup function for PTY and process
	cleanup := func() {
		if ptyFile != nil {
			ptyFile.Close()
		}
		if cmd != nil && cmd.Process != nil {
			// Try graceful shutdown first
			cmd.Process.Signal(syscall.SIGTERM)

			// Wait with timeout
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()

			select {
			case <-done:
				// Process exited cleanly
			case <-time.After(processShutdownTimeout):
				// Force kill if still running
				cmd.Process.Kill()
				<-done
			}
		}
	}

	// Start signal handler
	go func() {
		<-sigChan
		cleanup()
		os.Exit(0)
	}()

	// Ensure cleanup on normal exit
	defer cleanup()

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
