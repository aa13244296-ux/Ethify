//go:build windows

package main

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

func shutdownWhenWindowClosed() bool {
	return true
}

func openDesktopWindow(url string) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	configDir, err := os.UserConfigDir()
	if err != nil {
		showDesktopWindowError("Ethify could not locate its application data folder.")
		return
	}
	dataPath := filepath.Join(configDir, "Ethify", "WebView2")
	if err := os.MkdirAll(dataPath, 0700); err != nil {
		showDesktopWindowError("Ethify could not create its application data folder.")
		return
	}

	window := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "Ethify",
			Width:  1280,
			Height: 900,
			IconId: 1,
			Center: true,
		},
	})
	if window == nil {
		showDesktopWindowError("Ethify could not start its desktop window. Install the Microsoft Edge WebView2 Runtime, then try again.")
		return
	}
	defer window.Destroy()

	window.SetTitle("Ethify")
	window.Navigate(url)
	window.Run()
}

func showDesktopWindowError(message string) {
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("Ethify")
	_, _ = windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
}
