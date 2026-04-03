package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

var sites = []struct {
	cat, name, url string
}{
	{"movies & tv", "YTS", "https://yts.mx"},
	{"movies & tv", "EZTV", "https://eztv.re"},
	{"movies & tv", "1337x", "https://1337x.to"},
	{"movies & tv", "PirateBay", "https://thepiratebay.org/index.html"},
	{"movies & tv", "KAT", "https://kickasstorrents.to"},
	{"movies & tv", "TorrentGalaxy", "https://torrentgalaxy.to"},
	{"movies & tv", "MagnetDL", "https://www.magnetdl.com"},
	{"anime", "NyaaSi", "https://nyaa.si"},
	{"anime", "HorribleSubs", "https://subsplease.org"},
	{"anime", "TokyoTosho", "https://www.tokyotosho.info"},
	{"anime", "AniDex", "https://anidex.info"},
	{"anime", "nekoBT", "https://nekobt.org"},
	{"regional", "Rutor  🇷🇺", "https://rutor.info"},
	{"regional", "Rutracker", "https://rutracker.org"},
	{"regional", "Comando", "https://comando.la"},
	{"regional", "BluDV", "https://bludv.xyz"},
	{"regional", "Torrent9", "https://www.torrent9.ph"},
	{"regional", "ilCorSaRo", "https://ilcorsaronero.info"},
	{"regional", "MejorTorrent", "https://www.mejortorrent.org"},
	{"regional", "Wolfmax4k", "https://wolfmax4k.com"},
	{"regional", "Cinecalidad", "https://cinecalidad.lol"},
	{"regional", "BestTorrents", "https://besttorrents.pl"},
}

func showSources() {
	lastCat := ""
	for i, s := range sites {
		if s.cat != lastCat {
			if lastCat != "" {
				fmt.Println()
			}
			fmt.Println(s.cat)
			lastCat = s.cat
		}
		fmt.Printf("%3d) %s\n", i+1, s.name)
	}

	fmt.Print("\npick: ")
	var pick int
	if _, err := fmt.Scan(&pick); err != nil || pick < 1 || pick > len(sites) {
		fmt.Println("invalid input")
		return
	}

	url := sites[pick-1].url
	var err error
	switch runtime.GOOS {
	case "windows":
		err = exec.Command("cmd", "/c", "start", "", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = exec.Command("xdg-open", url).Start()
	}
	if err != nil {
		fmt.Println("error opening browser:", err)
	}
}
