package client

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (m *DownloadManager) Move(id, destination string) (DownloadSnapshot, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return DownloadSnapshot{}, errors.New("choose a destination folder")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return DownloadSnapshot{}, fmt.Errorf("resolve destination folder: %w", err)
	}
	if err := os.MkdirAll(destination, 0755); err != nil {
		return DownloadSnapshot{}, fmt.Errorf("create destination folder: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[id]
	if task == nil {
		return DownloadSnapshot{}, errors.New("download not found")
	}
	task.mu.Lock()
	previousState := task.state
	if previousState != "paused" && previousState != "failed" && previousState != "completed" {
		task.mu.Unlock()
		return DownloadSnapshot{}, fmt.Errorf("download is %s; pause it before moving", previousState)
	}
	oldFinal, oldPart := task.final, task.partPath
	newFinal := filepath.Join(destination, task.name)
	newPart := newFinal + "." + task.id + ".part"
	source, target := oldPart, newPart
	if previousState == "completed" {
		source, target = oldFinal, newFinal
	}
	if filepath.Clean(target) == filepath.Clean(source) {
		task.mu.Unlock()
		return task.snapshot(), nil
	}
	if _, reserved := m.reserved[newFinal]; reserved && filepath.Clean(newFinal) != filepath.Clean(oldFinal) {
		task.mu.Unlock()
		return DownloadSnapshot{}, errors.New("another download already uses that destination")
	}
	for _, path := range []string{newFinal, newPart} {
		if _, err := os.Lstat(path); err == nil {
			task.mu.Unlock()
			return DownloadSnapshot{}, errors.New("destination file already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			task.mu.Unlock()
			return DownloadSnapshot{}, fmt.Errorf("inspect destination file: %w", err)
		}
	}
	task.state = "moving"
	task.mu.Unlock()

	if err := moveFilePath(source, target); err != nil {
		task.mu.Lock()
		task.state = previousState
		task.err = err.Error()
		task.mu.Unlock()
		return DownloadSnapshot{}, fmt.Errorf("move download: %w", err)
	}

	task.mu.Lock()
	task.final = newFinal
	task.partPath = newPart
	task.state = previousState
	task.err = ""
	task.mu.Unlock()
	delete(m.reserved, oldFinal)
	m.reserved[newFinal] = struct{}{}
	if previousState != "completed" {
		if err := task.persistResumeState(); err != nil {
			rollbackErr := moveFilePath(target, source)
			task.mu.Lock()
			task.state = previousState
			if rollbackErr == nil {
				task.final = oldFinal
				task.partPath = oldPart
				task.err = err.Error()
			} else {
				task.final = newFinal
				task.partPath = newPart
				task.err = errors.Join(err, rollbackErr).Error()
			}
			task.mu.Unlock()
			if rollbackErr == nil {
				delete(m.reserved, newFinal)
				m.reserved[oldFinal] = struct{}{}
			}
			return DownloadSnapshot{}, errors.Join(fmt.Errorf("save moved download location: %w", err), rollbackErr)
		}
	}
	return task.snapshot(), nil
}

func (m *DownloadManager) Open(id string) error {
	m.mu.RLock()
	task := m.tasks[id]
	m.mu.RUnlock()
	if task == nil {
		return errors.New("download not found")
	}
	task.mu.Lock()
	if task.state != "completed" {
		state := task.state
		task.mu.Unlock()
		return fmt.Errorf("download is %s and is not ready to open", state)
	}
	path := task.final
	task.mu.Unlock()
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("inspect downloaded file: %w", err)
	}
	return openTransferPath(path, false)
}

func (m *DownloadManager) OpenFolder(id string) error {
	m.mu.RLock()
	task := m.tasks[id]
	m.mu.RUnlock()
	if task == nil {
		return errors.New("download not found")
	}
	task.mu.Lock()
	folder := filepath.Dir(task.final)
	task.mu.Unlock()
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		if err != nil {
			return fmt.Errorf("inspect download folder: %w", err)
		}
		return errors.New("download folder does not exist")
	}
	return openTransferPath(folder, true)
}

