package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	downloadWorkersPerNetwork = 4
	maxDownloadChunkSize      = 8 << 20
)

// DownloadNetwork identifies one active IPv4 interface that can be bound for
// an independent ranged HTTP request.
type DownloadNetwork struct {
	InterfaceIndex int    `json:"interfaceIndex"`
	Name           string `json:"name"`
	Address        string `json:"address"`
	subnet         netip.Prefix
}

type DownloadNetworkSnapshot struct {
	InterfaceIndex int    `json:"interfaceIndex"`
	Name           string `json:"name"`
	Address        string `json:"address"`
	Downloaded     int64  `json:"downloaded"`
	SpeedBps       int64  `json:"speedBps"`
}

type downloadProbe struct {
	size         int64
	filename     string
	etag         string
	lastModified string
}

type DownloadSnapshot struct {
	ID         string                    `json:"id"`
	Name       string                    `json:"name"`
	State      string                    `json:"state"`
	Location   string                    `json:"location"`
	Size       int64                     `json:"size"`
	Downloaded int64                     `json:"downloaded"`
	SpeedBps   int64                     `json:"speedBps"`
	Progress   float64                   `json:"progress"`
	Error      string                    `json:"error,omitempty"`
	CanResume  bool                      `json:"canResume"`
	Networks   []DownloadNetworkSnapshot `json:"networks"`
}

type downloadNetworkMeter struct {
	network    DownloadNetwork
	bytes      atomic.Int64
	last       int64
	lastSample time.Time
}

type downloadTask struct {
	mu         sync.Mutex
	id         string
	name       string
	state      string
	size       int64
	err        string
	file       *os.File
	partPath   string
	final      string
	target     string
	etag       string
	modified   string
	resumePath string
	ctx        context.Context
	cancel     context.CancelFunc

	checkpointMu sync.Mutex
	completed    []bool
	nextChunk    atomic.Int64
	downloaded   atomic.Int64
	chunkSize    int64
	networks     []*downloadNetworkMeter

	lastSample time.Time
	lastBytes  int64
	speedBps   int64
}

type DownloadManager struct {
	mu        sync.RWMutex
	tasks     map[string]*downloadTask
	reserved  map[string]struct{}
	outputDir string
	resumeDir string
	wg        sync.WaitGroup
	closed    bool
}

func NewDownloadManager(outputDir string) *DownloadManager {
	if outputDir == "" {
		outputDir = defaultDownloadDirectory()
	}
	manager := &DownloadManager{
		tasks:     make(map[string]*downloadTask),
		reserved:  make(map[string]struct{}),
		outputDir: outputDir,
		resumeDir: filepath.Join(resumeStateDirectory(outputDir), "downloads"),
	}
	if err := manager.restore(); err != nil {
		log.Printf("[Downloads] Resume recovery encountered errors: %v", err)
	}
	return manager
}

