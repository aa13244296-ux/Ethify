package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

const maxTorrentMetadataSize = 32 << 20

type TorrentSnapshot struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	State          string   `json:"state"`
	Size           int64    `json:"size"`
	Downloaded     int64    `json:"downloaded"`
	SpeedBps       int64    `json:"speedBps"`
	UploadSpeedBps int64    `json:"uploadSpeedBps"`
	Progress       float64  `json:"progress"`
	Peers          int      `json:"peers"`
	Error          string   `json:"error,omitempty"`
	CanResume      bool     `json:"canResume"`
	Networks       []string `json:"networks"`
}

type torrentTask struct {
	mu             sync.Mutex
	id             string
	name           string
	state          string
	size           int64
	downloaded     int64
	speedBps       int64
	uploadSpeedBps int64
	peers          int
	err            string
	source         string
	resumePath     string
	metainfoPath   string
	metainfoSaved  bool
	networks       []string
	client         *torrent.Client
	torrent        *torrent.Torrent
	cancel         context.CancelFunc
}

type TorrentManager struct {
	mu        sync.RWMutex
	tasks     map[string]*torrentTask
	outputDir string
	resumeDir string
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	wg        sync.WaitGroup
}

func NewTorrentManager(outputDir string) *TorrentManager {
	if outputDir == "" {
		outputDir = defaultDownloadDirectory()
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &TorrentManager{
		tasks:     make(map[string]*torrentTask),
		outputDir: outputDir,
		resumeDir: filepath.Join(resumeStateDirectory(outputDir), "torrents"),
		ctx:       ctx,
		cancel:    cancel,
	}
	if err := manager.restore(); err != nil {
		log.Printf("[Torrent] Resume recovery encountered errors: %v", err)
	}
	return manager
}

func (m *TorrentManager) Start(ctx context.Context, source string, interfaceIndices []int) (TorrentSnapshot, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return TorrentSnapshot{}, errors.New("torrent manager is closed")
	}
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	source = strings.TrimSpace(source)
	if !strings.HasPrefix(strings.ToLower(source), "magnet:") {
		parsed, err := url.ParseRequestURI(source)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return TorrentSnapshot{}, errors.New("enter a valid magnet link or HTTP(S) .torrent URL")
		}
	}
	networks, err := resolveTorrentNetworks(interfaceIndices)
	if err != nil {
		return TorrentSnapshot{}, err
	}
	if err := os.MkdirAll(m.outputDir, 0755); err != nil {
		return TorrentSnapshot{}, fmt.Errorf("create torrent download folder: %w", err)
	}

	client, err := newBoundTorrentClient(networks, m.outputDir)
	if err != nil {
		return TorrentSnapshot{}, err
	}
	requestCtx, cancelRequest := context.WithCancel(m.ctx)
	stopRequest := context.AfterFunc(ctx, cancelRequest)
	defer func() {
		stopRequest()
		cancelRequest()
	}()
	metadataCtx, cancelMetadata := context.WithTimeout(requestCtx, 30*time.Second)
	defer cancelMetadata()
	torrentHandle, err := addTorrentSource(metadataCtx, client, source, networks[0])
	if err != nil {
		return TorrentSnapshot{}, errors.Join(err, closeTorrentClient(client))
	}
	id, err := newDownloadID()
	if err != nil {
		return TorrentSnapshot{}, errors.Join(fmt.Errorf("create torrent ID: %w", err), closeTorrentClient(client))
	}

	taskCtx, cancel := context.WithCancel(m.ctx)
	task := &torrentTask{
		id:           id,
		name:         torrentHandle.Name(),
		state:        "metadata",
		source:       source,
		resumePath:   filepath.Join(m.resumeDir, id+".json"),
		metainfoPath: filepath.Join(m.resumeDir, id+".torrent"),
		client:       client,
		torrent:      torrentHandle,
		cancel:       cancel,
		networks:     make([]string, len(networks)),
	}
	for i, network := range networks {
		task.networks[i] = network.Name
	}
	if torrentHandle.Info() != nil {
		if err := task.persistMetainfo(); err != nil {
			cancel()
			return TorrentSnapshot{}, errors.Join(fmt.Errorf("save torrent metadata: %w", err), closeTorrentClient(client))
		}
	}
	if err := task.persistRecord(); err != nil {
		cancel()
		_ = task.removeResumeState()
		return TorrentSnapshot{}, errors.Join(fmt.Errorf("save torrent resume data: %w", err), closeTorrentClient(client))
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		_ = task.removeResumeState()
		return TorrentSnapshot{}, errors.Join(errors.New("torrent manager is closed"), closeTorrentClient(client))
	}
	m.tasks[id] = task
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		m.run(taskCtx, task)
	}()
	return task.snapshot(), nil
}

