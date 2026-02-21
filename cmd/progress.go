package cmd

import (
	"fmt"
	"io"

	"github.com/schollz/progressbar/v3"
)

type consoleProgress struct {
	bar *progressbar.ProgressBar
	out io.Writer
}

func newConsoleProgress(out io.Writer, description string) *consoleProgress {
	return &consoleProgress{
		out: out,
		bar: progressbar.NewOptions(-1,
			progressbar.OptionSetDescription(description),
			progressbar.OptionSetWriter(out),
			progressbar.OptionShowCount(),
			progressbar.OptionOnCompletion(func() {
				fmt.Fprint(out, "\n")
			}),
		),
	}
}

func (p *consoleProgress) SetDescription(desc string) {
	p.bar.Describe(desc)
}

func (p *consoleProgress) SetTotal(total int) {
	if total <= 0 {
		return
	}
	p.bar.ChangeMax(total)
}

func (p *consoleProgress) Add(n int) {
	_ = p.bar.Add(n)
}

func (p *consoleProgress) Done() {
	_ = p.bar.Finish()
	fmt.Fprint(p.out, "\n")
}