// ListDownloadNetworks returns active interfaces with a usable IPv4 address.
// Windows download sockets are pinned by both source address and interface index.
func ListDownloadNetworks() ([]DownloadNetwork, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	eligible, err := eligibleDownloadInterfaceIndices()
	if err != nil {
		return nil, err
	}

	var networks []DownloadNetwork
	for _, iface := range interfaces {
		if iface.Index <= 0 || iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if eligible != nil {
			if _, ok := eligible[iface.Index]; !ok {
				continue
			}
		}
		if strings.Contains(strings.ToLower(iface.Name), "ethify") {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, raw := range addresses {
			var address netip.Addr
			var subnet netip.Prefix
			switch value := raw.(type) {
			case *net.IPNet:
				address, _ = netip.ParseAddr(value.IP.String())
				if value.Mask != nil {
					prefixLength, _ := value.Mask.Size()
					if prefixLength >= 0 && address.Is4() {
						subnet = netip.PrefixFrom(address, prefixLength).Masked()
					}
				}
			case *net.IPAddr:
				address, _ = netip.ParseAddr(value.IP.String())
			}
			if !isUsableDownloadAddress(address) {
				continue
			}
			networks = append(networks, DownloadNetwork{
				InterfaceIndex: iface.Index,
				Name:           iface.Name,
				Address:        address.String(),
				subnet:         subnet,
			})
			break
		}
	}
	sort.Slice(networks, func(i, j int) bool {
		return networks[i].InterfaceIndex < networks[j].InterfaceIndex
	})
	return networks, nil
}

func isUsableDownloadAddress(address netip.Addr) bool {
	return address.IsValid() && address.Is4() && !address.IsLoopback() &&
		(address.IsGlobalUnicast() || address.IsLinkLocalUnicast())
}

func (m *DownloadManager) Start(ctx context.Context, rawURL string, interfaceIndices []int) (DownloadSnapshot, error) {
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return DownloadSnapshot{}, errors.New("download manager is closed")
	}
	parsed, err := url.ParseRequestURI(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return DownloadSnapshot{}, errors.New("enter a valid HTTP or HTTPS download URL")
	}
	if len(interfaceIndices) < 1 {
		return DownloadSnapshot{}, errors.New("select at least one active network")
	}

	available, err := ListDownloadNetworks()
	if err != nil {
		return DownloadSnapshot{}, err
	}
	wanted := make(map[int]struct{}, len(interfaceIndices))
	for _, index := range interfaceIndices {
		if _, exists := wanted[index]; exists {
			return DownloadSnapshot{}, fmt.Errorf("network interface %d is selected more than once", index)
		}
		wanted[index] = struct{}{}
	}
	var selected []DownloadNetwork
	seenAddresses := make(map[string]struct{}, len(interfaceIndices))
	for _, network := range available {
		if _, ok := wanted[network.InterfaceIndex]; ok {
			if _, duplicate := seenAddresses[network.Address]; duplicate {
				return DownloadSnapshot{}, errors.New("selected adapters have the same IPv4 address")
			}
			seenAddresses[network.Address] = struct{}{}
			selected = append(selected, network)
			delete(wanted, network.InterfaceIndex)
		}
	}
	if len(wanted) != 0 {
		return DownloadSnapshot{}, errors.New("one or more selected networks are no longer active")
	}
	return m.startWithNetworks(ctx, parsed, selected)
}

