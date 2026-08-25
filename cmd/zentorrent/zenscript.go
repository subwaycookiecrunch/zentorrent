package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/subwaycookiecrunch/zentorrent/internal/streamer"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ZsCommand struct {
	Title   string
	Quality string
	Source  string
	Episode string
}

func ParseZenScript(r io.Reader) []ZsCommand {
	var cmds []ZsCommand
	scanner := bufio.NewScanner(r)

	titleRe := regexp.MustCompile(`"([^"]+)"`)
	qualRe := regexp.MustCompile(`quality:([^\s]+)`)
	srcRe := regexp.MustCompile(`source:([^\s]+)`)
	epRe := regexp.MustCompile(`(S\d+E\d+|s\d+e\d+)`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "watch ") {
			continue
		}

		cmd := ZsCommand{}

		if m := titleRe.FindStringSubmatch(line); len(m) > 1 {
			cmd.Title = m[1]
		}
		if m := qualRe.FindStringSubmatch(line); len(m) > 1 {
			cmd.Quality = m[1]
		}
		if m := srcRe.FindStringSubmatch(line); len(m) > 1 {
			cmd.Source = m[1]
		}
		if m := epRe.FindStringSubmatch(line); len(m) > 1 {
			cmd.Episode = m[1]
		}

		if cmd.Title != "" {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func HeadlessResolve(cmd ZsCommand) (*Result, []Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	searchQuery := cmd.Title
	if cmd.Episode != "" {
		searchQuery += " " + cmd.Episode
	}

	var allRes []Result
	resChan := make(chan []Result, len(allSources))

	for _, src := range allSources {
		if cmd.Source != "" && !strings.Contains(src.name, cmd.Source) {
			resChan <- nil
			continue
		}
		go func(s sourceFunc) {
			r, _ := s.fn(ctx, searchQuery)
			resChan <- r
		}(src)
	}

	for i := 0; i < len(allSources); i++ {
		res := <-resChan
		if res != nil {
			allRes = append(allRes, res...)
		}
	}

	if len(allRes) == 0 {
		return nil, nil, fmt.Errorf("no results found")
	}

	var filtered []Result
	for _, r := range allRes {
		if cmd.Quality != "" {
			if !strings.Contains(strings.ToLower(r.Resolution), strings.ToLower(cmd.Quality)) {
				continue
			}
		}
		filtered = append(filtered, r)
	}

	if len(filtered) == 0 {
		return nil, allRes, fmt.Errorf("no results matched criteria")
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Seeders > filtered[j].Seeders
	})

	return &filtered[0], allRes, nil
}

func RunZenScript(file string, dryRun bool) {
	var r io.Reader
	if file == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(file)
		if err != nil {
			fmt.Printf("Error opening script: %v\n", err)
			return
		}
		defer f.Close()
		r = f
	}

	cmds := ParseZenScript(r)
	if len(cmds) == 0 {
		fmt.Println("No valid watch commands found.")
		return
	}

	fmt.Printf("Parsed %d watch commands. Resolving...\n\n", len(cmds))

	for i, cmd := range cmds {
		fmt.Printf("[%d/%d] Resolving: %s %s\n", i+1, len(cmds), cmd.Title, cmd.Episode)
		res, allRes, err := HeadlessResolve(cmd)
		if err != nil {
			fmt.Printf("  -> ❌ Failed to resolve: %v\n", err)
			continue
		}

		fmt.Printf("  -> ✅ Found: %s (Seeders: %d, Source: %s)\n", res.Title, res.Seeders, res.Source)

		if !dryRun {
			fmt.Printf("  -> 🎬 Launching stream...\n")

			var backups []string
			var downgrades []string
			if len(allRes) > 0 {
				backups = findBackups(allRes, res)
				downgrades = findDowngrades(allRes, res)
			}

			streamURL := fmt.Sprintf("http://localhost:%d/stream", appConfig.StreamPort)
			go streamTorrent(res.Magnet, backups, downgrades)

			for i := 0; i < 30; i++ {
				if streamer.CheckStream(streamURL) {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}

			subPath := ""
			playerCmd, err := streamer.LaunchPlayer(streamURL, subPath, 0)
			if err != nil {
				fmt.Printf("  -> ❌ Player error: %v\n", err)
				continue
			}

			err = playerCmd.Wait()
			if err != nil {
				fmt.Printf("  -> Player closed with error: %v\n", err)
			} else {
				fmt.Printf("  -> Player closed cleanly.\n")
			}
			fmt.Println()
		}
	}
}

func ExportWatchlistToZenScript(out string) {
	bmarks := loadBookmarks()
	if len(bmarks) == 0 {
		fmt.Println("Watchlist is empty.")
		return
	}

	f, err := os.Create(out)
	if err != nil {
		fmt.Printf("Error creating file: %v\n", err)
		return
	}
	defer f.Close()

	for _, b := range bmarks {
		f.WriteString(fmt.Sprintf("watch \"%s\"\n", b.Title))
	}
	fmt.Printf("Exported %d items to %s\n", len(bmarks), out)
}
