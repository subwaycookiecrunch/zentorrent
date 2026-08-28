package main

import (
	"fmt"
	"github.com/subwaycookiecrunch/zentorrent/internal/config"
	"net"
	"net/http"
	"os"
	"strings"
)

var appConfig config.Config

func main() {
	appConfig = config.Load()
	ApplyTheme(appConfig.Theme)

	if len(os.Args) < 2 {
		startServicesOrDie(true)
		StartMainMenu()
		runShutdown()
		return
	}

	arg := os.Args[1]

	switch {
	case arg == "stream" && len(os.Args) >= 3:
		startServicesOrDie(true)
		magnet := os.Args[2]
		if !strings.HasPrefix(magnet, "magnet:") {
			fmt.Println("Error: Invalid magnet link")
			ExitApp(1)
		}
		StartStreamTUI(magnet, nil, nil)
		runShutdown()

	case arg == "search" && len(os.Args) >= 3:
		startServicesOrDie(true)
		query := strings.Join(os.Args[2:], " ")
		StartSearchTUI(query)
		runShutdown()

	case arg == "music" || arg == "player":
		query := ""
		if len(os.Args) >= 3 {
			query = strings.Join(os.Args[2:], " ")
		}
		if err := LaunchZenPlayer(query); err != nil {
			fmt.Printf("ZenPlayer error: %v\n", err)
			os.Exit(1)
		}

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
		config.Print(appConfig)

	case arg == "server":
		PrintBanner()
		fmt.Printf("Starting background server on port %d...\n", appConfig.ExtPort)
		go StartExtensionServer()
		select {}

	case arg == "help" || arg == "--help" || arg == "-h":
		PrintBanner()
		PrintUsage()

	case arg == "party":
		if len(os.Args) < 3 || os.Args[2] == "--help" || os.Args[2] == "-h" {
			PrintBanner()
			fmt.Println("  ZenParty CLI:")
			fmt.Println("    zentorrent party create <title>    Host a synchronized Watch Party")
			fmt.Println("    zentorrent party join <room_code>  Join an existing Watch Party room")
			fmt.Println()
			return
		}
		startServicesOrDie(true)
		action := os.Args[2]
		if action == "create" {
			if len(os.Args) < 4 {
				fmt.Println("Usage: zentorrent party create <movie/series title>")
				return
			}
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
			if len(os.Args) < 4 {
				fmt.Println("Usage: zentorrent party join <room_code>")
				return
			}
			isPartyJoiner = true
			StartPartyJoin(os.Args[3])
		}

	case arg == "watchonline" || arg == "online" || arg == "watch" || arg == "web":
		StartWatchOnlineSession()

	case arg == "serve":
		startServicesOrDie(true)
		if err := ensureVODServer(); err != nil {
			fmt.Println("> ", err)
			os.Exit(1)
		}
		PrintBanner()
		fmt.Printf("🌐 Web dashboard: http://localhost:%d\n", appConfig.StreamPort)
		if ips, ok := lanIPs(); ok {
			for _, ip := range ips {
				fmt.Printf("                  http://%s:%d\n", ip, appConfig.StreamPort)
			}
		}
		fmt.Println("  Ctrl-C to stop")
		select {}

	case arg == "sync":
		startServicesOrDie(false)
		if services == nil {
			fmt.Println("> services unavailable")
			os.Exit(1)
		}
		services.ForceCatalogSync()
		runShutdown()

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
			startServicesOrDie(true)
			StartStreamTUI(arg, nil, nil)
			runShutdown()
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
	fmt.Printf("config.Config File:       %s\n", config.Path())
}

// startServicesOrDie boots the v4 discovery stack; fatal on failure since
// every caller here is about to enter an interactive session.
func startServicesOrDie(interactive bool) {
	s, err := StartServices(appConfig, interactive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "> failed to initialize discovery services: %v\n", err)
		os.Exit(1)
	}
	_ = s // also reachable via Discovery()/CatalogHandle()
	_ = ensureVODServer()
}

// lanIPs lists non-loopback IPv4 addresses for the web dashboard banner.
func lanIPs() ([]string, bool) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, false
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			out = append(out, ipnet.IP.String())
		}
	}
	return out, len(out) > 0
}
