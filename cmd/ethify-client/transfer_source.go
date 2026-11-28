package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aa13244296-ux/ethify/pkg/client"
)

func transferSourceArgument(args []string) string {
	if len(args) == 0 {
		return ""
	}
	source := strings.TrimSpace(args[0])
	if strings.HasPrefix(strings.ToLower(source), "magnet:") {
		return source
	}
	if parsed, err := url.ParseRequestURI(source); err == nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") &&
		strings.EqualFold(filepath.Ext(parsed.Path), ".torrent") {
		return source
	}
	if filepath.IsAbs(source) && strings.EqualFold(filepath.Ext(source), ".torrent") {
		return source
	}
	return ""
}

func forwardTorrentSource(httpAddress, source string) error {
	networks, err := client.ListDownloadNetworks()
	if err != nil {
		return fmt.Errorf("list active adapters: %w", err)
	}
	indices := make([]int, 0, len(networks))
	for _, network := range networks {
		indices = append(indices, network.InterfaceIndex)
	}
	if len(indices) == 0 {
		return fmt.Errorf("no active adapters are available for the torrent")
	}
	payload, err := json.Marshal(struct {
		Source           string `json:"source"`
		InterfaceIndices []int  `json:"interfaceIndices"`
	}{Source: source, InterfaceIndices: indices})
	if err != nil {
		return fmt.Errorf("encode torrent request: %w", err)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://"+httpAddress+"/api/torrents", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create torrent request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Ethify-Request", "1")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("send torrent to Ethify: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close Ethify torrent response: %v\n", err)
		}
	}()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("ethify rejected the torrent (HTTP %d): %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	return nil
}

func forwardTorrentWhenReady(httpAddress, source string) {
	go func() {
		deadline := time.Now().Add(15 * time.Second)
		client := &http.Client{Timeout: 500 * time.Millisecond}
		for time.Now().Before(deadline) {
			response, err := client.Get("http://" + httpAddress + "/api/status")
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					if err := forwardTorrentSource(httpAddress, source); err != nil {
						fmt.Fprintf(os.Stderr, "Ethify could not open torrent: %v\n", err)
					}
					return
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		fmt.Fprintln(os.Stderr, "Ethify did not become ready to open the torrent.")
	}()
}
