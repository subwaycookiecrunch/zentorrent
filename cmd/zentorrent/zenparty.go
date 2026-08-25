package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/subwaycookiecrunch/zentorrent/internal/streamer"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type PartyState struct {
	Pos    float64 `json:"pos"`
	Paused bool    `json:"paused"`
	Magnet string  `json:"magnet"`
}

var (
	isPartyHost   bool
	isPartyJoiner bool
	partyKey      string
)

func GeneratePartyKey() string {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func StartPartyHost(magnet string) {
	fmt.Printf("\n🍿 ZENPARTY HOST MODE 🍿\n")
	fmt.Printf("Party Key: %s\n", partyKey)
	fmt.Printf("Tell your friend to run: zentorrent party join %s\n\n", partyKey)

	go func() {
		go hostSyncRoutine(magnet)

		fmt.Println("Launching stream...")
		go streamTorrent(magnet, nil, nil)

		streamURL := fmt.Sprintf("http://localhost:%d/stream", appConfig.StreamPort)
		for i := 0; i < 30; i++ {
			if streamer.CheckStream(streamURL) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		playerCmd, _ := streamer.LaunchPlayer(streamURL, "", 0)
		if playerCmd != nil {
			playerCmd.Wait()
		}
		ExitApp(0)
	}()

	// Stay alive while the goroutine above runs; it ExitApp(0)s when
	// playback ends. (main() returning would kill it.)
	select {}
}

func StartPartyJoin(key string) {
	partyKey = strings.ToUpper(key)
	fmt.Printf("\n🍿 ZENPARTY JOIN MODE 🍿\n")
	fmt.Printf("Joining Party: %s\n\n", partyKey)

	topicURL := "https://ntfy.sh/zenparty_" + partyKey + "_host/json"
	req, _ := http.NewRequest("GET", topicURL, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Printf("Failed to connect to party: %v\n", err)
		return
	}

	var magnet string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		var ntfyMsg struct {
			Event   string `json:"event"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &ntfyMsg); err == nil && ntfyMsg.Event == "message" {
			var state PartyState
			if err := json.Unmarshal([]byte(ntfyMsg.Message), &state); err == nil {
				if state.Magnet != "" {
					magnet = state.Magnet
					break
				}
			}
		}
	}
	resp.Body.Close()

	if magnet == "" {
		fmt.Println("Failed to retrieve magnet link from host.")
		return
	}

	fmt.Println("Found stream! Buffering...")

	go joinerSyncRoutine()

	go streamTorrent(magnet, nil, nil)
	streamURL := fmt.Sprintf("http://localhost:%d/stream", appConfig.StreamPort)
	for i := 0; i < 30; i++ {
		if streamer.CheckStream(streamURL) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	playerCmd, _ := streamer.LaunchPlayer(streamURL, "", 0)
	if playerCmd != nil {
		playerCmd.Wait()
	}
	ExitApp(0)
}

func hostSyncRoutine(magnet string) {
	topicURL := "https://ntfy.sh/zenparty_" + partyKey + "_host"

	for {
		time.Sleep(2 * time.Second)

		pos, err := streamer.MPVGetTimePos()
		if err != nil {
			continue
		}

		state := PartyState{
			Pos:    pos,
			Magnet: magnet,
			Paused: false,
		}

		b, _ := json.Marshal(state)
		req, _ := http.NewRequest("POST", topicURL, strings.NewReader(string(b)))
		httpClient.Do(req)
	}
}

func joinerSyncRoutine() {
	topicURL := "https://ntfy.sh/zenparty_" + partyKey + "_host/json"

	for {
		req, _ := http.NewRequestWithContext(context.Background(), "GET", topicURL, nil)
		resp, err := httpClient.Do(req)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			var ntfyMsg struct {
				Event   string `json:"event"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &ntfyMsg); err == nil && ntfyMsg.Event == "message" {
				var state PartyState
				if err := json.Unmarshal([]byte(ntfyMsg.Message), &state); err == nil {
					localPos, err := streamer.MPVGetTimePos()
					if err == nil {
						drift := math.Abs(localPos - state.Pos)
						if drift > 4.0 {
							sendIPCCommand([]interface{}{"seek", state.Pos, "absolute+keyframes"})
							fmt.Printf("\n[ZenParty] Synced position with host (drift: %.1fs)\n", drift)
						}
					}
				}
			}
		}
		resp.Body.Close()
		time.Sleep(1 * time.Second)
	}
}

type partyModel struct {
	cursor    int
	mode      string
	textInput textinput.Model
	quitting  bool
	magnet    string
	code      string
	genKey    string
	err       string
}

