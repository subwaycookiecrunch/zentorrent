package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type streamModel struct {
	magnet   string
	quitting bool
	width      int
	frame      int // animation frame counter
	isDownload bool
}

func StartStreamTUI(uri string) {
	// Start the actual streaming in the background
	go streamTorrent(uri)

	p := tea.NewProgram(streamModel{magnet: uri, width: 60}, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
	}
}

func StartDownloadTUI(uri string) {
	go downloadTorrent(uri)

	p := tea.NewProgram(streamModel{magnet: uri, width: 60, isDownload: true}, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
	}
}

func (m streamModel) Init() tea.Cmd {
	return tickCmd()
}

func (m streamModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		if m.width > 80 {
			m.width = 80
		}
		if m.width < 40 {
			m.width = 40
		}
	case tickMsg:
		m.frame++
		return m, tickCmd()
	}
	return m, nil
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*200, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m streamModel) View() string {
	if m.quitting {
		return ""
	}

	currentStream.mu.RLock()
	filename := currentStream.Filename
	if filename == "" {
		filename = "Waiting for metadata..."
	} else if len(filename) > 55 {
		filename = filename[:52] + "..."
	}

	filesize := currentStream.FileSizeFmt
	if filesize == "" {
		filesize = "—"
	}

	progress := currentStream.Progress
	speed := currentStream.SpeedFmt
	peers := currentStream.Peers
	eta := currentStream.ETA
	status := currentStream.Status
	buffered := currentStream.Buffered
	res := currentStream.Resolution
	completed := currentStream.Completed
	currentStream.mu.RUnlock()

	var b strings.Builder

	// ── Header ──
	headerStyle := lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	if m.isDownload {
		b.WriteString(headerStyle.Render("  ⬇️  ZenTorrent Download"))
	} else {
		b.WriteString(headerStyle.Render("  ⚡ ZenTorrent Stream"))
	}
	b.WriteString("\n\n")

	// ── File Info ──
	titleStyle := lipgloss.NewStyle().Foreground(colorTextPri).Bold(true)
	resBadge := lipgloss.NewStyle().
		Foreground(colorCyan).
		Bold(true).
		Render(fmt.Sprintf("[%s]", res))

	b.WriteString(fmt.Sprintf("  %s  %s\n", titleStyle.Render(filename), resBadge))

	sizeInfo := lipgloss.NewStyle().Foreground(colorTextDim).
		Render(fmt.Sprintf("  %s / %s", formatSizeBytes(completed), filesize))
	b.WriteString(sizeInfo)
	b.WriteString("\n\n")

	// ── Progress Bar ──
	barWidth := 50
	if m.width > 20 {
		barWidth = m.width - 20
		if barWidth > 60 {
			barWidth = 60
		}
	}

	filled := int(progress / 100 * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}

	// Gradient progress bar: purple → cyan
	var barChars []string
	for i := 0; i < barWidth; i++ {
		if i < filled {
			// Interpolate color from purple to cyan based on position
			ratio := float64(i) / float64(barWidth)
			r := int(124*(1-ratio) + 6*ratio)
			g := int(58*(1-ratio) + 182*ratio)
			bv := int(237*(1-ratio) + 212*ratio)
			color := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, bv))
			barChars = append(barChars, lipgloss.NewStyle().Foreground(color).Render("━"))
		} else {
			barChars = append(barChars, lipgloss.NewStyle().Foreground(colorBorder).Render("━"))
		}
	}

	// Animated cursor at the progress edge
	if filled > 0 && filled < barWidth {
		pulseColors := []lipgloss.Color{"#a78bfa", "#c4b5fd", "#e9d5ff", "#c4b5fd", "#a78bfa"}
		pulseIdx := m.frame % len(pulseColors)
		barChars[filled-1] = lipgloss.NewStyle().Foreground(pulseColors[pulseIdx]).Bold(true).Render("●")
	}

	bar := strings.Join(barChars, "")
	pctStyle := lipgloss.NewStyle().Foreground(colorTextPri).Bold(true)
	b.WriteString(fmt.Sprintf("  %s %s\n\n", bar, pctStyle.Render(fmt.Sprintf("%5.1f%%", progress))))

	// ── Status Badge ──
	var statusBadge string
	switch status {
	case "connecting":
		dots := strings.Repeat(".", (m.frame%3)+1)
		statusBadge = lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("● CONNECTING" + dots)
	case "metadata":
		dots := strings.Repeat(".", (m.frame%3)+1)
		statusBadge = lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("● METADATA" + dots)
	case "buffering":
		spin := []string{"◐", "◓", "◑", "◒"}
		statusBadge = lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render(spin[m.frame%4] + " BUFFERING")
	case "streaming":
		statusBadge = lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render("▶ STREAMING")
	case "complete":
		statusBadge = lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render("✓ COMPLETE")
	default:
		statusBadge = lipgloss.NewStyle().Foreground(colorTextDim).Render("○ " + strings.ToUpper(status))
	}
	b.WriteString(fmt.Sprintf("  %s\n\n", statusBadge))

	// ── Stats Grid ──
	labelStyle := lipgloss.NewStyle().Foreground(colorTextDim)
	valStyle := lipgloss.NewStyle().Foreground(colorTextPri)

	// Speed with visual indicator
	speedBar := ""
	if speed != "" && speed != "0 B/s" {
		// Parse speed roughly for visual
		bars := 1
		if strings.Contains(speed, "MB") {
			bars = 5
		} else if strings.Contains(speed, "KB") {
			bars = 3
		}
		speedBar = lipgloss.NewStyle().Foreground(colorGreen).Render(strings.Repeat("▮", bars) + strings.Repeat("▯", 5-bars))
	}

		b.WriteString(fmt.Sprintf("  %s %s  %s    %s %s\n",
			labelStyle.Render("↓ Speed"),
			valStyle.Render(fmt.Sprintf("%-12s", speed)),
			speedBar,
			labelStyle.Render("👥 Peers"),
			valStyle.Render(fmt.Sprintf("%d", peers)),
		))

		// Buffer bar is only for streaming
		if !m.isDownload {
			bufWidth := 15
			bufFilled := int(buffered / 100 * float64(bufWidth))
			if bufFilled > bufWidth {
				bufFilled = bufWidth
			}
			bufBar := lipgloss.NewStyle().Foreground(colorGreen).Render(strings.Repeat("█", bufFilled)) +
				lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("░", bufWidth-bufFilled))

			b.WriteString(fmt.Sprintf("  %s %s %s    %s %s\n",
				labelStyle.Render("◎ Buffer"),
				bufBar,
				valStyle.Render(fmt.Sprintf("%.0f%%", buffered)),
				labelStyle.Render("⏱  ETA"),
				valStyle.Render(eta),
			))

			// Streaming port info
			b.WriteString("\n")
			portInfo := lipgloss.NewStyle().Foreground(colorTextDim).Italic(true).
				Render(fmt.Sprintf("  Stream: localhost:%d/stream", appConfig.StreamPort))
			b.WriteString(portInfo)
			b.WriteString("\n\n")
		} else {
			// Download ETA
			b.WriteString(fmt.Sprintf("  %s %s\n\n",
				labelStyle.Render("⏱  ETA"),
				valStyle.Render(eta),
			))
		}

		// ── Footer ──
		if m.isDownload {
			b.WriteString(footerStyle.Render("  press q to stop downloading"))
		} else {
			b.WriteString(footerStyle.Render("  press q to stop streaming"))
		}
		b.WriteString("\n")

	// Wrap in a box
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderLit).
		Padding(1, 2)

	return box.Render(b.String())
}

// sparkline generates a mini sparkline from a value (0-100)
func sparkline(val float64) string {
	chars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	idx := int(math.Min(val/100*float64(len(chars)-1), float64(len(chars)-1)))
	if idx < 0 {
		idx = 0
	}
	return string(chars[idx])
}
