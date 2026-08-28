package main

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var Version = "4.0.0"

const asciiArt = `  ███████╗███████╗███╗   ██╗
  ╚══███╔╝██╔════╝████╗  ██║
    ███╔╝ █████╗  ██╔██╗ ██║
   ███╔╝  ██╔══╝  ██║╚██╗██║
  ███████╗███████╗██║ ╚████║
  ╚══════╝╚══════╝╚═╝  ╚═══╝`

const subtitle = "  T O R R E N T"

func PrintBanner() {
	gradientStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7c3aed")).
		Bold(true)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#06b6d4")).
		Bold(true)

	versionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#525252")).
		Italic(true)

	borderStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#3f3f46")).
		Padding(0, 2)

	content := gradientStyle.Render(asciiArt) + "\n" +
		subtitleStyle.Render(subtitle) + "  " +
		versionStyle.Render("v"+Version)

	fmt.Println(borderStyle.Render(content))
	fmt.Println()
}

func PrintUsage() {
	cmdStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7c3aed")).
		Bold(true)

	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a1a1aa"))

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#06b6d4")).
		Bold(true).
		PaddingBottom(1)

	fmt.Println(headerStyle.Render("  USAGE"))
	fmt.Println()

	commands := []struct{ cmd, desc string }{
		{"zentorrent", "Launch interactive TUI menu"},
		{"zentorrent stream <magnet>", "Stream a magnet link with live TUI"},
		{"zentorrent search <query>", "Search all sources and stream"},
		{"zentorrent party create <query>", "Host a ZenParty synchronized stream"},
		{"zentorrent party join <room>", "Join a ZenParty stream"},
		{"zentorrent watchonline", "Launch private zero-cost tunnel session"},
		{"zentorrent music [query]", "Launch ZenPlayer retro cassette music player"},
		{"zentorrent sources", "Browse torrent sources"},
		{"zentorrent history", "Show recent streams"},
		{"zentorrent status", "Check background server status"},
		{"zentorrent config", "Show current configuration"},
	}

	for _, c := range commands {
		fmt.Printf("  %s  %s\n",
			cmdStyle.Render(padRight(c.cmd, 32)),
			descStyle.Render(c.desc),
		)
	}

	fmt.Println()
	fmt.Println(descStyle.Render("  Tip: Just run 'zentorrent' for the full interactive experience."))
	fmt.Println()
}
