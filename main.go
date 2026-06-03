package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

var appConfig Config

func main() {
	appConfig = LoadConfig()

	if len(os.Args) < 2 {
		StartMainMenu()
		return
	}

	arg := os.Args[1]

	switch {
	case arg == "stream" && len(os.Args) >= 3:
		magnet := os.Args[2]
		if !strings.HasPrefix(magnet, "magnet:") {
			fmt.Println("Error: Invalid magnet link")
			os.Exit(1)
		}
		StartStreamTUI(magnet)

	case arg == "search" && len(os.Args) >= 3:
		query := strings.Join(os.Args[2:], " ")
		StartSearchTUI(query)

	case arg == "sources":
		fmt.Println("Available sources:")
		for _, s := range allSources {
			fmt.Printf("  • %s\n", s.name)
		}

	case arg == "history":
		StartHistoryTUI()

	case arg == "status":
		checkStatus()

	case arg == "config":
		PrintConfig(appConfig)

	case arg == "server":
		PrintBanner()
		fmt.Printf("Starting background server on port %d...\n", appConfig.ExtPort)
		go StartExtensionServer()
		select {}

	case arg == "help" || arg == "--help" || arg == "-h":
		PrintBanner()
		PrintUsage()

	default:
		if strings.HasPrefix(arg, "magnet:") {
			StartStreamTUI(arg)
		} else {
			PrintBanner()
			fmt.Printf("Unknown command: %s\n\n", arg)
			PrintUsage()
			os.Exit(1)
		}
	}
}

func checkStatus() {
	url := fmt.Sprintf("http://localhost:%d/api/magnet", appConfig.ExtPort)
	req, _ := http.NewRequest("OPTIONS", url, nil)
	resp, err := http.DefaultClient.Do(req)

	fmt.Println("ZenTorrent Status:")
	fmt.Println("------------------")
	if err == nil {
		fmt.Printf("Background Server: RUNNING (Port %d)\n", appConfig.ExtPort)
		resp.Body.Close()
	} else {
		fmt.Printf("Background Server: STOPPED\n")
	}
	fmt.Printf("Streaming Port:    %d\n", appConfig.StreamPort)
	fmt.Printf("Config File:       %s\n", configPath())
}