func (m *DownloadManager) startWithNetworks(ctx context.Context, target *url.URL, networks []DownloadNetwork) (DownloadSnapshot, error) {
	if len(networks) < 1 {
		return DownloadSnapshot{}, errors.New("at least one active network is required")
	}
	seenAddresses := make(map[string]struct{}, len(networks))
	for _, network := range networks {
		address, err := netip.ParseAddr(network.Address)
		if err != nil || !address.Is4() || address.IsUnspecified() {
			return DownloadSnapshot{}, fmt.Errorf("network %q has no usable IPv4 address", network.Name)
		}
		if _, duplicate := seenAddresses[address.String()]; duplicate {
			return DownloadSnapshot{}, errors.New("selected adapters have the same IPv4 address")
		}
		seenAddresses[address.String()] = struct{}{}
	}

	type probeResult struct {
		probe downloadProbe
		err   error
	}
	results := make([]probeResult, len(networks))
	var probes sync.WaitGroup
	for i := range networks {
		probes.Add(1)
		go func(i int) {
			defer probes.Done()
			results[i].probe, results[i].err = probeRangedURL(ctx, target, networks[i])
		}(i)
	}
	probes.Wait()

	size := results[0].probe.size
	filename := results[0].probe.filename
	etag := results[0].probe.etag
	lastModified := results[0].probe.lastModified
	for i, result := range results {
		if result.err != nil {
			return DownloadSnapshot{}, fmt.Errorf("%s cannot reach this download: %w", networks[i].Name, result.err)
		}
		if result.probe.size != size {
			return DownloadSnapshot{}, fmt.Errorf("%s returned a different file size; the server cannot safely combine these paths", networks[i].Name)
		}
		if !sameDownloadRepresentation(etag, lastModified, result.probe.etag, result.probe.lastModified) {
			return DownloadSnapshot{}, fmt.Errorf("%s returned a different file version; the server cannot safely combine these paths", networks[i].Name)
		}
		if filename == "" && result.probe.filename != "" {
			filename = result.probe.filename
		}
	}
	if size <= 0 {
		return DownloadSnapshot{}, errors.New("download server did not report a positive file size")
	}
	if filename == "" {
		filename = safeDownloadFilename(target)
	}
	filename = sanitizeDownloadFilename(filename)

	if err := os.MkdirAll(m.outputDir, 0755); err != nil {
		return DownloadSnapshot{}, fmt.Errorf("create download folder: %w", err)
	}
	id, err := newDownloadID()
	if err != nil {
		return DownloadSnapshot{}, fmt.Errorf("create download ID: %w", err)
	}
	finalPath, err := m.reserveOutputPath(filename)
	if err != nil {
		return DownloadSnapshot{}, err
	}
	partPath := finalPath + "." + id + ".part"
	file, err := os.OpenFile(partPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		m.releaseOutputPath(finalPath)
		return DownloadSnapshot{}, fmt.Errorf("create partial download: %w", err)
	}
	if err := file.Truncate(size); err != nil {
		_ = file.Close()
		_ = os.Remove(partPath)
		m.releaseOutputPath(finalPath)
		return DownloadSnapshot{}, fmt.Errorf("prepare download file: %w", err)
	}

	taskCtx, cancel := context.WithCancel(context.Background())
	chunkSize := max(int64(1), min(int64(maxDownloadChunkSize), (size+int64(len(networks)*downloadWorkersPerNetwork)-1)/int64(len(networks)*downloadWorkersPerNetwork)))
	task := &downloadTask{
		id:         id,
		name:       filename,
		state:      "downloading",
		size:       size,
		file:       file,
		partPath:   partPath,
		final:      finalPath,
		target:     target.String(),
		etag:       etag,
		modified:   lastModified,
		resumePath: filepath.Join(m.resumeDir, id+".json"),
		ctx:        taskCtx,
		cancel:     cancel,
		chunkSize:  chunkSize,
		completed:  make([]bool, (size+chunkSize-1)/chunkSize),
		lastSample: time.Now(),
		networks:   make([]*downloadNetworkMeter, len(networks)),
	}
	for i, network := range networks {
		task.networks[i] = &downloadNetworkMeter{network: network, lastSample: task.lastSample}
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		_ = file.Close()
		_ = os.Remove(partPath)
		m.releaseOutputPath(finalPath)
		return DownloadSnapshot{}, errors.New("download manager is closed")
	}
	if err := task.persistResumeState(); err != nil {
		m.mu.Unlock()
		cancel()
		_ = file.Close()
		_ = os.Remove(partPath)
		m.releaseOutputPath(finalPath)
		return DownloadSnapshot{}, err
	}
	m.tasks[id] = task
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		m.run(task, target)
	}()
	return task.snapshot(), nil
}

func sameDownloadRepresentation(leftETag, leftModified, rightETag, rightModified string) bool {
	if leftETag != "" || rightETag != "" {
		if leftETag == rightETag && leftETag != "" {
			return leftModified == "" || rightModified == "" || leftModified == rightModified
		}
		return leftModified != "" && leftModified == rightModified
	}
	if leftModified != "" || rightModified != "" {
		return leftModified == rightModified
	}
	return true
}

func probeRangedURL(ctx context.Context, target *url.URL, network DownloadNetwork) (downloadProbe, error) {
	client, err := newBoundHTTPClient(network)
	if err != nil {
		return downloadProbe{}, err
	}
	defer client.CloseIdleConnections()

	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return downloadProbe{}, err
	}
	request.Header.Set("Range", "bytes=0-0")
	request.Header.Set("User-Agent", "Ethifi/0.1")
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(request)
	if err != nil {
		return downloadProbe{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		return downloadProbe{}, fmt.Errorf("server did not honor byte ranges (HTTP %d)", response.StatusCode)
	}
	size, err := parseContentRange(response.Header.Get("Content-Range"), 0, 0)
	if err != nil {
		return downloadProbe{}, err
	}
	if response.ContentLength >= 0 && response.ContentLength != 1 {
		return downloadProbe{}, errors.New("server returned an invalid one-byte range probe")
	}
	var probe [1]byte
	if _, err := io.ReadFull(response.Body, probe[:]); err != nil {
		return downloadProbe{}, err
	}
	return downloadProbe{
		size:         size,
		filename:     responseFilename(response.Header, target),
		etag:         response.Header.Get("ETag"),
		lastModified: response.Header.Get("Last-Modified"),
	}, nil
}

