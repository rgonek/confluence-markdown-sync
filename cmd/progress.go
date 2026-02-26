package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var progressDescriptionSwitchDelay = 100 * time.Millisecond

const (
	progressBarDefaultWidth = 24
	progressBarMinWidth     = 12
	progressBarMaxWidth     = 48
	progressItemMaxRunes    = 30
	progressTextMaxRunes    = 42
)

// progressSetDescriptionMsg updates the main description line.
type progressSetDescriptionMsg string

// progressSetItemMsg updates the current item shown next to the description.
type progressSetItemMsg string

// progressSetTotalMsg sets the total count and resets current to 0.
type progressSetTotalMsg int

// progressAddMsg increments the current count.
type progressAddMsg int

// progressTUIModel implements the bubbletea Model interface for progress display.
// It uses bubbles/progress for the styled bar and maintains all display state.
type progressTUIModel struct {
	bar         progress.Model
	description string
	item        string
	current     int
	total       int
}

func (m progressTUIModel) Init() tea.Cmd { return nil }

func (m progressTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case progressSetDescriptionMsg:
		m.description = string(msg)
	case progressSetItemMsg:
		m.item = string(msg)
	case progressSetTotalMsg:
		m.total = int(msg)
		m.current = 0
	case progressAddMsg:
		m.current += int(msg)
	}
	return m, nil
}

func (m progressTUIModel) View() string {
	return m.viewWithWidth(0)
}

func (m progressTUIModel) viewWithWidth(lineWidth int) string {
	descMaxRunes := progressTextMaxRunes
	if lineWidth > 0 {
		descBudget := lineWidth - 1
		if m.total > 0 {
			counterWidth := lipgloss.Width(fmt.Sprintf(" (%d/%d)", m.current, m.total))
			descBudget = lineWidth - m.bar.Width - counterWidth - 1
		}
		if descBudget < 0 {
			descBudget = 0
		}
		if descBudget < descMaxRunes {
			descMaxRunes = descBudget
		}
	}

	desc := progressDescriptionText(m.description, m.item, descMaxRunes)
	if m.total > 0 {
		pct := float64(m.current) / float64(m.total)
		if pct < 0 {
			pct = 0
		}
		if pct > 1 {
			pct = 1
		}

		progressPrefix := fmt.Sprintf("%s (%d/%d)", m.bar.ViewAs(pct), m.current, m.total)
		if strings.TrimSpace(desc) == "" {
			return progressPrefix
		}
		return fmt.Sprintf("%s %s", progressPrefix, desc)
	}
	return desc
}

// consoleProgress drives progressTUIModel and writes output inline to out.
// It uses a mutex for safe concurrent updates from the sync goroutines.
type consoleProgress struct {
	model       progressTUIModel
	out         io.Writer
	description string // kept for direct field access in tests
	lastWidth   int
	mu          sync.Mutex
}

func newConsoleProgress(out io.Writer, description string) *consoleProgress {
	return &consoleProgress{
		model: progressTUIModel{
			bar:         progress.New(progress.WithDefaultGradient(), progress.WithWidth(progressBarDefaultWidth)),
			description: description,
		},
		out:         out,
		description: description,
	}
}

// send applies a message to the model and re-renders the current line.
func (p *consoleProgress) send(msg tea.Msg) {
	p.mu.Lock()
	defer p.mu.Unlock()
	newModel, _ := p.model.Update(msg)
	p.model = newModel.(progressTUIModel)

	terminalWidth := outputTerminalWidth(p.out)
	if terminalWidth > 0 {
		p.model.bar.Width = progressBarWidthForTerminal(terminalWidth)
	} else {
		p.model.bar.Width = progressBarDefaultWidth
	}

	rendered := p.model.viewWithWidth(terminalWidth)
	renderedWidth := lipgloss.Width(rendered)
	clearWidth := p.lastWidth
	if renderedWidth > clearWidth {
		clearWidth = renderedWidth
	}

	if clearWidth > 0 {
		_, _ = fmt.Fprintf(p.out, "\r%s\r%s", strings.Repeat(" ", clearWidth), rendered)
	} else {
		_, _ = fmt.Fprintf(p.out, "\r%s", rendered)
	}
	p.lastWidth = renderedWidth
}

func (p *consoleProgress) SetDescription(desc string) {
	if progressDescriptionSwitchDelay > 0 {
		time.Sleep(progressDescriptionSwitchDelay)
	}
	p.description = desc
	p.send(progressSetDescriptionMsg(desc))
	slog.Debug("progress_description", "description", desc)
}

func (p *consoleProgress) SetCurrentItem(name string) {
	p.send(progressSetItemMsg(name))
	slog.Debug("progress_item", "description", p.description, "item", name)
}

func (p *consoleProgress) SetTotal(total int) {
	if total < 0 {
		return
	}
	p.send(progressSetTotalMsg(total))
}

func (p *consoleProgress) Add(n int) {
	p.send(progressAddMsg(n))
}

func (p *consoleProgress) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastWidth > 0 {
		_, _ = fmt.Fprintf(p.out, "\r%s\r", strings.Repeat(" ", p.lastWidth))
		p.lastWidth = 0
		return
	}
	_, _ = fmt.Fprint(p.out, "\r")
}

func progressDescriptionText(description, item string, maxRunes int) string {
	desc := strings.TrimSpace(description)
	item = strings.TrimSpace(item)
	if item != "" {
		truncatedItem := truncateLeftWithEllipsis(item, progressItemMaxRunes)
		if desc == "" {
			desc = truncatedItem
		} else {
			desc = fmt.Sprintf("%s (%s)", desc, truncatedItem)
		}
	}
	return truncateRightWithEllipsis(desc, maxRunes)
}

func truncateRightWithEllipsis(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func truncateLeftWithEllipsis(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes <= 3 {
		return string(runes[len(runes)-maxRunes:])
	}
	return "..." + string(runes[len(runes)-(maxRunes-3):])
}

func progressBarWidthForTerminal(terminalWidth int) int {
	if terminalWidth <= 0 {
		return progressBarDefaultWidth
	}
	width := terminalWidth / 3
	if width < progressBarMinWidth {
		return progressBarMinWidth
	}
	if width > progressBarMaxWidth {
		return progressBarMaxWidth
	}
	return width
}
