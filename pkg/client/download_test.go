package client

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDownloadRangeFallsBackToAnotherAdapter(t *testing.T) {
	payload := []byte("ethifi-range-fallback-payload")
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "adapter unavailable", http.StatusServiceUnavailable)
	}))
	defer failing.Close()

	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload)
	}))
	defer working.Close()

	workingURL, err := url.Parse(working.URL)
	if err != nil {
		t.Fatal(err)
	}

	file, err := os.CreateTemp(t.TempDir(), "ethifi-fallback-*.part")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close test file: %v", err)
		}
	})
	if err := file.Truncate(int64(len(payload))); err != nil {
		t.Fatal(err)
	}

	task := &downloadTask{
		ctx:       context.Background(),
		target:    workingURL.String(),
		size:      int64(len(payload)),
		file:      file,
		chunkSize: int64(len(payload)),
		completed: []bool{false},
	}
	routes := []downloadRangeRoute{
		{
			client: failing.Client(),
			meter: &downloadNetworkMeter{
				network: DownloadNetwork{InterfaceIndex: 6, Name: "Ethernet"},
			},
		},
		{
			client: working.Client(),
			meter: &downloadNetworkMeter{
				network: DownloadNetwork{InterfaceIndex: 14, Name: "Wi-Fi"},
			},
		},
	}
	if err := task.fetchRangeWithFallback(routes, 0, workingURL, 0, int64(len(payload)-1)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len(payload))
	if _, err := file.ReadAt(buffer, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buffer, payload) {
		t.Fatalf("fallback payload = %q, want %q", buffer, payload)
	}
}