func newBoundHTTPClient(network DownloadNetwork) (*http.Client, error) {
	address, err := netip.ParseAddr(network.Address)
	if err != nil || !address.Is4() || address.IsUnspecified() {
		return nil, fmt.Errorf("invalid local IPv4 address %q", network.Address)
	}
	if network.InterfaceIndex <= 0 {
		return nil, fmt.Errorf("invalid interface index %d for network %q", network.InterfaceIndex, network.Name)
	}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		LocalAddr: &net.TCPAddr{IP: net.IP(address.AsSlice())},
		Control: func(_, _ string, raw syscall.RawConn) error {
			return bindTCPInterface(raw, network.InterfaceIndex)
		},
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, remoteAddress string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", remoteAddress)
		},
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: downloadWorkersPerNetwork,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{Transport: transport}, nil
}

func (m *DownloadManager) run(task *downloadTask, target *url.URL) {
	defer m.releaseOutputPath(task.final)
	var workers sync.WaitGroup
	var failureOnce sync.Once
	failure := make(chan error, 1)

	fail := func(err error) {
		failureOnce.Do(func() {
			failure <- err
			task.cancel()
		})
	}

	routes := make([]downloadRangeRoute, 0, len(task.networks))
	for _, meter := range task.networks {
		client, err := newBoundHTTPClient(meter.network)
		if err != nil {
			fail(err)
			continue
		}
		routes = append(routes, downloadRangeRoute{client: client, meter: meter})
	}
	defer func() {
		for _, route := range routes {
			route.client.CloseIdleConnections()
		}
	}()

	for routeIndex := range routes {
		for i := 0; i < downloadWorkersPerNetwork; i++ {
			workers.Add(1)
			go func(primaryIndex int) {
				defer workers.Done()
				for {
					if task.ctx.Err() != nil {
						return
					}
					chunk := task.nextChunk.Add(1) - 1
					start := chunk * task.chunkSize
					if start >= task.size {
						return
					}
					task.mu.Lock()
					completed := task.completed[chunk]
					task.mu.Unlock()
					if completed {
						continue
					}
					end := min(start+task.chunkSize-1, task.size-1)
					if err := task.fetchRangeWithFallback(routes, primaryIndex, target, start, end); err != nil {
						if task.ctx.Err() == nil {
							fail(err)
						}
						return
					}
					if err := task.completeChunk(chunk); err != nil {
						fail(err)
						return
					}
				}
			}(routeIndex)
		}
	}

	workers.Wait()
	select {
	case err := <-failure:
		m.finish(task, "failed", err)
	case <-task.ctx.Done():
		if len(failure) == 0 {
			m.finish(task, "paused", nil)
		} else {
			m.finish(task, "failed", <-failure)
		}
	default:
		if task.downloaded.Load() != task.size {
			m.finish(task, "failed", fmt.Errorf("download incomplete: received %d of %d bytes", task.downloaded.Load(), task.size))
			return
		}
		if err := task.file.Sync(); err != nil {
			m.finish(task, "failed", fmt.Errorf("flush download: %w", err))
			return
		}
		if err := task.file.Close(); err != nil {
			m.finish(task, "failed", fmt.Errorf("close download: %w", err))
			return
		}
		if err := os.Rename(task.partPath, task.final); err != nil {
			m.finish(task, "failed", fmt.Errorf("save completed download: %w", err))
			return
		}
		task.mu.Lock()
		task.file = nil
		task.mu.Unlock()
		m.finish(task, "completed", nil)
	}
}

func (task *downloadTask) fetchRange(client *http.Client, target *url.URL, meter *downloadNetworkMeter, start, end int64) error {
	request, err := http.NewRequestWithContext(task.ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	if task.etag != "" && !strings.HasPrefix(strings.TrimSpace(task.etag), "W/") {
		request.Header.Set("If-Range", task.etag)
	} else if task.modified != "" {
		request.Header.Set("If-Range", task.modified)
	}
	request.Header.Set("User-Agent", "Ethifi/0.1")
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("server stopped honoring byte ranges (HTTP %d)", response.StatusCode)
	}
	if task.etag != "" && response.Header.Get("ETag") != "" && task.etag != response.Header.Get("ETag") {
		return errors.New("server changed the file ETag during the download")
	}
	if task.modified != "" && response.Header.Get("Last-Modified") != "" && task.modified != response.Header.Get("Last-Modified") {
		return errors.New("server changed the file timestamp during the download")
	}
	total, err := parseContentRange(response.Header.Get("Content-Range"), start, end)
	if err != nil {
		return err
	}
	if total != task.size {
		return errors.New("server changed the file size during the download")
	}

	expected := end - start + 1
	if response.ContentLength >= 0 && response.ContentLength != expected {
		return fmt.Errorf("server returned %d bytes for a %d-byte range", response.ContentLength, expected)
	}
	writer := &downloadChunkWriter{file: task.file, offset: start, task: task, meter: meter}
	written, err := io.CopyN(writer, response.Body, expected)
	if err != nil {
		return fmt.Errorf("read range: %w", err)
	}
	if written != expected {
		return fmt.Errorf("short range: received %d of %d bytes", written, expected)
	}
	return nil
}

