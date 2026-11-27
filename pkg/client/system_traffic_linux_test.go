//go:build linux

package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultLinuxDownloadDirectoryUsesHomeDownloads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got, want := defaultDownloadDirectory(), filepath.Join(home, "Downloads"); got != want {
		t.Fatalf("defaultDownloadDirectory() = %q, want %q", got, want)
	}
}

func TestReadLinuxInterfaceByteCounters(t *testing.T) {
	root := t.TempDir()
	for name, value := range map[string]string{
		"rx_bytes": "12345\n",
		"tx_bytes": "6789\n",
	} {
		path := filepath.Join(root, "enp1s0", "statistics", name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := readLinuxInterfaceByteCounters(root, []DownloadNetwork{
		{InterfaceIndex: 4, Name: "enp1s0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[4] != (interfaceByteCounters{received: 12345, sent: 6789}) {
		t.Fatalf("interface counters = %+v, want received=12345 sent=6789", got[4])
	}
}

func TestReadLinuxInterfaceByteCountersReportsMissingFiles(t *testing.T) {
	_, err := readLinuxInterfaceByteCounters(t.TempDir(), []DownloadNetwork{
		{InterfaceIndex: 4, Name: "enp1s0"},
	})
	if err == nil {
		t.Fatal("expected a missing sysfs counter error")
	}
}
