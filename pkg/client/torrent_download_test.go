package client

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTorrentManagerValidatesSourceAndNetworkCount(t *testing.T) {
	manager := NewTorrentManager(t.TempDir())
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close torrent manager: %v", err)
		}
	})
	if _, err := manager.Start(context.Background(), "file:///private/file.torrent", []int{1, 2}); err == nil {
		t.Fatal("local file URL unexpectedly accepted")
	}
	if _, err := manager.Start(context.Background(), "magnet:?xt=urn:btih:example", []int{1}); err == nil || !strings.Contains(err.Error(), "at least two active networks") {
		t.Fatalf("single-network torrent error = %v, want at-least-two-network error", err)
	}
	if _, err := resolveTorrentNetworks([]int{1, 1}); err == nil || !strings.Contains(err.Error(), "selected more than once") {
		t.Fatalf("duplicate-network error = %v, want duplicate-selection error", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("close torrent manager: %v", err)
	}
	if _, err := manager.Start(context.Background(), "magnet:?xt=urn:btih:example", []int{1, 2}); err == nil || !strings.Contains(err.Error(), "manager is closed") {
		t.Fatalf("closed manager error = %v, want closed-manager error", err)
	}
}

func TestTorrentManagerRestoresPausedTaskAndDiscardsResumeData(t *testing.T) {
	directory := t.TempDir()
	manager := NewTorrentManager(directory)
	id := "resume-test"
	task := &torrentTask{
		id:           id,
		name:         "Saved torrent",
		source:       "magnet:?xt=urn:btih:0123456789012345678901234567890123456789",
		resumePath:   filepath.Join(manager.resumeDir, id+".json"),
		metainfoPath: filepath.Join(manager.resumeDir, id+".torrent"),
	}
	if err := task.persistRecord(); err != nil {
		t.Fatalf("persist torrent resume record: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("close initial manager: %v", err)
	}

	manager = NewTorrentManager(directory)
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close recovered manager: %v", err)
		}
	})
	snapshots := manager.List()
	if len(snapshots) != 1 || snapshots[0].ID != id || snapshots[0].State != "paused" || !snapshots[0].CanResume {
		t.Fatalf("recovered torrents = %+v, want paused resumable torrent", snapshots)
	}
	if err := manager.Discard(id); err != nil {
		t.Fatalf("discard recovered torrent: %v", err)
	}
	if _, err := os.Stat(task.resumePath); !os.IsNotExist(err) {
		t.Fatalf("torrent resume record still exists, stat error = %v", err)
	}
	snapshots = manager.List()
	if snapshots[0].State != "cancelled" || snapshots[0].CanResume {
		t.Fatalf("discarded torrent state = %+v, want cancelled and not resumable", snapshots[0])
	}
}