func resolveTorrentNetworks(interfaceIndices []int) ([]DownloadNetwork, error) {
	if len(interfaceIndices) < 2 {
		return nil, errors.New("select at least two active networks to download a torrent across adapters")
	}
	available, err := ListDownloadNetworks()
	if err != nil {
		return nil, err
	}
	wanted := make(map[int]struct{}, len(interfaceIndices))
	for _, index := range interfaceIndices {
		if _, exists := wanted[index]; exists {
			return nil, fmt.Errorf("network interface %d is selected more than once", index)
		}
		wanted[index] = struct{}{}
	}
	var selected []DownloadNetwork
	addresses := make(map[string]struct{}, len(interfaceIndices))
	for _, network := range available {
		if _, ok := wanted[network.InterfaceIndex]; !ok {
			continue
		}
		if _, duplicate := addresses[network.Address]; duplicate {
			return nil, errors.New("selected adapters have the same IPv4 address")
		}
		addresses[network.Address] = struct{}{}
		selected = append(selected, network)
		delete(wanted, network.InterfaceIndex)
	}
	if len(wanted) != 0 {
		return nil, errors.New("one or more selected networks are no longer active")
	}
	return selected, nil
}

func addTorrentSource(ctx context.Context, client *torrent.Client, source string, network DownloadNetwork) (*torrent.Torrent, error) {
	if strings.HasPrefix(strings.ToLower(source), "magnet:") {
		handle, err := client.AddMagnet(source)
		if err != nil {
			return nil, fmt.Errorf("add magnet link: %w", err)
		}
		return handle, nil
	}

	httpClient, err := newBoundHTTPClient(network)
	if err != nil {
		return nil, err
	}
	defer httpClient.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Ethifi/1.0")
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch torrent metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch torrent metadata: server returned HTTP %d", response.StatusCode)
	}
	metadata, err := io.ReadAll(io.LimitReader(response.Body, maxTorrentMetadataSize+1))
	if err != nil {
		return nil, fmt.Errorf("read torrent metadata: %w", err)
	}
	if len(metadata) > maxTorrentMetadataSize {
		return nil, fmt.Errorf("torrent metadata exceeds %d MiB", maxTorrentMetadataSize>>20)
	}
	info, err := metainfo.Load(bytes.NewReader(metadata))
	if err != nil {
		return nil, fmt.Errorf("parse torrent metadata: %w", err)
	}
	handle, err := client.AddTorrent(info)
	if err != nil {
		return nil, fmt.Errorf("add torrent metadata: %w", err)
	}
	return handle, nil
}

func (m *TorrentManager) run(ctx context.Context, task *torrentTask) {
	defer func() {
		if err := closeTorrentClient(task.client); err != nil {
			task.finish("failed", fmt.Errorf("close torrent client: %w", err))
		}
	}()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastRead, lastWritten int64
	lastSample := time.Now()
	downloadEnabled := false
	for {
		select {
		case <-ctx.Done():
			task.finish("paused", nil)
			return
		case <-ticker.C:
			if task.torrent.Info() == nil {
				task.updateMetadata()
				continue
			}
			task.mu.Lock()
			metainfoSaved := task.metainfoSaved
			task.mu.Unlock()
			if !metainfoSaved {
				if err := task.persistMetainfo(); err != nil {
					task.finish("failed", fmt.Errorf("save torrent metadata: %w", err))
					return
				}
			}
			if !downloadEnabled {
				task.torrent.DownloadAll()
				downloadEnabled = true
			}
			stats := task.torrent.Stats()
			now := time.Now()
			elapsed := now.Sub(lastSample).Seconds()
			if elapsed <= 0 {
				elapsed = 0.001
			}
			read := stats.BytesReadData.Int64()
			written := stats.BytesWrittenData.Int64()
			downloadRate, uploadRate := int64(0), int64(0)
			if read >= lastRead {
				downloadRate = int64(float64(read-lastRead) / elapsed)
			}
			if written >= lastWritten {
				uploadRate = int64(float64(written-lastWritten) / elapsed)
			}
			lastRead, lastWritten, lastSample = read, written, now

			info := task.torrent.Info()
			size := info.TotalLength()
			downloaded := task.torrent.BytesCompleted()
			task.mu.Lock()
			task.name = sanitizeDownloadFilename(task.torrent.Name())
			task.state = "downloading"
			task.size = size
			task.downloaded = downloaded
			task.speedBps = downloadRate
			task.uploadSpeedBps = uploadRate
			task.peers = stats.ActivePeers
			task.mu.Unlock()
			if size > 0 && downloaded >= size {
				task.finish("completed", nil)
				return
			}
		}
	}
}

func (task *torrentTask) updateMetadata() {
	task.mu.Lock()
	task.name = sanitizeDownloadFilename(task.torrent.Name())
	task.state = "metadata"
	task.peers = task.torrent.Stats().ActivePeers
	task.mu.Unlock()
}

