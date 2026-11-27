package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadManagerMovePreservesPartialResumeData(t *testing.T) {
	root := t.TempDir()
	manager := NewDownloadManager(root)
	defer manager.Close()

	id := "0123456789abcdef"
	name := "movie.bin"
	oldPath := filepath.Join(root, name)
	partPath := oldPath + "." + id + ".part"
	if err := os.WriteFile(partPath, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	task := &downloadTask{
		id:         id,
		name:       name,
		state:      "paused",
		size:       7,
		partPath:   partPath,
		final:      oldPath,
		target:     "https://example.test/movie.bin",
		resumePath: filepath.Join(manager.resumeDir, id+".json"),
		chunkSize:  7,
		completed:  []bool{true},
	}
	if err := task.persistResumeState(); err != nil {
		t.Fatal(err)
	}
	manager.tasks[id] = task
	manager.reserved[oldPath] = struct{}{}

	destination := filepath.Join(root, "moved")
	snapshot, err := manager.Move(id, destination)
	if err != nil {
		t.Fatalf("move paused download: %v", err)
	}
	expectedPath := filepath.Join(destination, name)
	if snapshot.Location != expectedPath {
		t.Fatalf("location = %q, want %q", snapshot.Location, expectedPath)
	}
	if _, err := os.Stat(expectedPath + "." + id + ".part"); err != nil {
		t.Fatalf("moved partial file is missing: %v", err)
	}
	var record downloadResumeRecord
	if err := readResumeState(task.resumePath, &record); err != nil {
		t.Fatalf("read moved resume record: %v", err)
	}
	if record.TargetPath != expectedPath {
		t.Fatalf("saved target path = %q, want %q", record.TargetPath, expectedPath)
	}

	restarted := NewDownloadManager(root)
	defer restarted.Close()
	restored, ok := restarted.tasks[id]
	if !ok {
		t.Fatal("moved paused download was not restored")
	}
	if got := restored.snapshot().Location; got != expectedPath {
		t.Fatalf("restored location = %q, want %q", got, expectedPath)
	}
}

func TestTorrentManagerMovePausedDataAndPersistLocation(t *testing.T) {
	root := t.TempDir()
	manager := NewTorrentManager(root)
	defer manager.Close()

	id := "torrent-move-test"
	name := "release"
	oldDataPath := filepath.Join(root, name)
	if err := os.MkdirAll(oldDataPath, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(oldDataPath, "file.bin"), []byte("torrent data"), 0644); err != nil {
		t.Fatal(err)
	}
	task := &torrentTask{
		id:         id,
		name:       name,
		state:      "paused",
		source:     "magnet:?xt=urn:btih:0123456789012345678901234567890123456789",
		outputDir:  root,
		resumePath: filepath.Join(manager.resumeDir, id+".json"),
	}
	manager.tasks[id] = task

	destination := filepath.Join(root, "new-location")
	snapshot, err := manager.Move(id, destination)
	if err != nil {
		t.Fatalf("move paused torrent: %v", err)
	}
	expectedPath := filepath.Join(destination, name)
	if snapshot.Location != expectedPath {
		t.Fatalf("location = %q, want %q", snapshot.Location, expectedPath)
	}
	if _, err := os.Stat(filepath.Join(expectedPath, "file.bin")); err != nil {
		t.Fatalf("moved torrent data is missing: %v", err)
	}
	var record torrentResumeRecord
	if err := readResumeState(task.resumePath, &record); err != nil {
		t.Fatalf("read moved torrent resume record: %v", err)
	}
	if record.OutputDir != destination {
		t.Fatalf("saved output directory = %q, want %q", record.OutputDir, destination)
	}

	restarted := NewTorrentManager(root)
	defer restarted.Close()
	restored, ok := restarted.tasks[id]
	if !ok {
		t.Fatal("moved paused torrent was not restored")
	}
	if got := restored.snapshot().Location; got != expectedPath {
		t.Fatalf("restored location = %q, want %q", got, expectedPath)
	}
}

func TestTorrentDataPathRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"../outside", `..\outside`, `C:\outside`, ""} {
		if path, err := torrentDataPath(t.TempDir(), name); err == nil {
			t.Errorf("torrentDataPath(%q) = %q, want an error", name, path)
		}
	}
}
