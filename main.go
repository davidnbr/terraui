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

// Theme holds the styles for a rendering mode
type Theme struct {
	HeaderPlan lipgloss.Style
	HeaderLog  lipgloss.Style
	InputMode  lipgloss.Style

	Create  lipgloss.Style
	Update  lipgloss.Style
	Destroy lipgloss.Style
	Replace lipgloss.Style
	Import  lipgloss.Style

	Error   lipgloss.Style
	Warning lipgloss.Style
	Prompt  lipgloss.Style

	// Rich formatting for diagnostic markers (^ and ~ underlines)
	Underline lipgloss.Style

	AddAttr    lipgloss.Style
	RemoveAttr lipgloss.Style
	ChangeAttr lipgloss.Style
	Forces     lipgloss.Style

	Dim      lipgloss.Style
	Default  lipgloss.Style
	Selected lipgloss.Style

	// Optimized replacers
	ErrorReplacer   *strings.Replacer
	WarningReplacer *strings.Replacer
}

// ResourceChange represents a single resource change from terraform plan
type ResourceChange struct {
	Address    string   // Resource address (e.g., "aws_instance.web")
	Action     string   // Action type: create, update, destroy, replace, import
	ActionText string   // Original text like "will be updated in-place", "must be replaced"
	Attributes []string // List of attribute changes
	Expanded   bool     // Whether details are expanded in UI
}

// DiagnosticLine represents a single line of detail in a diagnostic message
type DiagnosticLine struct {
	Content  string
	IsMarker bool
}

// Diagnostic represents an error or warning from Terraform
type Diagnostic struct {
	Severity string           // "error" or "warning"
	Summary  string           // Main message
	Detail   []DiagnosticLine // Additional detail lines
	Expanded bool             // Whether details are expanded in UI
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
	cursor        int  // Current line index
	width         int  // Terminal width
	height        int  // Terminal height
	offset        int  // Scroll offset
	ready         bool // Whether initial size is known
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

	// Cached theme to avoid repeated allocations during rendering
	cachedTheme *Theme
}

func (m *Model) theme() Theme {
	if m.cachedTheme == nil {
		t := getTheme(m.renderingMode)
		m.cachedTheme = &t
	}
	return *m.cachedTheme
}

func createGuideReplacer(style lipgloss.Style) *strings.Replacer {
	return strings.NewReplacer(
		"│", style.Render("│"),
		"├", style.Render("├"),
		"─", style.Render("─"),
		"╵", style.Render("╵"),
	)
}

func getTheme(mode RenderingMode) Theme {
	if mode == RenderingModeHighContrast {
		t := Theme{
			HeaderPlan: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#89b4fa")).Padding(0, 1),
			HeaderLog:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#cba6f7")).Padding(0, 1),
			InputMode:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#a6e3a1")).Padding(0, 1),

			Create:  lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Bold(true),
			Update:  lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af")).Bold(true),
			Destroy: lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true),
			Replace: lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7")).Bold(true),
			Import:  lipgloss.NewStyle().Foreground(lipgloss.Color("#89dceb")).Bold(true),

			Error:   lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true),
			Warning: lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")).Bold(true),
			Prompt:  lipgloss.NewStyle().Foreground(lipgloss.Color("#f5c2e7")).Bold(true),

			Underline: lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Underline(true).Bold(true),

			AddAttr:    lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")),
			RemoveAttr: lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")),
			ChangeAttr: lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af")),
			Forces:     lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true),

			Dim:      lipgloss.NewStyle().Foreground(lipgloss.Color("#7f849c")),
			Default:  lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")),
			Selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#cdd6f4")).Background(lipgloss.Color("#45475a")),
		}
		t.ErrorReplacer = createGuideReplacer(t.Error)
		t.WarningReplacer = createGuideReplacer(t.Warning)
		return t
	}

	// Dashboard mode (mimics standard Terraform colors but with Catppuccin palette)
	t := Theme{
		HeaderPlan: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#89b4fa")).Padding(0, 1),
		HeaderLog:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#cba6f7")).Padding(0, 1),
		InputMode:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#1e1e2e")).Background(lipgloss.Color("#a6e3a1")).Padding(0, 1),

		Create:  lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Bold(true), // Green
		Update:  lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af")).Bold(true), // Yellow
		Destroy: lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true), // Red
		Replace: lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7")).Bold(true), // Mauve
		Import:  lipgloss.NewStyle().Foreground(lipgloss.Color("#89dceb")).Bold(true), // Sky

		Error:   lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true),
		Warning: lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")).Bold(true),
		Prompt:  lipgloss.NewStyle().Foreground(lipgloss.Color("#f5c2e7")).Bold(true),

		Underline: lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Underline(true).Bold(true),

		AddAttr:    lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")),
		RemoveAttr: lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")),
		ChangeAttr: lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af")),
		Forces:     lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true),

		Dim:      lipgloss.NewStyle().Foreground(lipgloss.Color("#7f849c")),
		Default:  lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")),
		Selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#cdd6f4")).Background(lipgloss.Color("#45475a")),
	}
	t.ErrorReplacer = createGuideReplacer(t.Error)
	t.WarningReplacer = createGuideReplacer(t.Warning)
	return t
}

