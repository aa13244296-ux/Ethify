package client

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type downloadResumeRecord struct {
	Version      int    `json:"version"`
	ID           string `json:"id"`
	URL          string `json:"url"`
	Name         string `json:"name"`
	TargetPath   string `json:"targetPath,omitempty"`
	Size         int64  `json:"size"`
	ChunkSize    int64  `json:"chunkSize"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Completed    []bool `json:"completed"`
}

func (m *DownloadManager) restore() error {
	entries, err := os.ReadDir(m.resumeDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list saved downloads: %w", err)
	}
	var restoreErrors []error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(m.resumeDir, entry.Name())
		var record downloadResumeRecord
		if err := readResumeState(path, &record); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		if err := m.restoreRecord(path, record); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("%s: %w", entry.Name(), err))
		}
	}
	return errors.Join(restoreErrors...)
}

func (m *DownloadManager) restoreRecord(path string, record downloadResumeRecord) error {
	if record.Version != 1 || !validDownloadID(record.ID) || filepath.Base(path) != record.ID+".json" {
		return errors.New("resume record has an invalid version or ID")
	}
	target, err := url.ParseRequestURI(record.URL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return errors.New("resume record has an invalid download URL")
	}
	name := sanitizeDownloadFilename(record.Name)
	if name != record.Name || record.Size <= 0 || record.ChunkSize <= 0 || record.ChunkSize > maxDownloadChunkSize {
		return errors.New("resume record has invalid file details")
	}
	chunkCount := (record.Size + record.ChunkSize - 1) / record.ChunkSize
	if chunkCount <= 0 || chunkCount > 16<<20 || int64(len(record.Completed)) != chunkCount {
		return errors.New("resume record has an invalid completed-chunk map")
	}
	finalPath := record.TargetPath
	if finalPath == "" {
		finalPath = filepath.Join(m.outputDir, name)
	}
	if !filepath.IsAbs(finalPath) || filepath.Base(filepath.Clean(finalPath)) != name {
		return errors.New("resume record has an invalid destination path")
	}
	finalPath = filepath.Clean(finalPath)
	partPath := finalPath + "." + record.ID + ".part"
	part, err := os.OpenFile(partPath, os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open partial file: %w", err)
	}
	info, err := part.Stat()
	if err != nil {
		_ = part.Close()
		return fmt.Errorf("inspect partial file: %w", err)
	}
	if info.Size() != record.Size {
		_ = part.Close()
		return fmt.Errorf("partial file size is %d, expected %d", info.Size(), record.Size)
	}
	if _, err := os.Stat(finalPath); err == nil {
		_ = part.Close()
		return errors.New("destination file already exists; refusing to overwrite it")
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = part.Close()
		return fmt.Errorf("check destination file: %w", err)
	}

	taskCtx, cancel := context.WithCancel(context.Background())
	task := &downloadTask{
		id:         record.ID,
		name:       name,
		state:      "paused",
		size:       record.Size,
		file:       part,
		partPath:   partPath,
		final:      finalPath,
		target:     target.String(),
		etag:       record.ETag,
		modified:   record.LastModified,
		resumePath: path,
		ctx:        taskCtx,
		cancel:     cancel,
		chunkSize:  record.ChunkSize,
		completed:  append([]bool(nil), record.Completed...),
		lastSample: time.Now(),
	}
	for index, complete := range task.completed {
		if complete {
			bytes := task.chunkLength(int64(index))
			task.downloaded.Add(bytes)
		}
	}
	m.mu.Lock()
	if _, exists := m.reserved[finalPath]; exists {
		m.mu.Unlock()
		_ = part.Close()
		cancel()
		return errors.New("another saved download uses this destination")
	}
	m.reserved[finalPath] = struct{}{}
	m.tasks[record.ID] = task
	m.mu.Unlock()
	return nil
}

func validDownloadID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, value := range id {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return false
		}
	}
	return true
}

func (task *downloadTask) chunkLength(index int64) int64 {
	start := index * task.chunkSize
	if start >= task.size {
		return 0
	}
	return min(task.chunkSize, task.size-start)
}

func (task *downloadTask) persistResumeState() error {
	task.checkpointMu.Lock()
	defer task.checkpointMu.Unlock()
	if task.file != nil {
		if err := task.file.Sync(); err != nil {
			return fmt.Errorf("flush partial download: %w", err)
		}
	}
	task.mu.Lock()
	record := downloadResumeRecord{
		Version:      1,
		ID:           task.id,
		URL:          task.target,
		Name:         task.name,
		TargetPath:   task.final,
		Size:         task.size,
		ChunkSize:    task.chunkSize,
		ETag:         task.etag,
		LastModified: task.modified,
		Completed:    append([]bool(nil), task.completed...),
	}
	task.mu.Unlock()
	if err := writeResumeState(task.resumePath, record); err != nil {
		return fmt.Errorf("save download checkpoint: %w", err)
	}
	task.downloaded.Store(task.completedBytes())
	return nil
}

func (task *downloadTask) completeChunk(index int64) error {
	task.checkpointMu.Lock()
	defer task.checkpointMu.Unlock()
	if err := task.file.Sync(); err != nil {
		return fmt.Errorf("flush completed range: %w", err)
	}
	task.mu.Lock()
	if index < 0 || index >= int64(len(task.completed)) {
		task.mu.Unlock()
		return fmt.Errorf("download range index %d is out of bounds", index)
	}
	task.completed[index] = true
	record := downloadResumeRecord{
		Version:      1,
		ID:           task.id,
		URL:          task.target,
		Name:         task.name,
		TargetPath:   task.final,
		Size:         task.size,
		ChunkSize:    task.chunkSize,
		ETag:         task.etag,
		LastModified: task.modified,
		Completed:    append([]bool(nil), task.completed...),
	}
	task.mu.Unlock()
	if err := writeResumeState(task.resumePath, record); err != nil {
		return fmt.Errorf("save download checkpoint: %w", err)
	}
	return nil
}

func (task *downloadTask) completedBytes() int64 {
	task.mu.Lock()
	defer task.mu.Unlock()
	var total int64
	for index, complete := range task.completed {
		if complete {
			total += task.chunkLength(int64(index))
		}
	}
	return total
}

func (task *downloadTask) removeResumeState() error {
	if err := os.Remove(task.resumePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove download checkpoint: %w", err)
	}
	return nil
}
