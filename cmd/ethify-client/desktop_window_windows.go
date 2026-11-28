//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

const (
	wmClose              = 0x0010
	wmDestroy            = 0x0002
	wmApp                = 0x8000
	wmTrayCallback       = wmApp + 1
	wmLButtonUp          = 0x0202
	wmLButtonDoubleClick = 0x0203
	wmRButtonUp          = 0x0205
	wmContextMenu        = 0x007B
	wmNull               = 0x0000
	gwlpWndProc          = ^uintptr(3)
	swHide               = 0
	swRestore            = 9
	nimAdd               = 0
	nimDelete            = 2
	nifMessage           = 1
	nifIcon              = 2
	nifTip               = 4
	imageIcon            = 1
	lrDefaultSize        = 0x0040
	lrShared             = 0x8000
	// rsrc assigns ID 1 to the manifest before assigning the icon group.
	appIconResourceID = 2
	tpmRightButton    = 0x0002
	tpmReturnCommand  = 0x0100
	mfString          = 0x0000
	trayMenuOpen      = 1001
	trayMenuExit      = 1002
)

type point struct {
	x int32
	y int32
}

type notifyIconData struct {
	cbSize           uint32
	window           uintptr
	id               uint32
	flags            uint32
	callbackMessage  uint32
	icon             uintptr
	tip              [128]uint16
	state            uint32
	stateMask        uint32
	info             [256]uint16
	timeoutOrVersion uint32
	infoTitle        [64]uint16
	infoFlags        uint32
	guid             [16]byte
	balloonIcon      uintptr
}

type trayWindow struct {
	window         uintptr
	originalProc   uintptr
	windowProc     uintptr
	webview        webview2.WebView
	icon           uintptr
	iconAdded      bool
	taskbarCreated uint32
	notifyData     notifyIconData
}

var trayAPI = struct {
	sync.Once
	user32, shell32       *windows.LazyDLL
	getWindowLongPtr      *windows.LazyProc
	setWindowLongPtr      *windows.LazyProc
	callWindowProc        *windows.LazyProc
	showWindow            *windows.LazyProc
	setForegroundWindow   *windows.LazyProc
	getCursorPos          *windows.LazyProc
	createPopupMenu       *windows.LazyProc
	appendMenu            *windows.LazyProc
	trackPopupMenu        *windows.LazyProc
	destroyMenu           *windows.LazyProc
	shellNotifyIcon       *windows.LazyProc
	loadImage             *windows.LazyProc
	registerWindowMessage *windows.LazyProc
	postMessage           *windows.LazyProc
}{}

func loadTrayAPI() {
	trayAPI.Do(func() {
		trayAPI.user32 = windows.NewLazySystemDLL("user32.dll")
		trayAPI.shell32 = windows.NewLazySystemDLL("shell32.dll")
		trayAPI.getWindowLongPtr = trayAPI.user32.NewProc("GetWindowLongPtrW")
		trayAPI.setWindowLongPtr = trayAPI.user32.NewProc("SetWindowLongPtrW")
		trayAPI.callWindowProc = trayAPI.user32.NewProc("CallWindowProcW")
		trayAPI.showWindow = trayAPI.user32.NewProc("ShowWindow")
		trayAPI.setForegroundWindow = trayAPI.user32.NewProc("SetForegroundWindow")
		trayAPI.getCursorPos = trayAPI.user32.NewProc("GetCursorPos")
		trayAPI.createPopupMenu = trayAPI.user32.NewProc("CreatePopupMenu")
		trayAPI.appendMenu = trayAPI.user32.NewProc("AppendMenuW")
		trayAPI.trackPopupMenu = trayAPI.user32.NewProc("TrackPopupMenu")
		trayAPI.destroyMenu = trayAPI.user32.NewProc("DestroyMenu")
		trayAPI.shellNotifyIcon = trayAPI.shell32.NewProc("Shell_NotifyIconW")
		trayAPI.loadImage = trayAPI.user32.NewProc("LoadImageW")
		trayAPI.registerWindowMessage = trayAPI.user32.NewProc("RegisterWindowMessageW")
		trayAPI.postMessage = trayAPI.user32.NewProc("PostMessageW")
	})
}

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
			IconId: appIconResourceID,
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
	tray, err := newTrayWindow(window)
	if err != nil {
		showDesktopWindowError("Ethify could not set up its notification-area icon. The window will close normally.\n\n" + err.Error())
		window.Run()
		return
	}
	defer tray.removeIcon()
	window.Run()
}

