//go:build windows

package client

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const internetSettingsRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

type windowsSystemProxyBackend struct{}

func NewSystemProxyManager() *SystemProxyManager {
	backupPath, err := systemProxyBackupPath()
	manager := newSystemProxyManager(windowsSystemProxyBackend{}, true, backupPath)
	if err != nil {
		manager.lastError = err.Error()
		return manager
	}
	if err := manager.Restore(); err != nil {
		manager.lastError = err.Error()
	}
	return manager
}

func (windowsSystemProxyBackend) Read() (systemProxySettings, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsRegistryPath, registry.QUERY_VALUE)
	if err != nil {
		return systemProxySettings{}, fmt.Errorf("open current-user Internet Settings: %w", err)
	}
	defer key.Close()

	var settings systemProxySettings
	enabled, valueType, err := key.GetIntegerValue("ProxyEnable")
	if err == nil {
		if valueType != registry.DWORD {
			return systemProxySettings{}, fmt.Errorf("ProxyEnable has unsupported registry type %d", valueType)
		}
		settings.EnablePresent = true
		settings.Enabled = enabled != 0
	} else if !errors.Is(err, registry.ErrNotExist) {
		return systemProxySettings{}, fmt.Errorf("read ProxyEnable: %w", err)
	}

	server, valueType, err := key.GetStringValue("ProxyServer")
	if err == nil {
		if valueType != registry.SZ {
			return systemProxySettings{}, fmt.Errorf("ProxyServer has unsupported registry type %d", valueType)
		}
		settings.ServerPresent = true
		settings.Server = server
		settings.ServerValueType = valueType
	} else if !errors.Is(err, registry.ErrNotExist) {
		return systemProxySettings{}, fmt.Errorf("read ProxyServer: %w", err)
	}
	return settings, nil
}

func (windowsSystemProxyBackend) Apply(settings systemProxySettings) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, internetSettingsRegistryPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open current-user Internet Settings for update: %w", err)
	}
	defer key.Close()

	if settings.ServerPresent {
		if settings.ServerValueType != 0 && settings.ServerValueType != registry.SZ {
			return fmt.Errorf("cannot safely restore ProxyServer registry type %d", settings.ServerValueType)
		}
		if err := key.SetStringValue("ProxyServer", settings.Server); err != nil {
			return fmt.Errorf("write ProxyServer: %w", err)
		}
	} else if err := deleteRegistryValueIfPresent(key, "ProxyServer"); err != nil {
		return fmt.Errorf("restore ProxyServer: %w", err)
	}

	if settings.EnablePresent {
		value := uint32(0)
		if settings.Enabled {
			value = 1
		}
		if err := key.SetDWordValue("ProxyEnable", value); err != nil {
			return fmt.Errorf("write ProxyEnable: %w", err)
		}
	} else if err := deleteRegistryValueIfPresent(key, "ProxyEnable"); err != nil {
		return fmt.Errorf("restore ProxyEnable: %w", err)
	}

	return notifyWindowsProxyChanged()
}

func deleteRegistryValueIfPresent(key registry.Key, name string) error {
	err := key.DeleteValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func notifyWindowsProxyChanged() error {
	internetSetOption := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	if err := internetSetOption.Find(); err != nil {
		return fmt.Errorf("load WinINet proxy notification API: %w", err)
	}
	for _, option := range []uintptr{39, 37} {
		result, _, callErr := internetSetOption.Call(0, option, 0, 0)
		if result == 0 {
			if callErr == syscall.Errno(0) {
				callErr = syscall.EINVAL
			}
			return fmt.Errorf("notify Windows about proxy settings: %w", callErr)
		}
	}
	return nil
}
