package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
)

var progressDescriptionSwitchDelay = 100 * time.Millisecond

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
	desc := m.description
	if m.item != "" {
		display := m.item
		if len(display) > 30 {
			display = "..." + display[len(display)-27:]
		}
		desc = fmt.Sprintf("%s (%s)", desc, display)
	}
	if m.total > 0 {
		pct := float64(m.current) / float64(m.total)
		return fmt.Sprintf("%s %s %d/%d", desc, m.bar.ViewAs(pct), m.current, m.total)
	}
	return desc
}

// consoleProgress drives progressTUIModel and writes output inline to out.
// It uses a mutex for safe concurrent updates from the sync goroutines.
type consoleProgress struct {
	model       progressTUIModel
	out         io.Writer
	description string // kept for direct field access in tests
	mu          sync.Mutex
}

func newConsoleProgress(out io.Writer, description string) *consoleProgress {
	return &consoleProgress{
		model: progressTUIModel{
			bar:         progress.New(progress.WithDefaultGradient()),
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
	_, _ = fmt.Fprintf(p.out, "\r%-80s\r%s", " ", p.model.View())
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
	_, _ = fmt.Fprint(p.out, "\r")
}
