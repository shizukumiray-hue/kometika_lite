//go:build !windows

package api

import "syscall"

func getDiskSpace(path string, info *SystemInfo) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		if err := syscall.Statfs(".", &stat); err != nil {
			return
		}
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bavail * uint64(stat.Bsize)
	if totalBytes == 0 {
		return
	}

	usedBytes := totalBytes - freeBytes
	const GB = 1024 * 1024 * 1024
	info.DiskTotalGB = float64(totalBytes) / GB
	info.DiskUsedGB = float64(usedBytes) / GB
	info.DiskPercent = (float64(usedBytes) / float64(totalBytes)) * 100
}
