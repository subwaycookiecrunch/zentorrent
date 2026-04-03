package main

import (
	"fmt"
	"strings"
)

type UI struct {
	width int
	drawn bool
}

func NewUI() *UI {
	return &UI{width: 30}
}

func (u *UI) Clear() {
	fmt.Print("\033[H\033[2J")
}

func (u *UI) Render(name string, completed, total, speed int64, peers int) {
	if u.drawn {
		fmt.Print("\033[7A")
	}
	u.drawn = true

	pct := 0.0
	filled := 0
	if total > 0 {
		pct = float64(completed) / float64(total) * 100
		filled = int((float64(completed) / float64(total)) * float64(u.width))
		if filled > u.width {
			filled = u.width
		}
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", u.width-filled)

	eta := "∞"
	if speed > 0 && total > completed {
		eta = fmtETA((total - completed) / speed)
	}

	fmt.Printf("\033[KDownloading: %s\n", trunc(name, 50))
	fmt.Printf("\033[K\n")
	fmt.Printf("\033[K[%s] %.1f%%\n", bar, pct)
	fmt.Printf("\033[K\n")
	fmt.Printf("\033[KSpeed: %s\n", fmtSpeed(speed))
	fmt.Printf("\033[KETA: %s\n", eta)
	fmt.Printf("\033[KPeers: %d\n", peers)
}

func fmtSpeed(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B/s", b)
	}
	if b < 1024*1024 {
		return fmt.Sprintf("%.1f KB/s", float64(b)/1024)
	}
	return fmt.Sprintf("%.2f MB/s", float64(b)/(1024*1024))
}

func fmtETA(s int64) string {
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 3600 {
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %dm", s/3600, (s%3600)/60)
}

func trunc(s string, max int) string {
	rs := []rune(s)
	if len(rs) > max {
		return string(rs[:max-3]) + "..."
	}
	return s
}