// Pre-compiled regex patterns for parsing
var (
	headerPattern    = regexp.MustCompile(`^\s*# (.+?) (will be created|will be destroyed|will be updated in-place|must be replaced|will be imported)`)
	errorPattern     = regexp.MustCompile(`Error:\s*(.+)`)
	warningPattern   = regexp.MustCompile(`Warning:\s*(.+)`)
	promptPattern    = regexp.MustCompile(`Enter a value:\s*$`)
	markerPattern    = regexp.MustCompile(`^\s*on\s+.+\s+line\s+\d+`)
	underlinePattern = regexp.MustCompile(`^\s*[\^~]+`)
	ansiPattern      = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	ansiColorPattern = regexp.MustCompile(`\x1b\[(?:3[0-9]|4[0-9]|9[0-9]|10[0-9]|38;[0-9;]+|48;[0-9;]+)m`)
	ansiResetPattern = regexp.MustCompile(`\x1b\[0m`)
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
		cleanLine := stripANSI(rawLine)
		richLine := sanitizeTerraformANSI(rawLine)

		// Diagnostic block handling
		if strings.HasPrefix(cleanLine, "╷") {
			// If we're already in a diagnostic block, process the previous one
			// before starting a new one (handles missing ╵ between blocks)
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
			inDiagnostic = true
			diagLines = make([]string, 0)
			return
		}
		if strings.HasPrefix(cleanLine, "╵") {
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
			// Use cleanLine (fully stripped) to detect │ prefix, then strip from
			// richLine at the same position. This handles cases where ANSI
			// formatting codes (bold/underline) precede the │ character.
			richLineContent := richLine
			if strings.HasPrefix(cleanLine, "│") {
				// Find │ in richLine and strip everything up to and including it
				if idx := strings.Index(richLine, "│"); idx >= 0 {
					richLineContent = richLine[idx+len("│"):]
				}
			}
			diagLines = append(diagLines, richLineContent)
			return
		}

		// Resource header detection
		if match := headerPattern.FindStringSubmatch(cleanLine); match != nil {
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
		if currentResource != nil && strings.Contains(cleanLine, " resource \"") {
			inResource = true
			bracketDepth = strings.Count(cleanLine, "{") - strings.Count(cleanLine, "}")
			return
		}
		if inResource {
			if currentResource != nil {
				depthChange := strings.Count(cleanLine, "{") - strings.Count(cleanLine, "}")

				// If we hit depth 0 and the line has a closing brace, it's the resource block end
				if bracketDepth+depthChange == 0 && strings.Contains(cleanLine, "}") {
					res := *currentResource
					select {
					case m.streamChan <- StreamMsg{Resource: &res}:
					case <-ctx.Done():
						return
					}
					currentResource = nil
					inResource = false
					bracketDepth = 0
				} else {
					// It's an attribute line (including nested braces)
					// We keep the original 'cleanLine' (without trimming) to preserve indentation
					if strings.TrimSpace(cleanLine) != "" {
						currentResource.Attributes = append(currentResource.Attributes, cleanLine)
					}
					bracketDepth += depthChange
				}
			} else {
				inResource = false
			}
			return
		}

		// Generic log line
		if strings.TrimSpace(cleanLine) != "" {
			l := cleanLine
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

	// Flush any pending diagnostic block (stream ended without closing ╵)
	if inDiagnostic && len(diagLines) > 0 {
		diag := parseDiagnosticBlock(diagLines)
		if diag != nil {
			select {
			case m.streamChan <- StreamMsg{Diagnostic: diag}:
			case <-ctx.Done():
			}
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
			// Wrap log lines
			// renderLogLine adds 2 spaces padding/cursor
			// So we wrap at width - 2
			wrapped := wrapText(log, m.width-2, 0)
			for _, w := range wrapped {
				m.lines = append(m.lines, Line{
					Type:    LineTypeLog,
					Content: w,
					AttrIdx: i,
				})
			}
		}
		return
	}

	// Plan view: diagnostics first, then resources
	for i, diag := range m.diagnostics {
		// Wrap summary (accounting for 4 chars prefix: "▸ ✗ ")
		wrappedSummary := wrapText(diag.Summary, m.width-4, 0)
		for wIdx, summaryLine := range wrappedSummary {
			m.lines = append(m.lines, Line{
				Type:        LineTypeDiagnostic,
				DiagIdx:     i,
				ResourceIdx: -1,
				AttrIdx:     wIdx,
				Content:     summaryLine,
			})
		}

		if diag.Expanded {
			for j, detail := range diag.Detail {
				// Wrap diagnostic details (accounting for 4 spaces padding in render)
				wrapped := wrapText(detail.Content, m.width-4, 0)
				for _, w := range wrapped {
					m.lines = append(m.lines, Line{
						Type:        LineTypeDiagnosticDetail,
						DiagIdx:     i,
						ResourceIdx: -1,
						AttrIdx:     j,
						Content:     w,
					})
				}
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
				// Wrap attributes
				// Indentation is preserved in attr string, so we use full width
				// We calculate hanging indent based on the attribute's structure
				indent := getIndentForLine(attr)
				wrapped := wrapText(attr, m.width, indent)

				for _, w := range wrapped {
					m.lines = append(m.lines, Line{
						Type:        LineTypeAttribute,
						ResourceIdx: i,
						DiagIdx:     -1,
						AttrIdx:     j,
						Content:     w,
					})
				}
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
	m.cachedTheme = nil // Invalidate cache so theme() regenerates it
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
		m.width = msg.Width
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
		output.WriteString(m.theme().Dim.Render(fmt.Sprintf("  ↑ %d more lines above\n", startLine)))
	}

	// Content lines
	for i := startLine; i < endLine; i++ {
		output.WriteString(m.renderLine(i))
		output.WriteString("\n")
	}

	// Scroll indicator (bottom)
	if remaining := len(m.lines) - endLine; remaining > 0 {
		output.WriteString(m.theme().Dim.Render(fmt.Sprintf("  ↓ %d more lines below\n", remaining)))
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
	t := m.theme()
	var header string
	if m.inputMode {
		header = t.InputMode.Render("INPUT") + " " + t.Dim.Render("Interactive Mode")
	} else if m.showLogs {
		header = t.HeaderLog.Render("LOGS") + " " + t.Dim.Render("Terraform Output")
	} else {
		header = t.HeaderPlan.Render("PLAN") + " " + t.Dim.Render("Terraform Viewer")
	}

	var status string
	if m.prompt != "" {
		status = t.Warning.Render(" ● WAITING FOR INPUT")
	} else if !m.done {
		status = t.Dim.Render(" ● Live")
	} else {
		status = t.Dim.Render(" ● Done")
	}

	controls := t.Dim.Render(" ↑↓:navigate  q:quit  L:mode  m:toggle colors")
	if m.ptyFile != nil {
		if m.inputMode {
			controls += t.Dim.Render("  Esc:exit input")
		} else {
			controls += t.Dim.Render("  i:enter input")
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
		return m.renderDiagnosticLine(line, isSelected)
	case LineTypeDiagnosticDetail:
		return m.renderDiagnosticDetailLine(line, isSelected)
	case LineTypeResource:
		return m.renderResourceLine(line.ResourceIdx, isSelected)
	case LineTypeAttribute:
		return m.renderAttributeLine(line, isSelected)
	}

	return ""
}

// renderLogLine renders a log line with contextual styling
func (m Model) renderLogLine(content string, isSelected bool) string {
	var style lipgloss.Style
	t := m.theme()

	switch {
	case strings.Contains(content, "Error:"):
		style = t.Error
	case strings.Contains(content, "Warning:"):
		style = t.Warning
	case strings.HasPrefix(content, "Initializing"):
		style = t.Import
	case strings.Contains(content, "Success!"),
		strings.Contains(content, "Creation complete"),
		strings.Contains(content, "Complete!"):
		style = t.Create
	case strings.Contains(content, "Enter a value:"):
		style = t.Forces
	case strings.Contains(content, "Creating..."),
		strings.Contains(content, "Destroying..."),
		strings.Contains(content, "Modifying..."):
		style = t.Update
	default:
		style = t.Default
	}

	if isSelected {
		return t.Selected.Render("► " + content)
	}
	return "  " + style.Render(content)
}

// renderDiagnosticLine renders a diagnostic header line
func (m Model) renderDiagnosticLine(line Line, isSelected bool) string {
	if line.DiagIdx < 0 || line.DiagIdx >= len(m.diagnostics) {
		return ""
	}

	diag := m.diagnostics[line.DiagIdx]
	t := m.theme()
	var style lipgloss.Style
	var symbol string

	if diag.Severity == "error" {
		style = t.Error
		symbol = "✗"
	} else {
		style = t.Warning
		symbol = "⚠"
	}

	expandIcon := "▸"
	if diag.Expanded {
		expandIcon = "▾"
	}

	// Use wrapped content if available
	summaryText := line.Content
	if summaryText == "" {
		summaryText = diag.Summary // Fallback
	}

	// If this is the first line (AttrIdx 0), show symbols and header text
	// If continuation (AttrIdx > 0), show indentation
	var prefix string
	if line.AttrIdx <= 0 {
		prefix = fmt.Sprintf("%s %s ", expandIcon, symbol)

		// Add "Error:" or "Warning:" prefix if it's the first line of summary
		// We re-add it because parser stripped it, but we want to render it with style.
		if m.renderingMode == RenderingModeHighContrast {
			// In HighContrast, the background is colored, so we use plain text for the prefix
			// to avoid Red-on-Red contrast issues (foreground will be dark on red bg).
			if diag.Severity == "error" {
				prefix += "Error: "
			} else if diag.Severity == "warning" {
				prefix += "Warning: "
			}
		} else {
			// In Dashboard, we style the prefix text
			if diag.Severity == "error" {
				prefix += t.Error.Render("Error: ")
			} else if diag.Severity == "warning" {
				prefix += t.Warning.Render("Warning: ")
			}
		}
	} else {
		prefix = "    " // 4 spaces to align with text
	}

	content := prefix + summaryText
	if isSelected {
		return t.Selected.Render("► " + content)
	}
	return "  " + style.Render(content)
}

// renderDiagnosticDetailLine renders a diagnostic detail line
func (m Model) renderDiagnosticDetailLine(line Line, isSelected bool) string {
	if line.DiagIdx < 0 || line.DiagIdx >= len(m.diagnostics) {
		return ""
	}

	diag := m.diagnostics[line.DiagIdx]
	var detail DiagnosticLine
	if line.AttrIdx >= 0 && line.AttrIdx < len(diag.Detail) {
		detail = diag.Detail[line.AttrIdx]
	}

	t := m.theme()
	richLine := line.Content
	cleanLine := stripANSI(richLine)

	var guideStyle lipgloss.Style
	var replacer *strings.Replacer
	if diag.Severity == "error" {
		guideStyle = t.Error
		replacer = t.ErrorReplacer
	} else {
		guideStyle = t.Warning
		replacer = t.WarningReplacer
	}

	// 1. Color structural guides (│, ├, ─, ╵) using an efficient Replacer
	richLine = replacer.Replace(richLine)

	// 2. Color and UNDERLINE marker lines (^ or ~ markers)
	if underlinePattern.MatchString(cleanLine) {
		// Use the dedicated Underline style (Red/Bold/Underlined)
		// We style the markers while preserving indentation
		trimmedMarker := strings.TrimSpace(cleanLine)
		styledMarker := t.Underline.Render(trimmedMarker)
		// Re-apply to the rich content string
		richLine = strings.Replace(richLine, trimmedMarker, styledMarker, 1)
	}

	// 3. Bold location markers ("on file.tf line X:")
	if detail.IsMarker {
		richLine = lipgloss.NewStyle().Bold(true).Render(richLine)
	}

	// 4. Apply mode-specific final wrapping
	if m.renderingMode == RenderingModeHighContrast {
		// In High Contrast, the entire line inherits the severity color
		// We use a style that only sets the foreground to avoid overwriting internal bold/underline
		richLine = lipgloss.NewStyle().Foreground(guideStyle.GetForeground()).Render(richLine)
	}

	if isSelected {
		return t.Selected.Render("►   " + richLine)
	}
	// \x1b[22;24m resets bold (22) and underline (24) without a full reset ([0m).
	// This prevents formatting from leaking to subsequent lines while preserving
	// any color state that lipgloss may have set. We can't use lipgloss for this
	// because it doesn't provide a "reset formatting only" style.
	return "    " + richLine + "\x1b[22;24m"
}

// renderResourceLine renders a resource header line
func (m Model) renderResourceLine(resIdx int, isSelected bool) string {
	if resIdx < 0 || resIdx >= len(m.resources) {
		return ""
	}

	rc := m.resources[resIdx]
	t := m.theme()
	symbol := getSymbol(rc.Action)
	style := m.getStyleForAction(rc.Action)

	expandIcon := "▸"
	if rc.Expanded {
		expandIcon = "▾"
	}

	// Format content based on mode
	var content string
	if m.renderingMode == RenderingModeHighContrast {
		// High Contrast: Color the whole prefix (symbol + address)
		content = style.Render(fmt.Sprintf("%s %s %s", expandIcon, symbol, rc.Address))
	} else {
		// Dashboard: Color only the symbol
		content = fmt.Sprintf("%s %s %s", expandIcon, style.Render(symbol), t.Default.Render(rc.Address))
	}

	if isSelected {
		selBg := t.Selected.GetBackground()
		arrowStyle := lipgloss.NewStyle().Foreground(t.Default.GetForeground()).Background(selBg).Bold(true)

		// For selected state, we need to handle background carefully
		var prefix string
		if m.renderingMode == RenderingModeHighContrast {
			prefix = lipgloss.NewStyle().Foreground(style.GetForeground()).Background(selBg).Bold(true).Render(fmt.Sprintf("%s %s %s", expandIcon, symbol, rc.Address))
		} else {
			// In dashboard mode selected, keep address default color (but on selected bg) and symbol colored
			symStyled := lipgloss.NewStyle().Foreground(style.GetForeground()).Background(selBg).Bold(true).Render(symbol)
			addrStyled := lipgloss.NewStyle().Foreground(t.Default.GetForeground()).Background(selBg).Bold(true).Render(rc.Address)
			prefix = fmt.Sprintf("%s %s %s", expandIcon, symStyled, addrStyled)
		}

		suffixStyle := lipgloss.NewStyle().Foreground(t.Dim.GetForeground()).Background(selBg)
		suffix := suffixStyle.Render(rc.ActionText)

		return fmt.Sprintf("%s%s %s", arrowStyle.Render("► "), prefix, suffix)
	}

	var suffix string
	if m.renderingMode == RenderingModeDashboard {
		// Dashboard: Color the action text (e.g. "will be updated in-place")
		suffix = style.Render(rc.ActionText)
	} else {
		// High Contrast: Dim the action text
		suffix = t.Dim.Render(rc.ActionText)
	}

	return fmt.Sprintf("  %s %s", content, suffix)
}

// renderAttributeLine renders an attribute line with syntax highlighting
func (m Model) renderAttributeLine(line Line, isSelected bool) string {
	content := line.Content
	// Retrieve original full attribute string for context (style determination of wrapped lines)
	original := ""
	if line.ResourceIdx >= 0 && line.ResourceIdx < len(m.resources) {
		if line.AttrIdx >= 0 && line.AttrIdx < len(m.resources[line.ResourceIdx].Attributes) {
			original = m.resources[line.ResourceIdx].Attributes[line.AttrIdx]
		}
	}

	if isSelected {
		t := m.theme()
		selBg := t.Selected.GetBackground()
		style := lipgloss.NewStyle().Background(selBg)

		// For selected state, we want to maintain alignment while showing the cursor
		// Find where the content starts (after leading whitespace)
		trimmed := strings.TrimLeft(content, " ")
		indent := content[:len(content)-len(trimmed)]

		// We use the first two characters of indent for the cursor if possible
		var cursor, rest string
		if len(indent) >= 2 {
			cursor = "► "
			rest = indent[2:]
		} else {
			cursor = "►"
			rest = indent
		}

		cursorStyle := lipgloss.NewStyle().Foreground(t.Default.GetForeground()).Background(selBg).Bold(true)
		return cursorStyle.Render(cursor) + style.Render(rest) + m.styleAttributeMinimal(trimmed, original)
	}

	if m.renderingMode == RenderingModeHighContrast {
		return m.styleAttribute(content, original)
	}

	// Dashboard mode: minimal coloring
	// Apply style only to the prefix/symbol
	return m.styleAttributeMinimal(content, original)
}

// renderPrompt renders the pinned prompt with optional input cursor
func (m Model) renderPrompt() string {
	t := m.theme()
	promptLine := t.Prompt.Render(">> " + m.prompt)
	if m.inputMode {
		promptLine += " " + t.Create.Render(m.userInput) + t.Dim.Render("█")
	}
	return promptLine
}

// renderFooter renders the summary footer
func (m Model) renderFooter() string {
	if m.showLogs {
		return m.theme().Dim.Render(fmt.Sprintf("%d lines", len(m.lines)))
	}
	return m.getSummary(m.resources, m.diagnostics)
}

// styleAttributeMinimal styles an attribute with minimal color (only symbols)
func (m Model) styleAttributeMinimal(attr string, original string) string {
	t := m.theme()
	trimmed := strings.TrimSpace(attr)

	// Special handling for "# forces replacement"
	if idx := strings.Index(attr, "# forces replacement"); idx != -1 {
		before := attr[:idx]
		forces := "# forces replacement"
		after := attr[idx+len(forces):]
		// For the recursive call, we pass original because it's still part of the same line context
		return m.styleAttributeMinimal(before, original) + t.Forces.Render(forces) + t.Default.Render(after)
	}

	var symbol string
	var style lipgloss.Style

	switch {
	case strings.HasPrefix(trimmed, "+"):
		symbol = "+"
		style = t.AddAttr
	case strings.HasPrefix(trimmed, "-"):
		symbol = "-"
		style = t.RemoveAttr
	case strings.HasPrefix(trimmed, "~"):
		symbol = "~"
		style = t.ChangeAttr
	case strings.HasPrefix(trimmed, "#"):
		return t.Dim.Render(attr)
	default:
		// No prefix on this line (wrapped line?)
		// Check original string to see if we are in a changed attribute
		originalTrimmed := strings.TrimSpace(original)
		if strings.HasPrefix(originalTrimmed, "+") || strings.HasPrefix(originalTrimmed, "~") {
			// It's a wrapped part of an addition/update. Should be default color (White).
			return t.Default.Render(attr)
		}
		if strings.HasPrefix(originalTrimmed, "-") {
			// It's a wrapped part of a deletion.
			// Terraform usually colors deletions entirely red? Or standard text red?
			// In minimal mode, we might want Red text for deletions?
			// User said "text should be white... for changes without changes use gray".
			// For deletions, usually everything is red in standard CLI?
			// Let's stick to user request: "On lines with changes the text should be white".
			// Wait, for deletions (-), text usually IS the value being removed.
			// If I use t.RemoveAttr (Red), it matches the symbol.
			return t.RemoveAttr.Render(attr)
		}

		return t.Dim.Render(attr)
	}

	// Reconstruct the string with only symbol colored
	// Find the symbol index in the original string to preserve whitespace
	idx := strings.Index(attr, symbol)
	if idx == -1 {
		return attr // Fallback
	}

	prefix := attr[:idx]
	rawSuffix := attr[idx+len(symbol):]

	// Highlight arrows "->"
	var suffix string
	if strings.Contains(rawSuffix, "->") {
		parts := strings.Split(rawSuffix, "->")
		for i, part := range parts {
			if i > 0 {
				suffix += style.Render("->")
			}
			suffix += t.Default.Render(part)
		}
	} else {
		suffix = t.Default.Render(rawSuffix)
	}

	return prefix + style.Render(symbol) + suffix
}

// styleAttribute applies syntax highlighting to an attribute line
func (m Model) styleAttribute(attr string, original string) string {
	t := m.theme()
	// Special handling for "# forces replacement"
	if idx := strings.Index(attr, "# forces replacement"); idx != -1 {
		before := attr[:idx]
		forces := "# forces replacement"
		after := attr[idx+len(forces):]
		return m.styleAttributePrefix(before, original) + t.Forces.Render(forces) + t.Default.Render(after)
	}
	return m.styleAttributePrefix(attr, original)
}

// styleAttributePrefix styles an attribute based on its prefix (+/-/~/etc)
func (m Model) styleAttributePrefix(attr string, original string) string {
	trimmed := strings.TrimSpace(attr)
	t := m.theme()

	switch {
	case strings.HasPrefix(trimmed, "+"):
		return t.AddAttr.Render(attr)
	case strings.HasPrefix(trimmed, "-"):
		return t.RemoveAttr.Render(attr)
	case strings.HasPrefix(trimmed, "~"):
		return t.ChangeAttr.Render(attr)
	case strings.HasPrefix(trimmed, "#"):
		return t.Dim.Render(attr)
	default:
		// Fallback to original context
		originalTrimmed := strings.TrimSpace(original)
		switch {
		case strings.HasPrefix(originalTrimmed, "+"):
			return t.AddAttr.Render(attr)
		case strings.HasPrefix(originalTrimmed, "-"):
			return t.RemoveAttr.Render(attr)
		case strings.HasPrefix(originalTrimmed, "~"):
			return t.ChangeAttr.Render(attr)
		default:
			return t.Dim.Render(attr)
		}
	}
}

// getSummary generates the summary line showing change counts
func (m Model) getSummary(resources []ResourceChange, diagnostics []Diagnostic) string {
	var parts []string
	t := m.theme()

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
		parts = append(parts, t.Error.Render(fmt.Sprintf("✗%d error", errorCount)))
	}
	if warningCount > 0 {
		parts = append(parts, t.Warning.Render(fmt.Sprintf("⚠%d warning", warningCount)))
	}

	// Count resource changes
	counts := make(map[string]int)
	for _, r := range resources {
		counts[r.Action]++
	}

	if c := counts["create"]; c > 0 {
		parts = append(parts, t.Create.Render(fmt.Sprintf("+%d create", c)))
	}
	if c := counts["update"]; c > 0 {
		parts = append(parts, t.Update.Render(fmt.Sprintf("~%d update", c)))
	}
	if c := counts["destroy"]; c > 0 {
		parts = append(parts, t.Destroy.Render(fmt.Sprintf("-%d destroy", c)))
	}
	if c := counts["replace"]; c > 0 {
		parts = append(parts, t.Replace.Render(fmt.Sprintf("±%d replace", c)))
	}
	if c := counts["import"]; c > 0 {
		parts = append(parts, t.Import.Render(fmt.Sprintf("←%d import", c)))
	}

	if len(parts) == 0 {
		return t.Dim.Render("No changes")
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
func (m Model) getStyleForAction(action string) lipgloss.Style {
	t := m.theme()
	switch action {
	case "create":
		return t.Create
	case "update":
		return t.Update
	case "destroy":
		return t.Destroy
	case "replace":
		return t.Replace
	case "import":
		return t.Import
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

// sanitizeTerraformANSI preserves formatting (bold, underline) but removes colors and resets.
func sanitizeTerraformANSI(s string) string {
	// 1. Strip color codes
	s = ansiColorPattern.ReplaceAllString(s, "")
	// 2. Strip reset codes ([0m). We rely on renderers to handle resets correctly.
	s = ansiResetPattern.ReplaceAllString(s, "")
	return s
}

// parseDiagnosticBlock parses a diagnostic block into a Diagnostic struct
func parseDiagnosticBlock(richLines []string) *Diagnostic {
	if len(richLines) == 0 {
		return nil
	}

	var severity, summary string
	var details []DiagnosticLine

	for i, richLine := range richLines {
		// Clean text for regex matching and empty line detection
		cleanLine := stripANSI(richLine)
		trimmed := strings.TrimSpace(cleanLine)

		if trimmed == "" {
			// Keep empty lines for spacing, but don't parse them as headers
			if severity != "" {
				details = append(details, DiagnosticLine{Content: ""})
			}
			continue
		}

		// Use the line with preserved indentation and rich formatting (bold/underline)
		// We remove one leading space if present, as it's usually the space after '│'
		richLineContent := richLine
		if strings.HasPrefix(richLineContent, " ") {
			richLineContent = richLineContent[1:]
		}

		if match := errorPattern.FindStringSubmatch(trimmed); match != nil {
			if severity == "" {
				severity = "error"
				summary = match[1]
			} else {
				details = append(details, DiagnosticLine{Content: richLineContent, IsMarker: markerPattern.MatchString(trimmed)})
			}
			continue
		}

		if match := warningPattern.FindStringSubmatch(trimmed); match != nil {
			if severity == "" {
				severity = "warning"
				summary = match[1]
			} else {
				details = append(details, DiagnosticLine{Content: richLineContent, IsMarker: markerPattern.MatchString(trimmed)})
			}
			continue
		}

		if severity != "" && i > 0 {
			details = append(details, DiagnosticLine{Content: richLineContent, IsMarker: markerPattern.MatchString(trimmed)})
		}
	}
	// Fallback: if no Error:/Warning: prefix was found, preserve the content
	// as an error diagnostic so no information is lost. Diagnostic blocks (╷...╵)
	// always indicate problems that the user needs to see.
	if severity == "" || summary == "" {
		var nonEmptyLines []string
		for _, rl := range richLines {
			clean := stripANSI(rl)
			trimmed := strings.TrimSpace(clean)
			if trimmed != "" {
				content := rl
				if strings.HasPrefix(content, " ") {
					content = content[1:]
				}
				nonEmptyLines = append(nonEmptyLines, content)
			}
		}
		if len(nonEmptyLines) == 0 {
			return nil
		}
		// Use first non-empty line as summary, rest as details
		summary = stripANSI(nonEmptyLines[0])
		severity = "error"
		details = nil
		for _, line := range nonEmptyLines[1:] {
			clean := stripANSI(line)
			details = append(details, DiagnosticLine{
				Content:  line,
				IsMarker: markerPattern.MatchString(strings.TrimSpace(clean)),
			})
		}
	}

	return &Diagnostic{
		Severity: severity,
		Summary:  summary,
		Detail:   details,
		Expanded: severity == "error",
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
