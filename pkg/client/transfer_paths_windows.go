package client

import (
	"errors"
	"strings"
)

func openTransferPath(path string, reveal bool) error {
	if reveal {
		return execHidden("explorer.exe", path).Start()
	}
	return execHidden("rundll32.exe", "url.dll,FileProtocolHandler", path).Start()
}

func chooseTransferDirectory() (string, error) {
	command := `$dialog = New-Object System.Windows.Forms.FolderBrowserDialog; if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Write($dialog.SelectedPath) }`
	output, err := execHidden("powershell.exe", "-NoProfile", "-STA", "-Command", "Add-Type -AssemblyName System.Windows.Forms; "+command).Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", errors.New("folder selection was cancelled")
	}
	return path, nil
}