type downloadRangeRoute struct {
	client *http.Client
	meter  *downloadNetworkMeter
}

func (task *downloadTask) fetchRangeWithFallback(routes []downloadRangeRoute, primaryIndex int, target *url.URL, start, end int64) error {
	var errs []error
	for offset := 0; offset < len(routes); offset++ {
		index := (primaryIndex + offset) % len(routes)
		route := routes[index]
		if err := task.fetchRange(route.client, target, route.meter, start, end); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Errorf("%s: %w", route.meter.network.Name, err))
		}
		if task.ctx.Err() != nil {
			return task.ctx.Err()
		}
		if offset+1 < len(routes) {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-timer.C:
			case <-task.ctx.Done():
				timer.Stop()
				return task.ctx.Err()
			}
			timer.Stop()
		}
	}
	return fmt.Errorf("all selected adapters failed this range: %w", errors.Join(errs...))
}

type downloadChunkWriter struct {
	file   *os.File
	offset int64
	task   *downloadTask
	meter  *downloadNetworkMeter
}

func (w *downloadChunkWriter) Write(data []byte) (int, error) {
	n, err := w.file.WriteAt(data, w.offset)
	w.offset += int64(n)
	w.task.downloaded.Add(int64(n))
	w.meter.bytes.Add(int64(n))
	return n, err
}

func parseContentRange(value string, wantStart, wantEnd int64) (int64, error) {
	if !strings.HasPrefix(value, "bytes ") {
		return 0, errors.New("server returned an invalid Content-Range")
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes "), "/")
	if len(parts) != 2 {
		return 0, errors.New("server returned an invalid Content-Range")
	}
	bounds := strings.Split(parts[0], "-")
	if len(bounds) != 2 {
		return 0, errors.New("server returned an invalid Content-Range")
	}
	start, err := strconv.ParseInt(bounds[0], 10, 64)
	if err != nil {
		return 0, errors.New("server returned an invalid Content-Range start")
	}
	end, err := strconv.ParseInt(bounds[1], 10, 64)
	if err != nil {
		return 0, errors.New("server returned an invalid Content-Range end")
	}
	total, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || total <= end || start != wantStart || end != wantEnd {
		return 0, errors.New("server returned a mismatched Content-Range")
	}
	return total, nil
}

func responseFilename(header http.Header, target *url.URL) string {
	if disposition := header.Get("Content-Disposition"); disposition != "" {
		_, params, err := mime.ParseMediaType(disposition)
		if err == nil && params["filename"] != "" {
			return params["filename"]
		}
	}
	return safeDownloadFilename(target)
}

func safeDownloadFilename(target *url.URL) string {
	name, err := url.PathUnescape(filepath.Base(target.Path))
	if err != nil || name == "." || name == string(filepath.Separator) || name == "" {
		return "download.bin"
	}
	return name
}

func sanitizeDownloadFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" || name == "." || name == ".." {
		return "download.bin"
	}
	return name
}

func newDownloadID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (m *DownloadManager) reserveOutputPath(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for suffix := 0; ; suffix++ {
		candidate := name
		if suffix > 0 {
			ext := filepath.Ext(name)
			base := strings.TrimSuffix(name, ext)
			candidate = fmt.Sprintf("%s (%d)%s", base, suffix, ext)
		}
		final := filepath.Join(m.outputDir, candidate)
		if _, ok := m.reserved[final]; ok {
			continue
		}
		if _, err := os.Lstat(final); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("check download destination: %w", err)
		}
		m.reserved[final] = struct{}{}
		return final, nil
	}
}

