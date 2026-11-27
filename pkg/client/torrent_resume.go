package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

type torrentResumeRecord struct {
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	OutputDir string `json:"outputDir,omitempty"`
}

func (task *torrentTask) persistRecord() error {
	task.mu.Lock()
	record := torrentResumeRecord{
		Version:   1,
		ID:        task.id,
		Name:      task.name,
		Source:    task.source,
		OutputDir: task.outputDir,
	}
	path := task.resumePath
	task.mu.Unlock()
	return writeResumeState(path, record)
}

func (task *torrentTask) persistMetainfo() error {
	if task.torrent == nil || task.torrent.Info() == nil {
		return errors.New("torrent metadata is not available yet")
	}
	metainfo := task.torrent.Metainfo()
	if len(metainfo.InfoBytes) == 0 {
		return errors.New("torrent metadata is empty")
	}
	var data bytes.Buffer
	if err := metainfo.Write(&data); err != nil {
		return fmt.Errorf("encode torrent metadata: %w", err)
	}
	task.mu.Lock()
	path := task.metainfoPath
	task.mu.Unlock()
	if err := writeResumeBytes(path, data.Bytes()); err != nil {
		return err
	}
	task.mu.Lock()
	task.metainfoSaved = true
	task.mu.Unlock()
	return task.persistRecord()
}

func (task *torrentTask) removeResumeState() error {
	var errs []error
	for _, path := range []string{task.resumePath, task.metainfoPath} {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *TorrentManager) restore() error {
	entries, err := os.ReadDir(m.resumeDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list torrent resume data: %w", err)
	}
	if err := os.MkdirAll(m.resumeDir, 0700); err != nil {
		return fmt.Errorf("secure torrent resume directory: %w", err)
	}
	var failures []error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(m.resumeDir, entry.Name())
		var record torrentResumeRecord
		if err := readResumeState(path, &record); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		if record.Version != 1 || record.ID == "" || filepath.Base(record.ID) != record.ID ||
			entry.Name() != record.ID+".json" || record.Source == "" {
			failures = append(failures, fmt.Errorf("%s: invalid torrent resume record", entry.Name()))
			continue
		}
		if record.Name != "" && sanitizeDownloadFilename(record.Name) != record.Name {
			failures = append(failures, fmt.Errorf("%s: invalid torrent name", entry.Name()))
			continue
		}
		if record.OutputDir == "" {
			record.OutputDir = m.outputDir
		} else if !filepath.IsAbs(record.OutputDir) {
			failures = append(failures, fmt.Errorf("%s: invalid torrent download directory", entry.Name()))
			continue
		}
		task := &torrentTask{
			id:           record.ID,
			name:         record.Name,
			state:        "paused",
			source:       record.Source,
			outputDir:    filepath.Clean(record.OutputDir),
			resumePath:   path,
			metainfoPath: filepath.Join(m.resumeDir, record.ID+".torrent"),
		}
		if _, err := os.Stat(task.metainfoPath); err == nil {
			task.metainfoSaved = true
		} else if !os.IsNotExist(err) {
			failures = append(failures, fmt.Errorf("%s: inspect torrent metadata: %w", entry.Name(), err))
			continue
		}
		m.tasks[task.id] = task
	}
	return errors.Join(failures...)
}

func (m *TorrentManager) Resume(ctx context.Context, id string, interfaceIndices []int) (TorrentSnapshot, error) {
	networks, err := resolveTorrentNetworks(interfaceIndices)
	if err != nil {
		return TorrentSnapshot{}, err
	}
	m.mu.RLock()
	task := m.tasks[id]
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return TorrentSnapshot{}, errors.New("torrent manager is closed")
	}
	if task == nil {
		return TorrentSnapshot{}, errors.New("torrent not found")
	}
	task.mu.Lock()
	if task.state != "paused" && task.state != "failed" {
		state := task.state
		task.mu.Unlock()
		return TorrentSnapshot{}, fmt.Errorf("torrent is %s and cannot be resumed", state)
	}
	source, metaPath := task.source, task.metainfoPath
	task.state = "resuming"
	task.err = ""
	task.mu.Unlock()
	fail := func(err error) (TorrentSnapshot, error) {
		task.mu.Lock()
		task.state = "paused"
		task.err = err.Error()
		task.mu.Unlock()
		return TorrentSnapshot{}, err
	}
	task.mu.Lock()
	outputDir := task.outputDir
	task.mu.Unlock()
	client, err := newBoundTorrentClient(networks, outputDir)
	if err != nil {
		return fail(err)
	}
	var handle *torrent.Torrent
	if file, err := os.Open(metaPath); err == nil {
		fileInfo, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return fail(errors.Join(fmt.Errorf("inspect saved torrent metadata: %w", statErr), closeTorrentClient(client)))
		}
		if fileInfo.Size() > maxTorrentMetadataSize {
			_ = file.Close()
			return fail(errors.Join(errors.New("saved torrent metadata exceeds the size limit"), closeTorrentClient(client)))
		}
		info, loadErr := metainfo.Load(file)
		closeErr := file.Close()
		if loadErr != nil {
			return fail(errors.Join(fmt.Errorf("load saved torrent metadata: %w", loadErr), closeErr, closeTorrentClient(client)))
		}
		if closeErr != nil {
			return fail(errors.Join(fmt.Errorf("close saved torrent metadata: %w", closeErr), closeTorrentClient(client)))
		}
		handle, err = client.AddTorrent(info)
		if err != nil {
			return fail(errors.Join(fmt.Errorf("restore torrent: %w", err), closeTorrentClient(client)))
		}
	} else if os.IsNotExist(err) {
		metadataCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		handle, err = addTorrentSource(metadataCtx, client, source, networks[0])
		cancel()
		if err != nil {
			return fail(errors.Join(err, closeTorrentClient(client)))
		}
	} else {
		return fail(errors.Join(fmt.Errorf("open saved torrent metadata: %w", err), closeTorrentClient(client)))
	}
	taskCtx, cancel := context.WithCancel(m.ctx)
	task.mu.Lock()
	task.client = client
	task.torrent = handle
	task.cancel = cancel
	task.networks = make([]string, len(networks))
	for i := range networks {
		task.networks[i] = networks[i].Name
	}
	task.metainfoSaved = handle.Info() != nil && task.metainfoSaved
	task.state = "metadata"
	task.mu.Unlock()
	if err := task.persistRecord(); err != nil {
		cancel()
		return fail(errors.Join(fmt.Errorf("update torrent resume data: %w", err), closeTorrentClient(client)))
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		_ = closeTorrentClient(client)
		return fail(errors.New("torrent manager is closed"))
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		m.run(taskCtx, task)
	}()
	return task.snapshot(), nil
}

func (m *TorrentManager) Discard(id string) error {
	m.mu.RLock()
	task := m.tasks[id]
	m.mu.RUnlock()
	if task == nil {
		return errors.New("torrent not found")
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.state != "paused" && task.state != "failed" {
		return fmt.Errorf("torrent is %s and cannot be discarded", task.state)
	}
	if err := task.removeResumeState(); err != nil {
		return fmt.Errorf("remove torrent resume data: %w", err)
	}
	task.state = "cancelled"
	task.err = ""
	return nil
}
