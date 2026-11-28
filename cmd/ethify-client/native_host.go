package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

const (
	nativeHostMaxMessageSize = 1 << 20
	ethifyDashboardURL       = "http://127.0.0.1:18889"
)

type nativeHostRequest struct {
	Action string `json:"action"`
}

type nativeHostResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func runNativeMessagingHost() error {
	for {
		var size uint32
		if err := binary.Read(os.Stdin, binary.LittleEndian, &size); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read native message length: %w", err)
		}
		if size == 0 || size > nativeHostMaxMessageSize {
			return fmt.Errorf("native message size %d is invalid", size)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(os.Stdin, payload); err != nil {
			return fmt.Errorf("read native message: %w", err)
		}
		var request nativeHostRequest
		if err := json.Unmarshal(payload, &request); err != nil {
			return writeNativeHostResponse(nativeHostResponse{Error: "invalid native-host request"})
		}
		if request.Action != "ensure-running" {
			if err := writeNativeHostResponse(nativeHostResponse{Error: "unsupported native-host action"}); err != nil {
				return err
			}
			continue
		}
		if err := ensureEthifyService(); err != nil {
			if writeErr := writeNativeHostResponse(nativeHostResponse{Error: err.Error()}); writeErr != nil {
				return writeErr
			}
			continue
		}
		if err := writeNativeHostResponse(nativeHostResponse{OK: true}); err != nil {
			return err
		}
	}
}

func writeNativeHostResponse(response nativeHostResponse) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode native-host response: %w", err)
	}
	if len(payload) > nativeHostMaxMessageSize {
		return errors.New("native-host response exceeds the message limit")
	}
	if err := binary.Write(os.Stdout, binary.LittleEndian, uint32(len(payload))); err != nil {
		return fmt.Errorf("write native-host response length: %w", err)
	}
	if _, err := os.Stdout.Write(payload); err != nil {
		return fmt.Errorf("write native-host response: %w", err)
	}
	return nil
}

func ensureEthifyService() error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	if ethifyServiceReady(client) {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate Ethify executable: %w", err)
	}
	command := exec.Command(executable, "--daemon", "--no-browser")
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Ethify: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release Ethify process: %w", err)
	}

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if ethifyServiceReady(client) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("ethify did not start its local service")
}

func ethifyServiceReady(client *http.Client) bool {
	response, err := client.Get(ethifyDashboardURL + "/api/status")
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return response.StatusCode == http.StatusOK
}