func (m *DownloadManager) releaseOutputPath(path string) {
	m.mu.Lock()
	delete(m.reserved, path)
	m.mu.Unlock()
}

func (m *DownloadManager) finish(task *downloadTask, state string, err error) {
	task.cancel()
	if task.file != nil {
		if syncErr := task.file.Sync(); syncErr != nil && err == nil {
			err = fmt.Errorf("flush partial download: %w", syncErr)
		}
		if closeErr := task.file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close partial download: %w", closeErr)
		}
		task.mu.Lock()
		task.file = nil
		task.mu.Unlock()
	}
	if state == "completed" {
		if removeErr := task.removeResumeState(); removeErr != nil {
			log.Printf("[Downloads] Completed %q but could not remove its checkpoint: %v", task.name, removeErr)
		}
	} else {
		task.downloaded.Store(task.completedBytes())
	}
	task.mu.Lock()
	task.state = state
	if err != nil {
		task.err = err.Error()
	}
	task.mu.Unlock()
}

func (m *DownloadManager) List() []DownloadSnapshot {
	m.mu.RLock()
	tasks := make([]*downloadTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}
	m.mu.RUnlock()

	snapshots := make([]DownloadSnapshot, 0, len(tasks))
	for _, task := range tasks {
		snapshots = append(snapshots, task.snapshot())
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].ID < snapshots[j].ID })
	return snapshots
}

func (m *DownloadManager) Cancel(id string) error {
	m.mu.RLock()
	task := m.tasks[id]
	m.mu.RUnlock()
	if task == nil {
		return errors.New("download not found")
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.state != "downloading" {
		return fmt.Errorf("download is %s and cannot be cancelled", task.state)
	}
	task.state = "cancelling"
	task.cancel()
	return nil
}

func (m *DownloadManager) Resume(ctx context.Context, id string, interfaceIndices []int) (DownloadSnapshot, error) {
	networks, err := resolveDownloadNetworks(interfaceIndices)
	if err != nil {
		return DownloadSnapshot{}, err
	}
	return m.resumeWithNetworks(ctx, id, networks)
}

func (m *DownloadManager) resumeWithNetworks(ctx context.Context, id string, networks []DownloadNetwork) (DownloadSnapshot, error) {
	m.mu.RLock()
	task := m.tasks[id]
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return DownloadSnapshot{}, errors.New("download manager is closed")
	}
	if task == nil {
		return DownloadSnapshot{}, errors.New("download not found")
	}
	task.mu.Lock()
	if task.state != "paused" && task.state != "failed" {
		state := task.state
		task.mu.Unlock()
		return DownloadSnapshot{}, fmt.Errorf("download is %s and cannot be resumed", state)
	}
	if task.file == nil {
		file, err := os.OpenFile(task.partPath, os.O_RDWR, 0600)
		if err != nil {
			task.mu.Unlock()
			return DownloadSnapshot{}, fmt.Errorf("open partial download: %w", err)
		}
		task.file = file
	}
	task.state = "resuming"
	task.err = ""
	targetString := task.target
	task.mu.Unlock()

	failResume := func(err error) (DownloadSnapshot, error) {
		task.mu.Lock()
		task.state = "paused"
		task.err = err.Error()
		task.mu.Unlock()
		return DownloadSnapshot{}, err
	}
	target, err := url.ParseRequestURI(targetString)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return failResume(errors.New("saved download URL is invalid"))
	}
	if len(networks) < 1 {
		return failResume(errors.New("select at least one active network to resume this download"))
	}
	for _, network := range networks {
		probe, err := probeRangedURL(ctx, target, network)
		if err != nil {
			return failResume(fmt.Errorf("%s cannot resume this download: %w", network.Name, err))
		}
		if probe.size != task.size || !sameDownloadRepresentation(task.etag, task.modified, probe.etag, probe.lastModified) {
			return failResume(errors.New("the server file changed since this download was paused"))
		}
	}

	task.mu.Lock()
	task.networks = make([]*downloadNetworkMeter, len(networks))
	now := time.Now()
	for index, network := range networks {
		task.networks[index] = &downloadNetworkMeter{network: network, lastSample: now}
	}
	task.nextChunk.Store(0)
	task.lastSample = now
	task.lastBytes = task.downloaded.Load()
	task.ctx, task.cancel = context.WithCancel(context.Background())
	task.state = "downloading"
	task.mu.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		task.cancel()
		return failResume(errors.New("download manager is closed"))
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		m.run(task, target)
	}()
	return task.snapshot(), nil
}