func TestDownloadManagerAcceptsSingleAdapterForManagedDownloads(t *testing.T) {
	payload := []byte("single-adapter-download")
	loopbackIndex := testLoopbackInterfaceIndex(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bounds := strings.Split(strings.TrimPrefix(r.Header.Get("Range"), "bytes="), "-")
		if len(bounds) != 2 {
			http.Error(w, "invalid range", http.StatusBadRequest)
			return
		}
		start, err := strconv.Atoi(bounds[0])
		if err != nil {
			http.Error(w, "invalid range start", http.StatusBadRequest)
			return
		}
		end, err := strconv.Atoi(bounds[1])
		if err != nil || start < 0 || end < start || end >= len(payload) {
			http.Error(w, "invalid range end", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer server.Close()

	target, err := url.Parse(server.URL + "/file.bin")
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "Downloads")
	manager := NewDownloadManager(directory)
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close download manager: %v", err)
		}
	})
	if _, err := manager.startWithNetworks(context.Background(), target, []DownloadNetwork{
		{InterfaceIndex: loopbackIndex, Name: "loopback", Address: "127.0.0.1"},
	}); err != nil {
		t.Fatalf("start single-adapter download: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshots := manager.List()
		if len(snapshots) == 1 && snapshots[0].State == "completed" {
			actual, err := os.ReadFile(filepath.Join(directory, snapshots[0].Name))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, payload) {
				t.Fatalf("downloaded file = %q, want %q", actual, payload)
			}
			if info, err := os.Stat(directory); err != nil || !info.IsDir() {
				t.Fatalf("download directory was not created: info=%v err=%v", info, err)
			}
			return
		}
		if len(snapshots) == 1 && snapshots[0].State == "failed" {
			t.Fatalf("single-adapter download failed: %s", snapshots[0].Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("single-adapter download did not complete")
}

func TestDownloadManagerCombinesBoundInterfaces(t *testing.T) {
	payload := bytes.Repeat([]byte("ethifi-range-data-"), 16384)
	loopbackIndex := testLoopbackInterfaceIndex(t)
	var seen sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			seen.Store(host, struct{}{})
		}
		rangeHeader := strings.TrimPrefix(r.Header.Get("Range"), "bytes=")
		bounds := strings.Split(rangeHeader, "-")
		if len(bounds) != 2 {
			http.Error(w, "range required", http.StatusBadRequest)
			return
		}
		start, err := strconv.Atoi(bounds[0])
		if err != nil {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		end, err := strconv.Atoi(bounds[1])
		if err != nil || start < 0 || end < start || end >= len(payload) {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer server.Close()

	target, err := url.Parse(server.URL + "/sample.bin")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manager := NewDownloadManager(directory)
	initial, err := manager.startWithNetworks(context.Background(), target, []DownloadNetwork{
		{InterfaceIndex: loopbackIndex, Name: "Wi-Fi test", Address: "127.0.0.1"},
		{InterfaceIndex: loopbackIndex, Name: "Ethernet test", Address: "127.0.0.2"},
	})
	if err != nil {
		t.Fatalf("start combined download: %v", err)
	}

	var completed DownloadSnapshot
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshots := manager.List()
		if len(snapshots) == 1 && snapshots[0].State == "completed" {
			completed = snapshots[0]
			break
		}
		if len(snapshots) == 1 && snapshots[0].State == "failed" {
			t.Fatalf("download failed: %s", snapshots[0].Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if completed.State != "completed" {
		t.Fatalf("download did not complete; initial state %q", initial.State)
	}

	actual, err := os.ReadFile(filepath.Join(directory, completed.Name))
	if err != nil {
		t.Fatalf("read completed file: %v", err)
	}
	if !bytes.Equal(actual, payload) {
		t.Fatal("downloaded file does not match server content")
	}
	seenCount := 0
	seen.Range(func(_, _ any) bool {
		seenCount++
		return true
	})
	if seenCount < 2 {
		t.Fatalf("server observed %d source addresses, want both bound interfaces", seenCount)
	}
	if completed.Downloaded != int64(len(payload)) || completed.Progress != 100 {
		t.Fatalf("download stats = %d bytes, %.1f%%; want %d bytes, 100%%", completed.Downloaded, completed.Progress, len(payload))
	}
}

func TestUsableDownloadAddressAcceptsPrivateAndLinkLocalIPv4(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{address: "192.168.1.20", want: true},
		{address: "10.0.0.12", want: true},
		{address: "169.254.1.4", want: true},
		{address: "127.0.0.1", want: false},
		{address: "0.0.0.0", want: false},
		{address: "224.0.0.1", want: false},
		{address: "255.255.255.255", want: false},
		{address: "2001:db8::1", want: false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			address, err := netip.ParseAddr(test.address)
			if err != nil {
				t.Fatal(err)
			}
			if got := isUsableDownloadAddress(address); got != test.want {
				t.Fatalf("isUsableDownloadAddress(%s) = %v, want %v", address, got, test.want)
			}
		})
	}
}

func TestListDownloadNetworksReturnsActiveIPv4Interfaces(t *testing.T) {
	networks, err := ListDownloadNetworks()
	if err != nil {
		t.Fatalf("list download networks: %v", err)
	}
	seen := make(map[int]struct{}, len(networks))
	for _, network := range networks {
		if network.InterfaceIndex <= 0 || network.Name == "" {
			t.Fatalf("network has no stable interface identity: %+v", network)
		}
		if _, exists := seen[network.InterfaceIndex]; exists {
			t.Fatalf("interface %d was listed more than once", network.InterfaceIndex)
		}
		seen[network.InterfaceIndex] = struct{}{}
		address, err := netip.ParseAddr(network.Address)
		if err != nil || !isUsableDownloadAddress(address) {
			t.Fatalf("network has unusable address %q: %v", network.Address, err)
		}
		iface, err := net.InterfaceByIndex(network.InterfaceIndex)
		if err != nil {
			t.Fatalf("interface %d disappeared during enumeration: %v", network.InterfaceIndex, err)
		}
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			t.Fatalf("reported interface is not active and non-loopback: %+v", iface)
		}
	}
}

func TestDownloadManagerRejectsServerWithoutRangeSupport(t *testing.T) {
	loopbackIndex := testLoopbackInterfaceIndex(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not ranged"))
	}))
	defer server.Close()
	target, err := url.Parse(server.URL + "/sample.bin")
	if err != nil {
		t.Fatal(err)
	}

	manager := NewDownloadManager(t.TempDir())
	_, err = manager.startWithNetworks(context.Background(), target, []DownloadNetwork{
		{InterfaceIndex: loopbackIndex, Name: "Wi-Fi test", Address: "127.0.0.1"},
		{InterfaceIndex: loopbackIndex, Name: "Ethernet test", Address: "127.0.0.2"},
	})
	if err == nil || !strings.Contains(err.Error(), "did not honor byte ranges") {
		t.Fatalf("error = %v, want range-support rejection", err)
	}
	if got := len(manager.List()); got != 0 {
		t.Fatalf("created %d tasks after rejected range probe, want 0", got)
	}
}

