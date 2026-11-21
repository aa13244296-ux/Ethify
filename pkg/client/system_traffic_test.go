package client

import (
	"testing"
	"time"
)

func TestSystemTrafficMeterCalculatesPerAdapterRates(t *testing.T) {
	meter := newSystemTrafficMeter()
	networks := []DownloadNetwork{
		{InterfaceIndex: 6, Name: "Ethernet", Address: "192.0.2.1"},
		{InterfaceIndex: 13, Name: "Wi-Fi", Address: "192.0.2.2"},
	}
	start := time.Unix(100, 0)
	initial := meter.snapshot(networks, map[int]interfaceByteCounters{
		6:  {received: 1000, sent: 2000},
		13: {received: 5000, sent: 6000},
	}, start)
	for _, sample := range initial {
		if sample.DownRateBps != 0 || sample.UpRateBps != 0 {
			t.Fatalf("initial rates = down %d up %d; want zero before second sample", sample.DownRateBps, sample.UpRateBps)
		}
	}

	next := meter.snapshot(networks, map[int]interfaceByteCounters{
		6:  {received: 3000, sent: 2500},
		13: {received: 9000, sent: 8000},
	}, start.Add(2*time.Second))
	if len(next) != 2 {
		t.Fatalf("got %d adapter samples, want 2", len(next))
	}
	if next[0].DownRateBps != 1000 || next[0].UpRateBps != 250 {
		t.Fatalf("Ethernet rates = down %d up %d; want 1000 and 250 B/s", next[0].DownRateBps, next[0].UpRateBps)
	}
	if next[1].DownRateBps != 2000 || next[1].UpRateBps != 1000 {
		t.Fatalf("Wi-Fi rates = down %d up %d; want 2000 and 1000 B/s", next[1].DownRateBps, next[1].UpRateBps)
	}
}

func TestSystemTrafficMeterResetsWhenCountersDrop(t *testing.T) {
	meter := newSystemTrafficMeter()
	network := []DownloadNetwork{{InterfaceIndex: 6, Name: "Ethernet", Address: "192.0.2.1"}}
	start := time.Unix(100, 0)
	meter.snapshot(network, map[int]interfaceByteCounters{
		6: {received: 1000, sent: 1000},
	}, start)

	got := meter.snapshot(network, map[int]interfaceByteCounters{
		6: {received: 10, sent: 20},
	}, start.Add(time.Second))
	if got[0].DownRateBps != 0 || got[0].UpRateBps != 0 {
		t.Fatalf("rates after counter reset = down %d up %d; want zero", got[0].DownRateBps, got[0].UpRateBps)
	}
}

func TestSystemTrafficMeterCalculatesPerProcessRatesByAdapter(t *testing.T) {
	meter := newSystemTrafficMeter()
	networks := []DownloadNetwork{
		{InterfaceIndex: 6, Name: "Ethernet"},
		{InterfaceIndex: 13, Name: "Wi-Fi"},
	}
	start := time.Unix(100, 0)
	initial := []processFlowCounter{
		{Key: "steam-ethernet", ProcessID: 120, ProcessName: "steam.exe", InterfaceIndex: 6, Received: 1000, Sent: 200},
		{Key: "steam-ethernet-2", ProcessID: 120, ProcessName: "steam.exe", InterfaceIndex: 6, Received: 2000, Sent: 300},
		{Key: "browser-wifi", ProcessID: 220, ProcessName: "brave.exe", InterfaceIndex: 13, Received: 4000, Sent: 500},
	}
	if got := meter.processSnapshot(networks, initial, start); len(got) != 2 {
		t.Fatalf("initial process samples = %d, want 2", len(got))
	}

	next := []processFlowCounter{
		{Key: "steam-ethernet", ProcessID: 120, ProcessName: "steam.exe", InterfaceIndex: 6, Received: 5000, Sent: 1200},
		{Key: "steam-ethernet-2", ProcessID: 120, ProcessName: "steam.exe", InterfaceIndex: 6, Received: 2500, Sent: 500},
		{Key: "browser-wifi", ProcessID: 220, ProcessName: "brave.exe", InterfaceIndex: 13, Received: 6000, Sent: 900},
	}
	got := meter.processSnapshot(networks, next, start.Add(2*time.Second))
	if len(got) != 2 {
		t.Fatalf("process samples = %d, want 2", len(got))
	}
	byName := make(map[string]systemProcessTrafficSample, len(got))
	for _, sample := range got {
		byName[sample.Name] = sample
	}
	steam := byName["steam.exe"]
	if steam.InterfaceName != "Ethernet" || steam.Connections != 2 || steam.Downloaded != 4500 || steam.Uploaded != 1200 || steam.DownRateBps != 2250 || steam.UpRateBps != 600 {
		t.Fatalf("Steam process traffic = %+v", steam)
	}
	browser := byName["brave.exe"]
	if browser.InterfaceName != "Wi-Fi" || browser.Downloaded != 2000 || browser.Uploaded != 400 || browser.DownRateBps != 1000 || browser.UpRateBps != 200 {
		t.Fatalf("browser process traffic = %+v", browser)
	}
}