func newTrayWindow(window webview2.WebView) (*trayWindow, error) {
	loadTrayAPI()
	hwnd := uintptr(window.Window())
	if hwnd == 0 {
		return nil, syscall.EINVAL
	}
	originalProc, _, callErr := trayAPI.getWindowLongPtr.Call(hwnd, gwlpWndProc)
	if originalProc == 0 && callErr != syscall.Errno(0) {
		return nil, callErr
	}

	var module windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &module); err != nil {
		return nil, err
	}
	icon, _, callErr := trayAPI.loadImage.Call(
		uintptr(module), appIconResourceID, imageIcon, 0, 0, lrDefaultSize|lrShared,
	)
	if icon == 0 {
		if callErr != syscall.Errno(0) {
			return nil, callErr
		}
		return nil, syscall.EINVAL
	}

	tray := &trayWindow{
		window:       hwnd,
		originalProc: originalProc,
		webview:      window,
		icon:         icon,
	}
	taskbarCreatedName, _ := windows.UTF16PtrFromString("TaskbarCreated")
	taskbarCreated, _, _ := trayAPI.registerWindowMessage.Call(uintptr(unsafe.Pointer(taskbarCreatedName)))
	tray.taskbarCreated = uint32(taskbarCreated)
	tip, _ := windows.UTF16FromString("Ethify")
	tray.notifyData = notifyIconData{
		cbSize:          uint32(unsafe.Sizeof(notifyIconData{})),
		window:          hwnd,
		id:              1,
		flags:           nifMessage | nifIcon | nifTip,
		callbackMessage: wmTrayCallback,
		icon:            icon,
	}
	copy(tray.notifyData.tip[:], tip)
	tray.windowProc = windows.NewCallback(tray.wndProc)
	previousProc, _, callErr := trayAPI.setWindowLongPtr.Call(hwnd, gwlpWndProc, tray.windowProc)
	if previousProc == 0 && callErr != syscall.Errno(0) {
		return nil, callErr
	}
	return tray, nil
}

func (tray *trayWindow) wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch uint32(message) {
	case wmClose:
		if tray.addIcon() {
			_, _, _ = trayAPI.showWindow.Call(hwnd, swHide)
			return 0
		}
		showDesktopWindowError("Ethify could not minimize to the notification area. The app will close normally.")
	case wmTrayCallback:
		switch uint32(lParam) {
		case wmLButtonUp, wmLButtonDoubleClick:
			tray.showWindow()
			return 0
		case wmRButtonUp, wmContextMenu:
			tray.showContextMenu()
			return 0
		}
	default:
		if tray.taskbarCreated != 0 && uint32(message) == tray.taskbarCreated && tray.iconAdded {
			tray.iconAdded = false
			tray.addIcon()
			return 0
		}
	}
	return tray.callOriginal(hwnd, message, wParam, lParam)
}

func (tray *trayWindow) callOriginal(hwnd, message, wParam, lParam uintptr) uintptr {
	result, _, _ := trayAPI.callWindowProc.Call(
		tray.originalProc, hwnd, message, wParam, lParam,
	)
	return result
}

func (tray *trayWindow) addIcon() bool {
	if tray.iconAdded {
		return true
	}
	result, _, _ := trayAPI.shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&tray.notifyData)))
	tray.iconAdded = result != 0
	return tray.iconAdded
}

func (tray *trayWindow) removeIcon() {
	if !tray.iconAdded {
		return
	}
	callTrayAPI("remove tray icon", trayAPI.shellNotifyIcon, nimDelete, uintptr(unsafe.Pointer(&tray.notifyData)))
	tray.iconAdded = false
}

func (tray *trayWindow) showWindow() {
	tray.removeIcon()
	_, _, _ = trayAPI.showWindow.Call(tray.window, swRestore)
	callTrayAPI("focus tray window", trayAPI.setForegroundWindow, tray.window)
}

func (tray *trayWindow) showContextMenu() {
	menu, _, _ := trayAPI.createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer callTrayAPI("destroy tray menu", trayAPI.destroyMenu, menu)

	openText, _ := windows.UTF16PtrFromString("Open Ethify")
	exitText, _ := windows.UTF16PtrFromString("Exit")
	callTrayAPI("add Open menu item", trayAPI.appendMenu, menu, mfString, trayMenuOpen, uintptr(unsafe.Pointer(openText)))
	callTrayAPI("add Exit menu item", trayAPI.appendMenu, menu, mfString, trayMenuExit, uintptr(unsafe.Pointer(exitText)))

	var cursor point
	if result, _, _ := trayAPI.getCursorPos.Call(uintptr(unsafe.Pointer(&cursor))); result == 0 {
		return
	}
	callTrayAPI("focus tray window", trayAPI.setForegroundWindow, tray.window)
	command, _, _ := trayAPI.trackPopupMenu.Call(
		menu,
		tpmRightButton|tpmReturnCommand,
		uintptr(cursor.x),
		uintptr(cursor.y),
		0,
		tray.window,
		0,
	)
	callTrayAPI("post tray menu dismissal", trayAPI.postMessage, tray.window, wmNull, 0, 0)
	switch command {
	case trayMenuOpen:
		tray.showWindow()
	case trayMenuExit:
		tray.removeIcon()
		tray.callOriginal(tray.window, wmClose, 0, 0)
	}
}

func callTrayAPI(name string, proc *windows.LazyProc, args ...uintptr) uintptr {
	result, _, callErr := proc.Call(args...)
	if result == 0 {
		log.Printf("[Tray] %s failed: %v", name, callErr)
	}
	return result
}

func showDesktopWindowError(message string) {
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("Ethify")
	_, _ = windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
}
