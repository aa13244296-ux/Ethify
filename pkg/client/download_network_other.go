//go:build !windows

package client

func eligibleDownloadInterfaceIndices() (map[int]struct{}, error) {
	return nil, nil
}
