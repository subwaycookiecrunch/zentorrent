package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type streamModel struct {
	magnet     string
	quitting   bool
	forceExit  bool // ctrl+c: quit the whole app once the terminal is restored
	width      int
	frame      int
	isDownload bool
	isPlaylist bool
}

func StartStreamTUI(uri string, backups []string, downgrades []string) {
	go streamTorrent(uri, backups, downgrades)

	p := tea.NewProgram(streamModel{magnet: uri, width: 60}, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
	}
	if sm, ok := final.(streamModel); ok && sm.forceExit {
		// os.Exit inside Update would skip bubbletea's terminal restore and
		// leave the shell in raw mode; exit here instead.
		ExitApp(0)
	}
}

func StartPlaylistStreamTUI() {
	for {
		item := GlobalPlaylist.Advance()
		if item == nil {
			break
		}

		GlobalPlaylist.SetStatus(GlobalPlaylist.Current, "playing")

		currentStream.mu.Lock()
		currentStream.Filename = ""
		currentStream.FileSize = 0
		currentStream.FileSizeFmt = ""
		currentStream.Completed = 0
		currentStream.Progress = 0
		currentStream.Speed = 0
		currentStream.SpeedFmt = ""
		currentStream.Peers = 0
		currentStream.ETA = ""
		currentStream.Status = ""
		currentStream.Buffered = 0
		currentStream.Resolution = ""
		currentStream.SubtitlePath = ""
		currentStream.PlaybackPosSec = 0
		currentStream.mu.Unlock()

		go streamTorrent(item.Magnet, nil, nil)
		p := tea.NewProgram(streamModel{magnet: item.Magnet, width: 60, isPlaylist: true}, tea.WithAltScreen())

		final, err := p.Run()
		if err != nil {
			fmt.Printf("Alas, there's been an error: %v", err)
			break
		}
		if sm, ok := final.(streamModel); ok && sm.forceExit {
			ExitApp(0)
		}

		currentStream.mu.RLock()
		status := currentStream.Status
		currentStream.mu.RUnlock()

		if status != "stopped" {
			break
		}
	}
}

func StartDownloadTUI(uri string, delay time.Duration) {
	go downloadTorrent(uri, delay)

	p := tea.NewProgram(streamModel{magnet: uri, width: 60, isDownload: true}, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
	}
	if sm, ok := final.(streamModel); ok && sm.forceExit {
		ExitApp(0)
	}
}

func (m streamModel) Init() tea.Cmd {
	return tickCmd()
}

