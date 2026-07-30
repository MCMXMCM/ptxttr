package store

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DiskUsagePercent returns the percentage of blocks used on the filesystem
// containing path (0–100). The second return is false when statfs fails.
func DiskUsagePercent(path string) (float64, bool) {
	if path == "" {
		return 0, false
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, false
	}
	if stat.Blocks == 0 {
		return 0, false
	}
	used := stat.Blocks - stat.Bfree
	return float64(used) / float64(stat.Blocks) * 100, true
}

// DBDiskUsagePercent reports usage for the filesystem backing dbPath.
func DBDiskUsagePercent(dbPath string) (float64, bool) {
	if dbPath == "" {
		return 0, false
	}
	if abs, err := filepath.Abs(dbPath); err == nil && abs != "" {
		dbPath = abs
	}
	return DiskUsagePercent(dbPath)
}

// DBFileUsage reports SQLite storage as a percentage of the filesystem
// capacity backing dbPath. It includes the main DB plus WAL/SHM sidecars.
func DBFileUsage(dbPath string) (dbBytes, totalBytes int64, percent float64, ok bool) {
	if dbPath == "" {
		return 0, 0, 0, false
	}
	if abs, err := filepath.Abs(dbPath); err == nil && abs != "" {
		dbPath = abs
	}
	dir := filepath.Dir(dbPath)
	if dir == "" {
		dir = "."
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil || stat.Blocks == 0 || stat.Bsize <= 0 {
		return 0, 0, 0, false
	}
	total := int64(stat.Blocks) * int64(stat.Bsize)
	if total <= 0 {
		return 0, 0, 0, false
	}
	size := DBFileBytes(dbPath)
	return size, total, float64(size) / float64(total) * 100, true
}

// DBFileBytes returns the bytes occupied by the SQLite database and sidecars.
func DBFileBytes(dbPath string) int64 {
	if dbPath == "" {
		return 0
	}
	if abs, err := filepath.Abs(dbPath); err == nil && abs != "" {
		dbPath = abs
	}
	var total int64
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			total += info.Size()
		}
	}
	return total
}

func diskPressureThresholdPercent() int {
	return positiveIntEnv("PTXT_DB_DISK_PRESSURE_PERCENT", 85)
}

func pruneTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("PTXT_PRUNE_TIMEOUT"))
	if raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 5 * time.Minute
}

func vacuumTimeoutMinutes() int64 {
	return positiveInt64Env("PTXT_VACUUM_TIMEOUT_MIN", 60)
}

func vacuumTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("PTXT_VACUUM_TIMEOUT"))
	if raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return time.Duration(vacuumTimeoutMinutes()) * time.Minute
}

func freelistVacuumRatioThreshold() float64 {
	raw := strings.TrimSpace(os.Getenv("PTXT_FREELIST_VACUUM_RATIO"))
	if raw == "" {
		return 0.15
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil || parsed <= 0 {
		return 0.15
	}
	return parsed
}
