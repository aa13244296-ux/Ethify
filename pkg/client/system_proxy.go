package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const windowsSystemProxyAddress = "127.0.0.1:8080"
const systemProxyStringValueType = 1

type systemProxySettings struct {
	Enabled         bool   `json:"enabled"`
	EnablePresent   bool   `json:"enablePresent"`
	Server          string `json:"server"`
	ServerPresent   bool   `json:"serverPresent"`
	ServerValueType uint32 `json:"serverValueType,omitempty"`
}

type systemProxyBackup struct {
	Settings systemProxySettings `json:"settings"`
}

type systemProxyBackend interface {
	Read() (systemProxySettings, error)
	Apply(systemProxySettings) error
}

type SystemProxySnapshot struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Managed   bool   `json:"managed"`
	Address   string `json:"address"`
	Elevated  bool   `json:"elevated"`
	LastError string `json:"lastError,omitempty"`
	Notice    string `json:"notice,omitempty"`
}

type SystemProxyManager struct {
	mu         sync.Mutex
	backend    systemProxyBackend
	backupPath string
	supported  bool
	lastError  string
	notice     string
}

func newSystemProxyManager(backend systemProxyBackend, supported bool, backupPath string) *SystemProxyManager {
	return &SystemProxyManager{backend: backend, supported: supported, backupPath: backupPath}
}

func (m *SystemProxyManager) Enable() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.supported {
		return errors.New("windows user proxy settings are only available on Windows")
	}
	if m.lastError != "" {
		return fmt.Errorf("resolve the pending Windows proxy restoration first: %s", m.lastError)
	}
	if m.backupPath == "" {
		return errors.New("windows proxy backup path is unavailable")
	}
	if _, err := os.Stat(m.backupPath); err == nil {
		current, readErr := m.backend.Read()
		if readErr != nil {
			return fmt.Errorf("read current Windows proxy settings: %w", readErr)
		}
		if isEthifiSystemProxy(current) {
			return nil
		}
		return errors.New("a Windows proxy restoration is still pending")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check Windows proxy backup: %w", err)
	}

	original, err := m.backend.Read()
	if err != nil {
		return fmt.Errorf("read current Windows proxy settings: %w", err)
	}
	if isEthifiSystemProxy(original) {
		return errors.New("windows already uses Ethifi's proxy address without a saved original setting; leaving it unchanged")
	}
	if err := m.writeBackup(systemProxyBackup{Settings: original}); err != nil {
		return err
	}

	enabled := systemProxySettings{
		Enabled:         true,
		EnablePresent:   true,
		Server:          windowsSystemProxyAddress,
		ServerPresent:   true,
		ServerValueType: systemProxyStringValueType,
	}
	if err := m.backend.Apply(enabled); err != nil {
		restoreErr := m.backend.Apply(original)
		if restoreErr == nil {
			_ = os.Remove(m.backupPath)
			return fmt.Errorf("enable Windows proxy: %w", err)
		}
		m.lastError = errors.Join(err, restoreErr).Error()
		return fmt.Errorf("enable Windows proxy failed and rollback was unsuccessful: %s", m.lastError)
	}
	m.lastError = ""
	m.notice = ""
	return nil
}

func (m *SystemProxyManager) Disable() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.supported {
		return nil
	}
	if m.backupPath == "" {
		return errors.New("windows proxy backup path is unavailable")
	}
	if _, err := os.Stat(m.backupPath); errors.Is(err, os.ErrNotExist) {
		current, readErr := m.backend.Read()
		if readErr != nil {
			return fmt.Errorf("read current Windows proxy settings: %w", readErr)
		}
		if isEthifiSystemProxy(current) {
			return errors.New("ethify does not own this Windows proxy setting and will not overwrite it")
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("check Windows proxy backup: %w", err)
	}
	return m.restoreLocked()
}

func (m *SystemProxyManager) Restore() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.supported || m.backupPath == "" {
		return nil
	}
	if _, err := os.Stat(m.backupPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("check Windows proxy backup: %w", err)
	}
	return m.restoreLocked()
}

func (m *SystemProxyManager) restoreLocked() error {
	data, err := os.ReadFile(m.backupPath)
	if err != nil {
		return fmt.Errorf("read Windows proxy backup: %w", err)
	}
	var backup systemProxyBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		return fmt.Errorf("decode Windows proxy backup: %w", err)
	}
	current, err := m.backend.Read()
	if err != nil {
		return fmt.Errorf("read current Windows proxy settings: %w", err)
	}
	if isEthifiSystemProxy(current) {
		if err := m.backend.Apply(backup.Settings); err != nil {
			return fmt.Errorf("restore original Windows proxy settings: %w", err)
		}
		m.notice = "Restored the Windows proxy settings that were active before Ethifi."
	} else {
		m.notice = "Windows proxy settings changed while Ethifi was running; they were left untouched."
	}
	if err := os.Remove(m.backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Windows proxy backup: %w", err)
	}
	m.lastError = ""
	return nil
}

func (m *SystemProxyManager) Snapshot() SystemProxySnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := SystemProxySnapshot{
		Supported: m.supported,
		Address:   windowsSystemProxyAddress,
		Elevated:  isProcessElevated(),
		LastError: m.lastError,
		Notice:    m.notice,
	}
	if !m.supported || m.backend == nil {
		return snapshot
	}
	if current, err := m.backend.Read(); err != nil {
		snapshot.LastError = err.Error()
	} else {
		snapshot.Enabled = isEthifiSystemProxy(current)
	}
	if m.backupPath != "" {
		if _, err := os.Stat(m.backupPath); err == nil {
			snapshot.Managed = true
		}
	}
	return snapshot
}

func (m *SystemProxyManager) writeBackup(backup systemProxyBackup) error {
	dir := filepath.Dir(m.backupPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create Windows proxy backup directory: %w", err)
	}
	data, err := json.Marshal(backup)
	if err != nil {
		return fmt.Errorf("encode Windows proxy backup: %w", err)
	}
	tempPath := m.backupPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return fmt.Errorf("write Windows proxy backup: %w", err)
	}
	if err := os.Rename(tempPath, m.backupPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("save Windows proxy backup: %w", err)
	}
	return nil
}

func isEthifiSystemProxy(settings systemProxySettings) bool {
	return settings.Enabled && settings.Server == windowsSystemProxyAddress
}
