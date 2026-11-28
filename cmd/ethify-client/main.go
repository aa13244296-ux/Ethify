package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aa13244296-ux/ethify/pkg/client"
	"github.com/aa13244296-ux/ethify/pkg/profiles"
)

var (
	version = "1.1.1"
)

func main() {
	attachParentConsole()

	profileFlag := flag.String("profiles", "", "Path to profiles.json")
	httpFlag := flag.String("http", "127.0.0.1:18889", "HTTP address for Web UI Dashboard")
	noBrowserFlag := flag.Bool("no-browser", false, "Do not auto-open browser on startup")
	daemonFlag := flag.Bool("daemon", false, "Run Ethify in the background")
	daemonShortFlag := flag.Bool("d", false, "Run Ethify in the background (alias for -daemon)")
	nativeHostFlag := flag.Bool("native-host", false, "Run the Chromium Native Messaging host")

	// Direct CLI connection flags
	connectFlag := flag.Bool("connect", false, "Directly initiate tunnel from CLI")
	relayFlag := flag.String("relay", "", "Relay server endpoint (host:port)")
	pskFlag := flag.String("psk", "", "Pre-shared key")
	gameFlag := flag.String("game", "", "Target game ID (e.g. valorant, cs2, pubg)")
	regionFlag := flag.String("region", "asia-sg", "Target server region ID (e.g. asia-sg)")
	forceFlag := flag.Bool("force", false, "Force install routes immediately without waiting for game process")
	versionFlag := flag.Bool("version", false, "Print version")

	flag.Parse()
	transferSource := transferSourceArgument(flag.Args())

	if *versionFlag {
		fmt.Printf("Ethify v%s\n", version)
		return
	}
	if *nativeHostFlag {
		if err := runNativeMessagingHost(); err != nil {
			log.Printf("Native Messaging host failed: %v", err)
			os.Exit(1)
		}
		return
	}

	// Detached background daemon mode
	if *daemonFlag || *daemonShortFlag {
		var filteredArgs []string
		for _, arg := range os.Args[1:] {
			if arg != "-daemon" && arg != "--daemon" && arg != "-d" && arg != "--d" {
				filteredArgs = append(filteredArgs, arg)
			}
		}
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("Failed to resolve executable path: %v", err)
		}
		cmd := exec.Command(exe, filteredArgs...)
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			log.Fatalf("Failed to start daemon: %v", err)
		}
		fmt.Printf("Ethify started in background (PID: %d)\n", cmd.Process.Pid)
		return
	}

	// Single instance check: if an instance is already running on httpFlag, open UI and exit
	if !*connectFlag {
		checkClient := &http.Client{Timeout: 400 * time.Millisecond}
		if resp, err := checkClient.Get(fmt.Sprintf("http://%s/api/status", *httpFlag)); err == nil {
			_ = resp.Body.Close()
			log.Printf("[Ethify] Instance already running on http://%s. Opening dashboard...", *httpFlag)
			if transferSource != "" {
				if err := forwardTorrentSource(*httpFlag, transferSource); err != nil {
					log.Printf("[Ethify] Could not open torrent: %v", err)
				}
			}
			if !*noBrowserFlag {
				openDesktopWindow(fmt.Sprintf("http://%s", *httpFlag))
			}
			return
		}
	}

	// Locate profiles.json
	profilePath := *profileFlag
	if profilePath == "" {
		exe, _ := os.Executable()
		candidates := []string{
			filepath.Join(filepath.Dir(exe), "configs", "profiles.json"),
			"configs/profiles.json",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				profilePath = c
				break
			}
		}
	}

	pm, err := profiles.NewManager(profilePath)
	if err != nil {
		log.Printf("[Warning] Failed loading profiles: %v", err)
	} else {
		cat := pm.Catalog()
		modeStr := "custom disk config"
		if profilePath == "" {
			modeStr = "embedded zero-config"
		}
		log.Printf("[Profiles] Loaded %d game profiles & %d configured relays (%s)", len(cat.Games), len(cat.Relays), modeStr)
	}

	engine, err := client.NewEngine(pm)
	if err != nil {
		log.Fatalf("Failed to initialize client engine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	// CLI Direct Mode
	if *connectFlag {
		if *relayFlag == "" || *pskFlag == "" {
			log.Fatalf("Error: -relay and -psk are required when using -connect")
		}
		log.Printf("Connecting via CLI to %s for game %s (%s)...", *relayFlag, *gameFlag, *regionFlag)
		if err := engine.Connect(*relayFlag, []byte(*pskFlag), *gameFlag, *regionFlag, *forceFlag); err != nil {
			log.Fatalf("Connection failed: %v", err)
		}

		go func() {
			sig := <-sigChan
			log.Printf("[Client] Received signal %v, disconnecting...", sig)
			_ = engine.Disconnect()
			cancel()
		}()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats := engine.Stats()
				log.Printf("[Status] %s | Ping: %d ms | Up: %d KB/s | Down: %d KB/s | Routes: %d | GameActive: %v",
					stats.State, stats.PingMs, stats.UpRateBps/1024, stats.DownRateBps/1024, stats.RouteCount, stats.GameRunning)
			}
		}
	}

	// Web Dashboard Mode
	uiServer := client.NewUIServer(engine, pm, "")
	var windowClosed chan struct{}
	if !*noBrowserFlag {
		if shutdownWhenWindowClosed() {
			windowClosed = make(chan struct{})
		}
		go func() {
			time.Sleep(800 * time.Millisecond)
			openDesktopWindow(fmt.Sprintf("http://%s", *httpFlag))
			if windowClosed != nil {
				close(windowClosed)
			}
		}()
	}
	if transferSource != "" {
		forwardTorrentWhenReady(*httpFlag, transferSource)
	}

	go func() {
		if windowClosed == nil {
			sig := <-sigChan
			log.Printf("[Client] Received signal %v, disconnecting...", sig)
		} else {
			select {
			case sig := <-sigChan:
				log.Printf("[Client] Received signal %v, disconnecting...", sig)
			case <-windowClosed:
				log.Printf("[Ethify] Desktop window closed; shutting down.")
			}
		}
		_ = engine.Disconnect()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := uiServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("[Client] Dashboard shutdown failed: %v", err)
		}
		cancel()
	}()

	log.Printf("==================================================")
	log.Printf("  Ethify v%s", version)
	log.Printf("  Dashboard: http://%s", *httpFlag)
	log.Printf("  [Notice] Educational & network research use only.")
	log.Printf("==================================================")

	if err := uiServer.Start(*httpFlag); err != nil {
		log.Fatalf("UI server failed: %v", err)
	}
}
