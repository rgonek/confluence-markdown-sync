package cmd

import "github.com/charmbracelet/lipgloss"

var (
	// successStyle renders a green checkmark line (e.g. "✓ git found").
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))

	// warningStyle renders an amber warning header.
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)

	// errorStyle renders a bold red error description.
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)

	// headingStyle renders a bold section heading.
	headingStyle = lipgloss.NewStyle().Bold(true)
)