func resolveDownloadNetworks(interfaceIndices []int) ([]DownloadNetwork, error) {
	if len(interfaceIndices) < 1 {
		return nil, errors.New("select at least one active network")
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
	if len(wanted) > 0 {
		return nil, errors.New("one or more selected networks are no longer active")
	}
	return selected, nil
}

func (m *DownloadManager) Discard(id string) error {
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil {
		m.mu.Unlock()
		return errors.New("download not found")
	}
	task.mu.Lock()
	if task.state == "downloading" || task.state == "cancelling" || task.state == "resuming" {
		state := task.state
		task.mu.Unlock()
		m.mu.Unlock()
		return fmt.Errorf("download is %s; pause it before discarding", state)
	}
	task.mu.Unlock()
	if err := os.Remove(task.partPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.mu.Unlock()
		return fmt.Errorf("remove partial download: %w", err)
	}
	if err := task.removeResumeState(); err != nil {
		m.mu.Unlock()
		return err
	}
	delete(m.tasks, id)
	delete(m.reserved, task.final)
	m.mu.Unlock()
	return nil
}

func (m *DownloadManager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	tasks := make([]*downloadTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}
	for _, task := range tasks {
		task.mu.Lock()
		if task.state == "downloading" || task.state == "resuming" {
			task.state = "cancelling"
			task.cancel()
		}
		task.mu.Unlock()
	}
	m.mu.Unlock()
	m.wg.Wait()
	var closeErrors []error
	for _, task := range tasks {
		task.mu.Lock()
		if task.file != nil {
			if err := task.file.Sync(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("flush %q: %w", task.name, err))
			}
			if err := task.file.Close(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("close %q: %w", task.name, err))
			}
			task.file = nil
		}
		task.mu.Unlock()
	}
	return errors.Join(closeErrors...)
}

func (task *downloadTask) snapshot() DownloadSnapshot {
	task.mu.Lock()
	defer task.mu.Unlock()
	now := time.Now()
	downloaded := task.downloaded.Load()
	if elapsed := now.Sub(task.lastSample); elapsed > 0 {
		task.speedBps = int64(float64(downloaded-task.lastBytes) / elapsed.Seconds())
		task.lastBytes = downloaded
		task.lastSample = now
	}
	if task.state == "completed" || task.state == "failed" || task.state == "paused" {
		task.speedBps = 0
	}
	snapshot := DownloadSnapshot{
		ID:         task.id,
		Name:       task.name,
		State:      task.state,
		Location:   task.final,
		Size:       task.size,
		Downloaded: downloaded,
		SpeedBps:   task.speedBps,
		Error:      task.err,
		CanResume:  (task.state == "paused" || task.state == "failed") && task.resumePath != "",
		Networks:   make([]DownloadNetworkSnapshot, 0, len(task.networks)),
	}
	if task.size > 0 {
		snapshot.Progress = float64(downloaded) * 100 / float64(task.size)
	}
	for _, meter := range task.networks {
		current := meter.bytes.Load()
		elapsed := now.Sub(meter.lastSample).Seconds()
		if elapsed <= 0 {
			elapsed = 0.001
		}
		rate := int64(float64(current-meter.last) / elapsed)
		if task.state == "completed" || task.state == "failed" || task.state == "cancelled" {
			rate = 0
		}
		meter.last = current
		meter.lastSample = now
		snapshot.Networks = append(snapshot.Networks, DownloadNetworkSnapshot{
			InterfaceIndex: meter.network.InterfaceIndex,
			Name:           meter.network.Name,
			Address:        meter.network.Address,
			Downloaded:     current,
			SpeedBps:       rate,
		})
	}
	return snapshot
}

func defaultDownloadDirectory() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Downloads")
	}
	if current, err := user.Current(); err == nil && current.HomeDir != "" {
		return filepath.Join(current.HomeDir, "Downloads")
	}
	return filepath.Join(os.TempDir(), "Ethifi Downloads")
}