func (m *TorrentManager) Move(id, destination string) (TorrentSnapshot, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return TorrentSnapshot{}, errors.New("choose a destination folder")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return TorrentSnapshot{}, fmt.Errorf("resolve destination folder: %w", err)
	}
	if err := os.MkdirAll(destination, 0755); err != nil {
		return TorrentSnapshot{}, fmt.Errorf("create destination folder: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[id]
	if task == nil {
		return TorrentSnapshot{}, errors.New("torrent not found")
	}
	task.mu.Lock()
	previousState := task.state
	if previousState != "paused" && previousState != "failed" && previousState != "completed" && previousState != "cancelled" {
		task.mu.Unlock()
		return TorrentSnapshot{}, fmt.Errorf("torrent is %s; pause it before moving", previousState)
	}
	oldDir := task.outputDir
	oldDataPath, err := torrentDataPath(oldDir, task.name)
	if err != nil {
		task.mu.Unlock()
		return TorrentSnapshot{}, err
	}
	newDataPath, err := torrentDataPath(destination, task.name)
	if err != nil {
		task.mu.Unlock()
		return TorrentSnapshot{}, err
	}
	if filepath.Clean(oldDataPath) == filepath.Clean(newDataPath) {
		task.mu.Unlock()
		return task.snapshot(), nil
	}
	task.state = "moving"
	task.mu.Unlock()

	var moveErr error
	if info, statErr := os.Lstat(oldDataPath); statErr == nil {
		if info.IsDir() {
			moveErr = moveDirectoryPath(oldDataPath, newDataPath)
		} else if info.Mode().IsRegular() {
			moveErr = moveFilePath(oldDataPath, newDataPath)
		} else {
			moveErr = errors.New("torrent data path is not a regular file or folder")
		}
	} else if errors.Is(statErr, os.ErrNotExist) {
		if _, targetErr := os.Lstat(newDataPath); targetErr == nil {
			moveErr = errors.New("destination already contains a torrent with this name")
		} else if !errors.Is(targetErr, os.ErrNotExist) {
			moveErr = fmt.Errorf("inspect destination torrent folder: %w", targetErr)
		}
	} else {
		moveErr = fmt.Errorf("inspect torrent data folder: %w", statErr)
	}
	if moveErr != nil {
		task.mu.Lock()
		task.state = previousState
		task.err = moveErr.Error()
		task.mu.Unlock()
		return TorrentSnapshot{}, fmt.Errorf("move torrent data: %w", moveErr)
	}

	task.mu.Lock()
	task.outputDir = destination
	task.state = previousState
	task.err = ""
	task.mu.Unlock()
	if previousState != "completed" && previousState != "cancelled" {
		if err := task.persistRecord(); err != nil {
			var rollbackErr error
			if info, statErr := os.Lstat(newDataPath); statErr == nil {
				if info.IsDir() {
					rollbackErr = moveDirectoryPath(newDataPath, oldDataPath)
				} else if info.Mode().IsRegular() {
					rollbackErr = moveFilePath(newDataPath, oldDataPath)
				}
			}
			task.mu.Lock()
			task.state = previousState
			if rollbackErr == nil {
				task.outputDir = oldDir
				task.err = err.Error()
			} else {
				task.outputDir = destination
				task.err = errors.Join(err, rollbackErr).Error()
			}
			task.mu.Unlock()
			return TorrentSnapshot{}, errors.Join(fmt.Errorf("save moved torrent location: %w", err), rollbackErr)
		}
	}
	return task.snapshot(), nil
}

func (m *TorrentManager) Open(id string) error {
	m.mu.RLock()
	task := m.tasks[id]
	m.mu.RUnlock()
	if task == nil {
		return errors.New("torrent not found")
	}
	task.mu.Lock()
	path, pathErr := torrentDataPath(task.outputDir, task.name)
	task.mu.Unlock()
	if pathErr != nil {
		return pathErr
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect torrent data: %w", err)
	}
	if !info.IsDir() {
		return openTransferPath(path, false)
	}
	var firstFile string
	err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if fileInfo.Mode().IsRegular() {
			firstFile = current
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("find a downloaded torrent file: %w", err)
	}
	if firstFile == "" {
		return errors.New("torrent has no downloaded files to open")
	}
	return openTransferPath(firstFile, false)
}

func (m *TorrentManager) OpenFolder(id string) error {
	m.mu.RLock()
	task := m.tasks[id]
	m.mu.RUnlock()
	if task == nil {
		return errors.New("torrent not found")
	}
	task.mu.Lock()
	path, pathErr := torrentDataPath(task.outputDir, task.name)
	task.mu.Unlock()
	if pathErr != nil {
		return pathErr
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect torrent data folder: %w", err)
	}
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	return openTransferPath(path, true)
}

func selectTransferDirectory() (string, error) {
	return chooseTransferDirectory()
}

func torrentDataPath(outputDir, name string) (string, error) {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name ||
		sanitizeDownloadFilename(name) != name {
		return "", errors.New("torrent name is not a safe file or folder name")
	}
	return filepath.Join(outputDir, name), nil
}
