package iostreams

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// spinnerFrames contains the animation frames for the CLI spinner.
// Uses Braille pattern characters for smooth animation.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerInterval is the time between spinner frame updates.
const spinnerInterval = 80 * time.Millisecond

// progressIndicator manages a simple CLI spinner for progress indication.
type progressIndicator struct {
	w         io.Writer
	label     string
	done      chan struct{}
	mu        sync.Mutex
	running   bool
	frameIdx  int
	lastLabel string
}

// newProgressIndicator creates a new progress indicator that writes to w.
func newProgressIndicator(w io.Writer, label string) *progressIndicator {
	return &progressIndicator{
		w:     w,
		label: label,
		done:  make(chan struct{}),
	}
}

// start begins the spinner animation in a goroutine.
func (p *progressIndicator) start() {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.done = make(chan struct{})
	p.mu.Unlock()

	go p.run()
}

// run is the main animation loop.
func (p *progressIndicator) run() {
	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()

	// Initial render
	p.render()

	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.mu.Lock()
			p.frameIdx = (p.frameIdx + 1) % len(spinnerFrames)
			p.mu.Unlock()
			p.render()
		}
	}
}

// render draws the current spinner frame.
func (p *progressIndicator) render() {
	p.mu.Lock()
	frame := spinnerFrames[p.frameIdx]
	label := p.label
	p.mu.Unlock()

	// Clear line and render spinner with label
	// \r moves cursor to start of line
	// \033[K clears from cursor to end of line
	if label != "" {
		fmt.Fprintf(p.w, "\r\033[K%s %s", frame, label)
	} else {
		fmt.Fprintf(p.w, "\r\033[K%s", frame)
	}
}

// setLabel updates the spinner label.
func (p *progressIndicator) setLabel(label string) {
	p.mu.Lock()
	p.label = label
	p.mu.Unlock()
}

// stop stops the spinner and clears the line.
func (p *progressIndicator) stop() {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	close(p.done)
	p.mu.Unlock()

	// Clear the spinner line
	fmt.Fprint(p.w, "\r\033[K")
}

// stopWithMessage stops the spinner and shows a final message.
func (p *progressIndicator) stopWithMessage(msg string) {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		// Still print the message
		fmt.Fprintf(p.w, "\r\033[K%s\n", msg)
		return
	}
	p.running = false
	close(p.done)
	p.mu.Unlock()

	// Clear and print final message
	fmt.Fprintf(p.w, "\r\033[K%s\n", msg)
}
