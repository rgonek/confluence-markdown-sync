package cmd

import (
	"fmt"
	"io"
	"time"

	"github.com/schollz/progressbar/v3"
)

type consoleProgress struct {
	bar         *progressbar.ProgressBar
	out         io.Writer
	description string
}

func newConsoleProgress(out io.Writer, description string) *consoleProgress {
	return &consoleProgress{
		out:         out,
		description: description,
		bar: progressbar.NewOptions(-1,
			progressbar.OptionSetDescription(description),
			progressbar.OptionSetWriter(out),
			progressbar.OptionShowCount(),
			progressbar.OptionSetPredictTime(false),
			progressbar.OptionSetRenderBlankState(true),
			progressbar.OptionClearOnFinish(),
			progressbar.OptionFullWidth(),
		),
	}
}

func (p *consoleProgress) SetDescription(desc string) {
	// Add a small sleep when switching descriptions to prevent flickering
	// and ensure the user can see the transition
	time.Sleep(100 * time.Millisecond)
	p.description = desc
	p.bar.Describe(desc)
}

func (p *consoleProgress) SetCurrentItem(name string) {
	if name == "" {
		p.bar.Describe(p.description)
	} else {
		// Truncate item for display
		display := name
		if len(display) > 30 {
			display = "..." + display[len(display)-27:]
		}
		p.bar.Describe(fmt.Sprintf("%s (%s)", p.description, display))
	}
}

func (p *consoleProgress) SetTotal(total int) {
	if total < 0 {
		return
	}
	p.bar.Reset()
	p.bar.ChangeMax(total)
}

func (p *consoleProgress) Add(n int) {
	_ = p.bar.Add(n)
}

func (p *consoleProgress) Done() {
	_ = p.bar.Finish()
	fmt.Fprint(p.out, "\r") // Return to start of line
}