func TestDownloadManagerCancelPreservesPartialFileForResume(t *testing.T) {
	loopbackIndex := testLoopbackInterfaceIndex(t)
	requestStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", "bytes 0-0/1048576")
			w.Header().Set("Content-Length", "1")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("x"))
			return
		}
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	target, err := url.Parse(server.URL + "/slow.bin")
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	manager := NewDownloadManager(directory)
	download, err := manager.startWithNetworks(context.Background(), target, []DownloadNetwork{
		{InterfaceIndex: loopbackIndex, Name: "Wi-Fi test", Address: "127.0.0.1"},
		{InterfaceIndex: loopbackIndex, Name: "Ethernet test", Address: "127.0.0.2"},
	})
	if err != nil {
		t.Fatalf("start download: %v", err)
	}
	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("download workers did not start")
	}
	if err := manager.Cancel(download.ID); err != nil {
		t.Fatalf("cancel download: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshots := manager.List()
		if len(snapshots) == 1 && snapshots[0].State == "paused" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	snapshots := manager.List()
	if len(snapshots) != 1 || snapshots[0].State != "paused" || !snapshots[0].CanResume {
		t.Fatalf("download state = %v, want resumable pause", snapshots)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("saved resume files = %v, want partial file and checkpoint", entries)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
}

func TestDownloadManagerResumesAfterRestart(t *testing.T) {
	payload := bytes.Repeat([]byte("resume-after-restart-"), 16384)
	loopbackIndex := testLoopbackInterfaceIndex(t)
	requestStarted := make(chan struct{}, 1)
	var block sync.Mutex
	blockRequests := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
			w.Header().Set("Content-Length", "1")
			w.Header().Set("ETag", `"stable-version"`)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[:1])
			return
		}
		block.Lock()
		shouldBlock := blockRequests
		block.Unlock()
		if shouldBlock {
			select {
			case requestStarted <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		rangeHeader := strings.TrimPrefix(r.Header.Get("Range"), "bytes=")
		bounds := strings.Split(rangeHeader, "-")
		if len(bounds) != 2 {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		start, err := strconv.Atoi(bounds[0])
		if err != nil {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		end, err := strconv.Atoi(bounds[1])
		if err != nil || start < 0 || end < start || end >= len(payload) {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
		w.Header().Set("ETag", `"stable-version"`)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer server.Close()
	target, err := url.Parse(server.URL + "/resume.bin")
	if err != nil {
		t.Fatal(err)
	}
	networks := []DownloadNetwork{
		{InterfaceIndex: loopbackIndex, Name: "Wi-Fi test", Address: "127.0.0.1"},
		{InterfaceIndex: loopbackIndex, Name: "Ethernet test", Address: "127.0.0.2"},
	}
	directory := t.TempDir()
	manager := NewDownloadManager(directory)
	created, err := manager.startWithNetworks(context.Background(), target, networks)
	if err != nil {
		t.Fatalf("start download: %v", err)
	}
	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("download workers did not start")
	}
	if err := manager.Cancel(created.ID); err != nil {
		t.Fatalf("pause download: %v", err)
	}
	waitForDownloadState(t, manager, created.ID, "paused")
	if err := manager.Close(); err != nil {
		t.Fatalf("close first manager: %v", err)
	}

	manager = NewDownloadManager(directory)
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close resumed manager: %v", err)
		}
	})
	recovered := manager.List()
	if len(recovered) != 1 || recovered[0].ID != created.ID || recovered[0].State != "paused" || !recovered[0].CanResume {
		t.Fatalf("recovered downloads = %+v, want original paused download", recovered)
	}
	block.Lock()
	blockRequests = false
	block.Unlock()
	if _, err := manager.resumeWithNetworks(context.Background(), created.ID, networks); err != nil {
		t.Fatalf("resume recovered download: %v", err)
	}
	waitForDownloadState(t, manager, created.ID, "completed")
	actual, err := os.ReadFile(filepath.Join(directory, created.Name))
	if err != nil {
		t.Fatalf("read resumed file: %v", err)
	}
	if !bytes.Equal(actual, payload) {
		t.Fatal("resumed file does not match server content")
	}
}

func waitForDownloadState(t *testing.T, manager *DownloadManager, id, state string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, snapshot := range manager.List() {
			if snapshot.ID != id {
				continue
			}
			if snapshot.State == "failed" {
				t.Fatalf("download failed while waiting for %s: %s", state, snapshot.Error)
			}
			if snapshot.State == state {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("download did not reach state %q", state)
}

func testLoopbackInterfaceIndex(t *testing.T) int {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("list interfaces: %v", err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return iface.Index
		}
	}
	t.Skip("loopback interface is unavailable")
	return 0
}
