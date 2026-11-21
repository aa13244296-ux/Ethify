package client

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeSystemProxyBackend struct {
	settings systemProxySettings
	applyErr error
}

func (b *fakeSystemProxyBackend) Read() (systemProxySettings, error) {
	return b.settings, nil
}

func (b *fakeSystemProxyBackend) Apply(settings systemProxySettings) error {
	if b.applyErr != nil {
		err := b.applyErr
		b.applyErr = nil
		return err
	}
	b.settings = settings
	return nil
}

func TestSystemProxyRestoresOriginalSettingsAfterRestart(t *testing.T) {
	original := systemProxySettings{
		Enabled:         true,
		EnablePresent:   true,
		Server:          "proxy.example:3128",
		ServerPresent:   true,
		ServerValueType: systemProxyStringValueType,
	}
	backend := &fakeSystemProxyBackend{settings: original}
	backupPath := filepath.Join(t.TempDir(), "system-proxy-backup.json")

	manager := newSystemProxyManager(backend, true, backupPath)
	if err := manager.Enable(); err != nil {
		t.Fatalf("enable system proxy: %v", err)
	}
	if !isEthifiSystemProxy(backend.settings) {
		t.Fatalf("proxy settings = %+v, want Ethifi endpoint", backend.settings)
	}
	if !manager.Snapshot().Managed {
		t.Fatal("enabled system proxy should have a restoration backup")
	}

	restartedManager := newSystemProxyManager(backend, true, backupPath)
	if err := restartedManager.Restore(); err != nil {
		t.Fatalf("restore after restart: %v", err)
	}
	if backend.settings != original {
		t.Fatalf("restored settings = %+v, want %+v", backend.settings, original)
	}
	if restartedManager.Snapshot().Managed {
		t.Fatal("restoration backup should be removed after restore")
	}
	if _, err := os.Stat(backupPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup still exists after restore: %v", err)
	}
}

func TestSystemProxyDoesNotOverwriteUserChanges(t *testing.T) {
	original := systemProxySettings{EnablePresent: true, ServerPresent: true}
	backend := &fakeSystemProxyBackend{settings: original}
	backupPath := filepath.Join(t.TempDir(), "system-proxy-backup.json")
	manager := newSystemProxyManager(backend, true, backupPath)
	if err := manager.Enable(); err != nil {
		t.Fatalf("enable system proxy: %v", err)
	}

	userSetting := systemProxySettings{
		Enabled:         true,
		EnablePresent:   true,
		Server:          "user-proxy.example:9000",
		ServerPresent:   true,
		ServerValueType: systemProxyStringValueType,
	}
	backend.settings = userSetting
	if err := manager.Restore(); err != nil {
		t.Fatalf("restore after user change: %v", err)
	}
	if backend.settings != userSetting {
		t.Fatalf("user's changed settings were overwritten: %+v", backend.settings)
	}
	if snapshot := manager.Snapshot(); snapshot.Managed || snapshot.Notice == "" {
		t.Fatalf("unexpected state after preserving user's setting: %+v", snapshot)
	}
}

func TestSystemProxyRefusesUnownedLocalSetting(t *testing.T) {
	backend := &fakeSystemProxyBackend{settings: systemProxySettings{
		Enabled:         true,
		EnablePresent:   true,
		Server:          windowsSystemProxyAddress,
		ServerPresent:   true,
		ServerValueType: systemProxyStringValueType,
	}}
	manager := newSystemProxyManager(backend, true, filepath.Join(t.TempDir(), "backup.json"))
	if err := manager.Disable(); err == nil {
		t.Fatal("unowned local proxy setting should not be overwritten")
	}
}

func TestSystemProxyEnableRollsBackWhenApplyingFails(t *testing.T) {
	original := systemProxySettings{EnablePresent: true, ServerPresent: true, Server: "proxy.example:8080"}
	backend := &fakeSystemProxyBackend{settings: original, applyErr: errors.New("registry write denied")}
	manager := newSystemProxyManager(backend, true, filepath.Join(t.TempDir(), "backup.json"))
	if err := manager.Enable(); err == nil {
		t.Fatal("expected registry write failure")
	}
	if _, err := os.Stat(manager.backupPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback should remove its backup: %v", err)
	}
}