func StartPartyTUI(initialMagnet string) {
	ti := textinput.New()
	ti.CharLimit = 500
	ti.Width = 50

	mode := "menu"
	var magnet, genKey string
	if initialMagnet != "" {
		mode = "host_wait"
		magnet = initialMagnet
		genKey = GeneratePartyKey()
	}

	pm := partyModel{
		cursor:    0,
		mode:      mode,
		textInput: ti,
		magnet:    magnet,
		genKey:    genKey,
	}

	p := tea.NewProgram(pm)
	m, err := p.Run()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	finalModel := m.(partyModel)
	if finalModel.quitting {
		return
	}

	if finalModel.mode == "host_wait" {
		isPartyHost = true
		partyKey = finalModel.genKey
		go hostSyncRoutine(finalModel.magnet)
		StartStreamTUI(finalModel.magnet, nil, nil)
	} else if finalModel.mode == "join_wait" {
		isPartyJoiner = true
		partyKey = strings.ToUpper(finalModel.code)

		fmt.Println("🔄 Connecting to host...")
		magnet := fetchMagnetFromHost(partyKey)
		if magnet == "" {
			fmt.Println("Failed to retrieve magnet link from host.")
			time.Sleep(3 * time.Second)
			return
		}

		go joinerSyncRoutine()
		StartStreamTUI(magnet, nil, nil)
	}
}

func fetchMagnetFromHost(key string) string {
	topicURL := "https://ntfy.sh/zenparty_" + key + "_host/json"
	req, _ := http.NewRequest("GET", topicURL, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		var ntfyMsg struct {
			Event   string `json:"event"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &ntfyMsg); err == nil && ntfyMsg.Event == "message" {
			var state PartyState
			if err := json.Unmarshal([]byte(ntfyMsg.Message), &state); err == nil {
				if state.Magnet != "" {
					return state.Magnet
				}
			}
		}
	}
	return ""
}

func (m partyModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m partyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}

		switch m.mode {
		case "menu":
			switch key {
			case "q", "esc":
				m.quitting = true
				return m, tea.Quit
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down", "j":
				if m.cursor < 1 {
					m.cursor++
				}
			case "enter":
				if m.cursor == 0 {
					m.mode = "host_input"
					m.textInput.Placeholder = "Paste magnet link to host..."
					m.textInput.SetValue("")
					m.textInput.CharLimit = 500
					m.err = ""
					m.textInput.Focus()
					return m, textinput.Blink
				} else {
					m.mode = "join_input"
					m.textInput.Placeholder = "Enter 6-letter Room Code..."
					m.textInput.SetValue("")
					m.textInput.CharLimit = 6
					m.err = ""
					m.textInput.Focus()
					return m, textinput.Blink
				}
			}
		case "host_input":
			switch key {
			case "esc":
				m.mode = "menu"
				m.textInput.Blur()
			case "enter":
				val := strings.TrimSpace(m.textInput.Value())
				if val != "" {
					m.magnet = val
					m.genKey = GeneratePartyKey()
					m.mode = "host_wait"
				} else {
					m.err = "paste a magnet link first"
				}
			default:
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				return m, cmd
			}
		case "host_wait":
			switch key {
			case "esc":
				m.mode = "menu"
			case "enter":
				return m, tea.Quit
			}
		case "join_input":
			switch key {
			case "esc":
				m.mode = "menu"
				m.textInput.Blur()
			case "enter":
				val := strings.TrimSpace(m.textInput.Value())
				if val != "" {
					m.code = val
					m.mode = "join_wait"
					return m, tea.Quit
				}
				m.err = "enter the 6-letter room code"
			default:
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				return m, cmd
			}
		}
	}
	return m, nil
}

func (m partyModel) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	headerStyle := lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	b.WriteString(fmt.Sprintf("  %s\n\n", headerStyle.Render("🍿 ZenParty")))

	if m.err != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(colorRed).Render("  "+m.err) + "\n\n")
	}

	switch m.mode {
	case "menu":
		options := []string{"Host Party", "Join Party"}
		for i, opt := range options {
			if i == m.cursor {
				b.WriteString(fmt.Sprintf("  %s %s\n", menuCursorStyle.Render("▸"), menuSelectedStyle.Render(opt)))
			} else {
				b.WriteString(fmt.Sprintf("    %s\n", menuItemStyle.Render(opt)))
			}
		}
		b.WriteString("\n" + footerStyle.Render("  ↑/↓ navigate • enter select • esc/q exit"))

	case "host_input":
		b.WriteString("  " + inputLabelStyle.Render("Magnet Link:") + " ")
		b.WriteString(m.textInput.View() + "\n\n")
		b.WriteString(footerStyle.Render("  enter confirm • esc back"))

	case "join_input":
		b.WriteString("  " + inputLabelStyle.Render("Room Code:") + " ")
		b.WriteString(m.textInput.View() + "\n\n")
		b.WriteString(footerStyle.Render("  enter confirm • esc back"))

	case "host_wait":
		b.WriteString("  Room Code created!\n\n")
		codeBox := lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorCyan).
			Padding(1, 4).
			Foreground(colorPurple).
			Bold(true).
			Render(m.genKey)
		b.WriteString("  " + codeBox + "\n\n")
		b.WriteString("  Share this code with your friends.\n")
		b.WriteString("  They should select 'Join Party' and enter it.\n\n")
		b.WriteString(footerStyle.Render("  Press enter to launch stream • esc back"))
	}

	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderLit).
		Padding(0, 1)

	return box.Render(b.String())
}
