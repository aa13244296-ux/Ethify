package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func resumeStateDirectory(outputDir string) string {
	if filepath.Clean(outputDir) != filepath.Clean(defaultDownloadDirectory()) {
		return filepath.Join(outputDir, ".ethifi-resume")
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(outputDir, ".ethifi-resume")
	}
	return filepath.Join(configDir, "Ethifi", "resume")
}

func writeResumeState(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode resume data: %w", err)
	}
	return writeResumeBytes(path, data)
}

func writeResumeBytes(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create resume data directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".resume-*.tmp")
	if err != nil {
		return fmt.Errorf("create resume data file: %w", err)
	}
	tempPath := file.Name()
	defer func() {
		if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("[Resume] Remove temporary state file %q: %v", tempPath, err)
		}
	}()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure resume data file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write resume data: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("flush resume data: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close resume data: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace resume data: %w", err)
	}
	return nil
}

func readResumeState(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open resume data: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("[Resume] Close state file %q: %v", path, err)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect resume data: %w", err)
	}
	if info.Size() > 16<<20 {
		return errors.New("resume data exceeds the 16 MiB limit")
	}
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode resume data: %w", err)
	}
	return nil
}
