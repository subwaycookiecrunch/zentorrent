package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type webhookMsg struct {
	Event    string `json:"event"`
	Filename string `json:"filename"`
	Path     string `json:"path"`
	Time     string `json:"time"`
}

func fireWebhook(event, filename, filePath string) {
	if len(appConfig.Webhooks) == 0 {
		return
	}

	payload := webhookMsg{
		Event:    event,
		Filename: filename,
		Path:     filePath,
		Time:     time.Now().Format(time.RFC3339),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("> Webhook encoding error: %v\n", err)
		return
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	for _, url := range appConfig.Webhooks {
		go func(webhookURL string) {
			req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(data))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "ZenTorrent-Webhook/1.0")

			resp, err := client.Do(req)
			if err != nil {
				fmt.Printf("> Webhook error (%s): %v\n", webhookURL, err)
				return
			}
			defer resp.Body.Close()
			
			if resp.StatusCode >= 400 {
				fmt.Printf("> Webhook error (%s): HTTP %d\n", webhookURL, resp.StatusCode)
			}
		}(url)
	}
}
