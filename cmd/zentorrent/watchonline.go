package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// GenerateSessionToken creates a short random auth token.
func GenerateSessionToken() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "zen_" + hex.EncodeToString(b)
}

type watchOnlineModel struct {
	tunnelURL  string
	lanURL     string
	localURL   string
	sessionKey string
	quitting   bool
}

func (m watchOnlineModel) Init() tea.Cmd {
	return nil
}

func (m watchOnlineModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "backspace":
			m.quitting = true
			return m, tea.Quit
		case "ctrl+c":
			m.quitting = true
			runShutdown()
			os.Exit(0)
		}
	}
	return m, nil
}

func (m watchOnlineModel) View() string {
	if m.quitting {
		return ""
	}
	return renderWatchOnlineBanner(m.tunnelURL, m.lanURL, m.localURL, m.sessionKey)
}

// StartWatchOnlineSession starts the web server and opens a tunnel if possible.
func StartWatchOnlineSession() {
	startServicesOrDie(true)
	if err := ensureVODServer(); err != nil {
		fmt.Printf("> Failed to start streaming engine: %v\n", err)
		return
	}

	sessionKey := GenerateSessionToken()
	localURL := fmt.Sprintf("http://localhost:%d/?auth=%s", appConfig.StreamPort, sessionKey)

	var lanURL string
	if ips, ok := lanIPs(); ok && len(ips) > 0 {
		lanURL = fmt.Sprintf("http://%s:%d/?auth=%s", ips[0], appConfig.StreamPort, sessionKey)
	}

	tunnelURL := establishZeroCostTunnel(appConfig.StreamPort, sessionKey)

	targetOpenURL := localURL
	if tunnelURL != "" {
		targetOpenURL = tunnelURL
	} else if lanURL != "" {
		targetOpenURL = lanURL
	}

	// Automatically open in default browser
	go func() {
		time.Sleep(350 * time.Millisecond)
		_ = openBrowser(targetOpenURL)
	}()

	m := watchOnlineModel{
		tunnelURL:  tunnelURL,
		lanURL:     lanURL,
		localURL:   localURL,
		sessionKey: sessionKey,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	_, _ = p.Run()
}

func renderWatchOnlineBanner(tunnelURL, lanURL, localURL, sessionKey string) string {
	bannerBorder := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#8b5cf6")).
		Padding(1, 2)

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a78bfa")).
		Bold(true)

	urlHighlight := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#10b981")).
		Bold(true).
		Underline(true)

	secStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#38bdf8")).
		Bold(true)

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#71717a"))

	keyNavStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f59e0b")).
		Bold(true)

	var content strings.Builder
	content.WriteString(titleStyle.Render("🍿 ZENTORRENT CINEMA • PRIVATE WATCH SESSION") + "\n")
	content.WriteString(dimStyle.Render("Private streaming session") + "\n\n")

	if tunnelURL != "" {
		content.WriteString(secStyle.Render("🔒 Private HTTPS Link (Share with friends anywhere):") + "\n")
		content.WriteString("   " + urlHighlight.Render(tunnelURL) + "\n\n")
	} else {
		content.WriteString(secStyle.Render("🔒 Private Link:") + "\n")
		if lanURL != "" {
			content.WriteString("   " + urlHighlight.Render(lanURL) + "\n\n")
		} else {
			content.WriteString("   " + urlHighlight.Render(localURL) + "\n\n")
		}
	}

	if lanURL != "" {
		content.WriteString(dimStyle.Render("🌐 LAN Address:   ") + lanURL + "\n")
	}
	content.WriteString(dimStyle.Render("💻 Local Desktop: ") + localURL + "\n\n")

	content.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#e4e4e7")).Render("Desktop cinema browser opened. Press ESC to go back.") + "\n\n")
	content.WriteString(keyNavStyle.Render("  [ESC / Q]") + dimStyle.Render(" Go Back to Menu    ") + keyNavStyle.Render("[CTRL+C]") + dimStyle.Render(" Stop App"))

	return "\n" + bannerBorder.Render(content.String()) + "\n"
}

// establishZeroCostTunnel tries cloudflare/ssh tunnel providers.
func establishZeroCostTunnel(port int, sessionKey string) string {
	if cfURL := tryCloudflareQuickTunnel(port, sessionKey); cfURL != "" {
		return cfURL
	}
	if sshURL := trySSHReverseTunnel(port, sessionKey); sshURL != "" {
		return sshURL
	}
	return ""
}

func tryCloudflareQuickTunnel(port int, sessionKey string) string {
	cfPath, err := exec.LookPath("cloudflared")
	if err != nil {
		return ""
	}

	cmd := exec.Command(cfPath, "tunnel", "--url", fmt.Sprintf("http://localhost:%d", port))
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ""
	}

	if err := cmd.Start(); err != nil {
		return ""
	}

	urlChan := make(chan string, 1)
	go func() {
		defer cmd.Process.Release()
		scanner := bufio.NewScanner(stderr)
		re := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)
		for scanner.Scan() {
			line := scanner.Text()
			if match := re.FindString(line); match != "" {
				urlChan <- match
				return
			}
		}
		urlChan <- ""
	}()

	select {
	case u := <-urlChan:
		if u != "" {
			return fmt.Sprintf("%s/?auth=%s", u, sessionKey)
		}
	case <-time.After(3 * time.Second):
	}

	return ""
}

func trySSHReverseTunnel(port int, sessionKey string) string {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return ""
	}

	cmd := exec.Command(sshPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "ServerAliveInterval=30",
		"-o", "UserKnownHostsFile=/dev/null",
		"-R", fmt.Sprintf("80:localhost:%d", port),
		"nokey@localhost.run",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ""
	}

	if err := cmd.Start(); err != nil {
		return ""
	}

	urlChan := make(chan string, 1)
	go func() {
		defer cmd.Process.Release()
		scanner := bufio.NewScanner(stdout)
		re := regexp.MustCompile(`https://[a-zA-Z0-9.-]+\.lhr\.life|https://[a-zA-Z0-9.-]+\.localhost\.run`)
		for scanner.Scan() {
			line := scanner.Text()
			if match := re.FindString(line); match != "" {
				urlChan <- match
				return
			}
		}
		urlChan <- ""
	}()

	select {
	case u := <-urlChan:
		if u != "" {
			return fmt.Sprintf("%s/?auth=%s", u, sessionKey)
		}
	case <-time.After(3 * time.Second):
	}

	return ""
}

func openBrowser(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "linux":
		return exec.Command("xdg-open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return nil
	}
}
