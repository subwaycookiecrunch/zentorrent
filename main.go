package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: zentorrent <torrent-file|magnet>")
	}

	app, err := NewApp(os.Args[1])
	if err != nil {
		log.Fatalf("setup failed: %v", err)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigs
		app.Stop()
		os.Exit(0)
	}()

	go func() {
		var b [1]byte
		for {
			if n, _ := os.Stdin.Read(b[:]); n > 0 && (b[0] == 'q' || b[0] == 'Q') {
				app.Stop()
				os.Exit(0)
			}
		}
	}()

	app.Run()
}
