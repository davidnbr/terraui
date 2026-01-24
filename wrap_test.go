package main

import (
	"testing"
)

func TestWrapText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		width    int
		indent   int
		expected []string
	}{
		{
			name:     "No wrapping needed",
			text:     "Short line",
			width:    20,
			indent:   0,
			expected: []string{"Short line"},
		},
		{
			name:     "Simple wrap",
			text:     "This is a long line that needs wrapping",
			width:    10,
			indent:   0,
			expected: []string{"This is a ", "long line ", "that needs", " wrapping"},
		},
		{
			name:     "Wrap with indent",
			text:     "    Attribute = \"Long value that wraps\"",
			width:    20,
			indent:   4, // Hanging indent for wrapped lines
			expected: []string{"    Attribute = \"Lon", "    g value that wra", "    ps\""},
		},
		{
			name:     "Exact width",
			text:     "12345",
			width:    5,
			indent:   0,
			expected: []string{"12345"},
		},
		{
			name:     "One char over width",
			text:     "123456",
			width:    5,
			indent:   0,
			expected: []string{"12345", "6"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapText(tt.text, tt.width, tt.indent)
			if len(got) != len(tt.expected) {
				t.Errorf("wrapText() returned %d lines, expected %d", len(got), len(tt.expected))
				t.Errorf("Got: %v", got)
				t.Errorf("Expected: %v", tt.expected)
				return
			}
			for i, line := range got {
				if line != tt.expected[i] {
					t.Errorf("Line %d: got %q, expected %q", i, line, tt.expected[i])
				}
			}
		})
	}
}
