package client

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func moveFilePath(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return fmt.Errorf("create destination folder: %w", err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect destination: %w", err)
	}
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	if err := copyRegularFile(source, destination); err != nil {
		return err
	}
	if err := os.Remove(source); err != nil {
		if removeErr := os.Remove(destination); removeErr != nil {
			return errors.Join(fmt.Errorf("remove original after copying: %w", err), fmt.Errorf("remove copied file: %w", removeErr))
		}
		return fmt.Errorf("remove original after copying: %w", err)
	}
	return nil
}

func moveDirectoryPath(source, destination string) (returnErr error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve source folder: %w", err)
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve destination folder: %w", err)
	}
	relative, err := filepath.Rel(source, destination)
	if err != nil {
		return fmt.Errorf("compare source and destination: %w", err)
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) {
		return errors.New("destination cannot be inside the current download folder")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return fmt.Errorf("create destination parent folder: %w", err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect destination: %w", err)
	}
	if err := os.Rename(source, destination); err == nil {
		return nil
	}

	staging, err := os.MkdirTemp(filepath.Dir(destination), ".ethify-move-*")
	if err != nil {
		return fmt.Errorf("create temporary destination: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(staging); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary destination: %w", cleanupErr))
		}
	}()
	if err := copyDirectory(source, staging); err != nil {
		return fmt.Errorf("copy download folder: %w", err)
	}
	if err := os.Rename(staging, destination); err != nil {
		return fmt.Errorf("finish copying download folder: %w", err)
	}
	if err := os.RemoveAll(source); err != nil {
		return fmt.Errorf("remove original after copying to %q: %w", destination, err)
	}
	return nil
}

func copyDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			return copyRegularFile(path, target)
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("refusing to move symlink %q", relative)
		default:
			return fmt.Errorf("refusing to move unsupported file %q", relative)
		}
	})
}

func copyRegularFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	inputClosed := false
	defer func() {
		if !inputClosed {
			if closeErr := input.Close(); closeErr != nil {
				log.Printf("[Transfers] Close source file %q: %v", source, closeErr)
			}
		}
	}()
	info, err := input.Stat()
	if err != nil {
		return fmt.Errorf("inspect source file: %w", err)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create destination file: %w", err)
	}
	_, copyErr := io.Copy(output, input)
	inputCloseErr := input.Close()
	inputClosed = true
	syncErr := output.Sync()
	closeErr := output.Close()
	if err := errors.Join(copyErr, inputCloseErr, syncErr, closeErr); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("copy file: %w", err)
	}
	return nil
}
