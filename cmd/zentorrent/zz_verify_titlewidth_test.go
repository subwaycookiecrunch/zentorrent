package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestVerifyTitleWidthOverflow(t *testing.T) {
	currentStream.mu.Lock()
	currentStream.Filename = strings.Repeat("a", 60) // pure ASCII, > 55
	currentStream.FileSizeFmt = "1.4 GB"
	currentStream.Completed = 700_000_000
	currentStream.Progress = 50
	currentStream.SpeedFmt = "2.3 MB/s"
	currentStream.Peers = 12
	currentStream.ETA = "2m 14s"
	currentStream.Status = "streaming"
	currentStream.Buffered = 45
	currentStream.Resolution = "1080p"
	currentStream.mu.Unlock()

	appConfig.StreamPort = 8080

	name := currentStream.Filename
	fmt.Printf("truncated name cells = %d (len=%d)\n", lipgloss.Width(name), len(name))

	for _, tw := range []int{40, 55, 60, 71, 72, 80, 100, 200} {
		m := streamModel{magnet: "x", width: tw}
		view := m.View()
		maxW, maxLine := 0, ""
		for _, line := range strings.Split(view, "\n") {
			lw := lipgloss.Width(line)
			if lw > maxW {
				maxW, maxLine = lw, line
			}
		}
		if maxW > tw {
			t.Errorf("terminal=%d stream frame overflows by %d (widest line %q)", tw, maxW-tw, maxLine)
		} else {
			t.Logf("terminal=%2d -> frame width=%2d FITS", tw, maxW)
		}
	}
}