func (task *torrentTask) finish(state string, err error) {
	task.cancel()
	task.mu.Lock()
	task.state = state
	task.speedBps = 0
	task.uploadSpeedBps = 0
	if err != nil {
		task.err = err.Error()
	}
	task.mu.Unlock()
	if state == "completed" {
		if err := task.removeResumeState(); err != nil {
			log.Printf("[Torrent] Completed %q but could not remove its resume data: %v", task.name, err)
		}
	}
}

func (task *torrentTask) snapshot() TorrentSnapshot {
	task.mu.Lock()
	defer task.mu.Unlock()
	progress := float64(0)
	if task.size > 0 {
		progress = float64(task.downloaded) * 100 / float64(task.size)
	}
	return TorrentSnapshot{
		ID:             task.id,
		Name:           task.name,
		State:          task.state,
		Size:           task.size,
		Downloaded:     task.downloaded,
		SpeedBps:       task.speedBps,
		UploadSpeedBps: task.uploadSpeedBps,
		Progress:       progress,
		Peers:          task.peers,
		Error:          task.err,
		CanResume:      (task.state == "paused" || task.state == "failed") && task.resumePath != "",
		Networks:       append([]string(nil), task.networks...),
	}
}

func (m *TorrentManager) List() []TorrentSnapshot {
	m.mu.RLock()
	tasks := make([]*torrentTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}
	m.mu.RUnlock()
	snapshots := make([]TorrentSnapshot, 0, len(tasks))
	for _, task := range tasks {
		snapshots = append(snapshots, task.snapshot())
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].ID < snapshots[j].ID })
	return snapshots
}

func (m *TorrentManager) Cancel(id string) error {
	m.mu.RLock()
	task := m.tasks[id]
	m.mu.RUnlock()
	if task == nil {
		return errors.New("torrent not found")
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.state != "metadata" && task.state != "downloading" {
		return fmt.Errorf("torrent is %s and cannot be cancelled", task.state)
	}
	task.state = "cancelling"
	task.cancel()
	return nil
}

func (m *TorrentManager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.cancel()
	tasks := make([]*torrentTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}
	m.mu.Unlock()

	for _, task := range tasks {
		task.mu.Lock()
		if task.state == "metadata" || task.state == "downloading" {
			task.state = "cancelling"
			task.cancel()
		}
		task.mu.Unlock()
	}
	m.wg.Wait()

	var closeErrors []error
	for _, task := range tasks {
		task.mu.Lock()
		if task.state == "failed" && task.err != "" {
			closeErrors = append(closeErrors, fmt.Errorf("torrent %q: %s", task.name, task.err))
		}
		task.mu.Unlock()
	}
	return errors.Join(closeErrors...)
}

func closeTorrentClient(client *torrent.Client) error {
	errs := client.Close()
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

type torrentDialerPool struct {
	networks []DownloadNetwork
	next     atomic.Uint64
}

func (pool *torrentDialerPool) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	index := int((pool.next.Add(1) - 1) % uint64(len(pool.networks)))
	return dialTorrentTCP(ctx, pool.networks[index], address)
}

type boundTorrentDialer struct {
	network DownloadNetwork
}

func (dialer boundTorrentDialer) DialContext(ctx context.Context, _, address string) (net.Conn, error) {
	return dialTorrentTCP(ctx, dialer.network, address)
}

func newBoundTorrentClient(networks []DownloadNetwork, outputDir string) (*torrent.Client, error) {
	if !torrentNetworkBindingSupported {
		return nil, errors.New("adapter-bound torrent downloads are supported on Windows only")
	}
	pool := &torrentDialerPool{networks: networks}
	config := torrent.NewDefaultClientConfig()
	config.DataDir = outputDir
	config.DisableIPv6 = true
	config.DisableUTP = true
	config.NoUpload = true
	config.AcceptPeerConnections = false
	config.NoDefaultPortForwarding = true
	config.ListenPort = 0
	config.DialForPeerConns = false
	config.ListenHost = func(string) string { return networks[0].Address }
	config.ListenPacket = func(network, address string) (net.PacketConn, error) {
		return listenTorrentPacket(network, address, networks[0])
	}
	config.TrackerListenPacket = func(network, address string) (net.PacketConn, error) {
		return listenTorrentPacket(network, address, networks[0])
	}
	config.TrackerDialContext = pool.DialContext
	config.HTTPDialContext = pool.DialContext
	client, err := torrent.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("start torrent client: %w", err)
	}
	for _, network := range networks {
		client.AddDialer(torrent.NetworkDialer{
			Network: "tcp4",
			Dialer:  boundTorrentDialer{network: network},
		})
	}
	return client, nil
}