func (m streamModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			m.quitting = true
			return m, tea.Quit
		case "ctrl+c":
			m.forceExit = true
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
		currentStream.mu.RLock()
		status := currentStream.Status
		currentStream.mu.RUnlock()
		if m.isPlaylist && status == "stopped" {
			m.quitting = true
			return m, tea.Quit
		}
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
	res := currentStream.Resolution
	if filename == "" {
		filename = "Waiting for metadata..."
	} else {
		// Width-aware and rune-safe: byte slicing garbles Hindi/CJK titles.
		// The title row is "  name  [res]" and the box adds 6 chrome cells.
		badgeCells := 0
		if res != "" {
			badgeCells = lipgloss.Width(res) + 2 + 2 // [res] + gap
		}
		maxCells := 55
		if m.width > 0 {
			if budget := m.width - 10 - badgeCells; budget < maxCells {
				maxCells = budget
			}
		}
		if maxCells < 16 {
			maxCells = 16
		}
		filename = truncateCells(filename, maxCells)
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
	completed := currentStream.Completed
	currentStream.mu.RUnlock()

	var b strings.Builder

	headerStyle := lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	if m.isDownload {
		b.WriteString(headerStyle.Render("  ⬇️  ZenTorrent Download"))
	} else {
		b.WriteString(headerStyle.Render("  ⚡ ZenTorrent Stream"))
	}
	b.WriteString("\n\n")

	titleStyle := lipgloss.NewStyle().Foreground(colorTextPri).Bold(true)
	resBadge := ""
	if res != "" {
		resBadge = lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render("[" + res + "]")
	}

	b.WriteString(fmt.Sprintf("  %s  %s\n", titleStyle.Render(filename), resBadge))

	sizeInfo := lipgloss.NewStyle().Foreground(colorTextDim).
		Render(fmt.Sprintf("  %s / %s", formatSizeBytes(completed), filesize))
	b.WriteString(sizeInfo)
	b.WriteString("\n\n")

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

	rStart, gStart, bStart := parseHexColor(colorPurple)
	rEnd, gEnd, bEnd := parseHexColor(colorCyan)

	var barChars []string
	for i := 0; i < barWidth; i++ {
		if i < filled {
			ratio := float64(i) / float64(barWidth)
			r := int(float64(rStart)*(1-ratio) + float64(rEnd)*ratio)
			g := int(float64(gStart)*(1-ratio) + float64(gEnd)*ratio)
			bv := int(float64(bStart)*(1-ratio) + float64(bEnd)*ratio)
			color := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, bv))
			barChars = append(barChars, lipgloss.NewStyle().Foreground(color).Render("━"))
		} else {
			barChars = append(barChars, lipgloss.NewStyle().Foreground(colorBorder).Render("━"))
		}
	}

	if filled > 0 && filled < barWidth {
		pulseColors := []lipgloss.Color{colorPurple, colorCyan, colorTextPri, colorCyan, colorPurple}
		pulseIdx := m.frame % len(pulseColors)
		barChars[filled-1] = lipgloss.NewStyle().Foreground(pulseColors[pulseIdx]).Bold(true).Render("●")
	}

	bar := strings.Join(barChars, "")
	pctStyle := lipgloss.NewStyle().Foreground(colorTextPri).Bold(true)
	b.WriteString(fmt.Sprintf("  %s %s\n\n", bar, pctStyle.Render(fmt.Sprintf("%5.1f%%", progress))))

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
	case "pre-buffering":
		spin := []string{"◐", "◓", "◑", "◒"}
		statusBadge = lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render(spin[m.frame%4] + " PRE-BUFFERING (launching player soon)")
	case "streaming":
		statusBadge = lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Render("▶ STREAMING")
	case "downloading":
		spin := []string{"◐", "◓", "◑", "◒"}
		statusBadge = lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render(spin[m.frame%4] + " DOWNLOADING")
	case "complete":
		statusBadge = lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render("✓ COMPLETE")
	case "timeout":
		statusBadge = lipgloss.NewStyle().Foreground(colorRed).Bold(true).Render("✕ TIMED OUT — no peers found")
	default:
		statusBadge = lipgloss.NewStyle().Foreground(colorTextDim).Render("○ " + strings.ToUpper(status))
	}
	b.WriteString(fmt.Sprintf("  %s\n\n", statusBadge))

	labelStyle := lipgloss.NewStyle().Foreground(colorTextDim)
	valStyle := lipgloss.NewStyle().Foreground(colorTextPri)

	speedBar := ""
	if speed != "" && speed != "0 B/s" {
		bars := 1
		if strings.Contains(speed, "MB") {
			bars = 5
		} else if strings.Contains(speed, "KB") {
			bars = 3
		}
		speedBar = lipgloss.NewStyle().Foreground(colorGreen).Render(strings.Repeat("▮", bars) + strings.Repeat("▯", 5-bars))
	}

	if m.width > 0 && m.width < 52 {
		// Narrow terminal: drop the decorative mini-bars and stack the stats
		// so nothing clips past the box.
		b.WriteString(fmt.Sprintf("  %s %s\n",
			labelStyle.Render("↓ Speed"), valStyle.Render(speed)))
		b.WriteString(fmt.Sprintf("  %s %s\n",
			labelStyle.Render("👥 Peers"), valStyle.Render(fmt.Sprintf("%d", peers))))
	} else {
		b.WriteString(fmt.Sprintf("  %s %s  %s    %s %s\n",
			labelStyle.Render("↓ Speed"),
			valStyle.Render(fmt.Sprintf("%-12s", speed)),
			speedBar,
			labelStyle.Render("👥 Peers"),
			valStyle.Render(fmt.Sprintf("%d", peers)),
		))
	}

	if !m.isDownload {
		if m.width > 0 && m.width < 52 {
			b.WriteString(fmt.Sprintf("  %s %s%%\n",
				labelStyle.Render("◎ Buffer"), valStyle.Render(fmt.Sprintf("%.0f", buffered))))
			b.WriteString(fmt.Sprintf("  %s %s\n",
				labelStyle.Render("⏱ ETA"), valStyle.Render(eta)))
		} else {
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
		}

		b.WriteString("\n")
		portInfo := lipgloss.NewStyle().Foreground(colorTextDim).Italic(true).
			Render(fmt.Sprintf("  Stream: localhost:%d/stream", appConfig.StreamPort))
		b.WriteString(portInfo)
		b.WriteString("\n\n")
	} else {
		b.WriteString(fmt.Sprintf("  %s %s\n\n",
			labelStyle.Render("⏱  ETA"),
			valStyle.Render(eta),
		))
	}

	if m.isDownload {
		b.WriteString(footerStyle.Render("  press q to stop downloading"))
	} else {
		b.WriteString(footerStyle.Render("  press q to stop streaming"))
	}

	if m.isPlaylist {
		nextItem := GlobalPlaylist.GetNext()
		if nextItem != nil {
			b.WriteString("\n\n")
			statusText := "Queued"
			if nextItem.Status == "prefetching" {
				statusText = lipgloss.NewStyle().Foreground(colorAmber).Bold(true).Render("Prefetching...")
			}
			b.WriteString(fmt.Sprintf("  %s %s (%s)", labelStyle.Render("Up Next:"), valStyle.Render(truncateCells(nextItem.Title, 34)), statusText))
		}
	}

	b.WriteString("\n")

	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderLit).
		Padding(1, 2)

	return box.Render(b.String())
}
