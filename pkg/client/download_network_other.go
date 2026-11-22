//go:build !windows && !linux

package client

func eligibleDownloadInterfaceIndices() (map[int]struct{}, error) {
	return nil, nil
}
