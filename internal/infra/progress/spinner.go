// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package progress

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/stacklok/apiary/internal/domain/progress"
)

// Ensure SpinnerObserver implements progress.Observer.
var _ progress.Observer = (*SpinnerObserver)(nil)

// Braille dot spinner frames.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// SpinnerObserver renders progress as animated terminal spinners.
// Designed for interactive TTY use.
type SpinnerObserver struct {
	out  io.Writer
	mu   sync.Mutex
	stop chan struct{} // nil when no spinner is active
	wg   sync.WaitGroup

	successStyle lipgloss.Style
	warnStyle    lipgloss.Style
	failStyle    lipgloss.Style
	spinStyle    lipgloss.Style
}

// NewSpinnerObserver creates a SpinnerObserver that writes to w.
func NewSpinnerObserver(w io.Writer) *SpinnerObserver {
	return &SpinnerObserver{
		out:          w,
		successStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("2")), // green
		warnStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("3")), // yellow
		failStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("1")), // red
		spinStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("4")), // blue
	}
}

// Start stops any active spinner and begins a new one with msg.
func (s *SpinnerObserver) Start(_ progress.Phase, msg string) {
	s.stopSpinner()

	s.mu.Lock()
	stop := make(chan struct{})
	s.stop = stop
	s.mu.Unlock()

	s.wg.Add(1)
	go s.animate(msg, stop)
}

// Complete stops the spinner and prints a success line.
func (s *SpinnerObserver) Complete(msg string) {
	s.stopSpinner()
	s.printLine(s.successStyle.Render("✓"), msg)
}

// Warn stops the spinner and prints a warning line.
func (s *SpinnerObserver) Warn(msg string) {
	s.stopSpinner()
	s.printLine(s.warnStyle.Render("⚠"), msg)
}

// Fail stops the spinner and prints a failure line.
func (s *SpinnerObserver) Fail(msg string) {
	s.stopSpinner()
	s.printLine(s.failStyle.Render("✗"), msg)
}

func (s *SpinnerObserver) stopSpinner() {
	s.mu.Lock()
	if s.stop != nil {
		close(s.stop)
		s.stop = nil
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *SpinnerObserver) animate(msg string, stop <-chan struct{}) {
	defer s.wg.Done()
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	frame := 0
	for {
		select {
		case <-stop:
			// Clear spinner line before returning.
			s.mu.Lock()
			_, _ = fmt.Fprintf(s.out, "\r\033[K")
			s.mu.Unlock()
			return
		case <-ticker.C:
			s.mu.Lock()
			_, _ = fmt.Fprintf(s.out, "\r\033[K  %s %s",
				s.spinStyle.Render(spinnerFrames[frame%len(spinnerFrames)]),
				msg)
			s.mu.Unlock()
			frame++
		}
	}
}

func (s *SpinnerObserver) printLine(icon, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.out, "  %s %s\n", icon, msg)
}
