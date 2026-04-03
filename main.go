package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	go StartExtensionServer()

	if len(os.Args) < 2 {
		fmt.Println(`usage: zt "magnet:?xt=..."`)
		fmt.Println("background interception server running on port 9999...")
		select {}
	}

	arg := os.Args[1]
	if arg == "sources" {
		showSources()
	} else if strings.HasPrefix(arg, "magnet:") {
		streamMagnet(arg)
	} else {
		fmt.Println(`usage: zt "magnet:?xt=..."`)
		os.Exit(1)
	}
}
