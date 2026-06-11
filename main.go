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
	ApplyTheme(appConfig.Theme)

	if err := InitDHTIndex(); err != nil {
		fmt.Printf("Warning: Failed to initialize DHT index: %v\n", err)
	} else {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "DHT indexer crashed: %v\n", r)
				}
			}()
			StartDHTIndexer()
		}()
	}

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
		StartStreamTUI(magnet, nil, nil)

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

	case arg == "party" && len(os.Args) >= 4:
		action := os.Args[2]
		if action == "create" {
			query := strings.Join(os.Args[3:], " ")
			cmd := ZsCommand{Title: query}
			fmt.Printf("Resolving movie for party: %s...\n", query)
			res, _, err := HeadlessResolve(cmd)
			if err != nil {
				fmt.Printf("Failed to resolve movie: %v\n", err)
				return
			}
			isPartyHost = true
			partyKey = GeneratePartyKey()
			StartPartyHost(res.Magnet)
		} else if action == "join" {
			isPartyJoiner = true
			StartPartyJoin(os.Args[3])
		}

	case arg == "export" && len(os.Args) >= 3 && os.Args[2] == "watchlist":
		out := "watchlist.zs"
		if len(os.Args) >= 4 {
			out = os.Args[3]
		}
		ExportWatchlistToZenScript(out)

	case arg == "run" && len(os.Args) >= 3:
		dryRun := false
		file := os.Args[2]
		if file == "--dry-run" {
			dryRun = true
			if len(os.Args) >= 4 {
				file = os.Args[3]
			} else {
				file = "-"
			}
		} else if len(os.Args) >= 4 && os.Args[3] == "--dry-run" {
			dryRun = true
		}
		RunZenScript(file, dryRun)

	default:
		if strings.HasPrefix(arg, "magnet:") {
			StartStreamTUI(arg, nil, nil)
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
