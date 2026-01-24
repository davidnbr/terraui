package main

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

func wrapText(text string, width int, indent int) []string {
	if width <= 0 {
		return []string{text}
	}

	if len(text) == 0 {
		return []string{""}
	}

	var lines []string
	currentLine := ""
	currentWidth := 0

	// Pre-generate indent string
	indentStr := strings.Repeat(" ", indent)

	// First line logic is slightly different (no prepended indent, it's in the text)
	// But actually, the text passed in MIGHT have indentation already.
	// We scan the text character by character.

	runes := []rune(text)

	// We simply iterate and break when visual width exceeds limit
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		rw := runewidth.RuneWidth(r)

		if currentWidth+rw > width {
			// Flush current line
			lines = append(lines, currentLine)

			// Start new line with indent
			currentLine = indentStr
			currentWidth = indent

			// If indent itself is >= width, we are in trouble.
			// But assuming indent < width.

			// If the single character doesn't fit even after indent?
			// (e.g. width=5, indent=4, char width=2).
			// We force it (overflow) or break it?
			// Let's force it for now to avoid infinite loops.
		}

		currentLine += string(r)
		currentWidth += rw
	}

	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}

func getIndentForLine(text string) int {
	indent := 0
	for _, r := range text {
		if r == ' ' {
			indent++
		} else {
			break
		}
	}

	// If the line starts with a change symbol (+, -, ~),
	// standard Terraform hangs indent after the symbol (usually 2 chars: "+ ").
	// So we add 2 to the space indent.
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "~") {
		// Example: "    + attribute" -> 4 spaces. Symbol "+ " is 2. Total 6.
		// Indent should be indent + 2.
		return indent + 2
	}

	return indent
}
