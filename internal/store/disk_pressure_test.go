package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDBDiskUsagePercentOnTempDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sqlite")
	if _, ok := DBDiskUsagePercent(path); !ok {
		t.Fatal("expected statfs to succeed on temp dir")
	}
}

func TestDiskPressureThresholdDefault(t *testing.T) {
	t.Setenv("PTXT_DB_DISK_PRESSURE_PERCENT", "")
	if got := diskPressureThresholdPercent(); got != 85 {
		t.Fatalf("threshold = %d, want 85", got)
	}
}

func TestVacuumTimeoutFromEnv(t *testing.T) {
	t.Setenv("PTXT_VACUUM_TIMEOUT", "45m")
	if got := vacuumTimeout(); got != 45*time.Minute {
		t.Fatalf("vacuumTimeout = %v, want 45m", got)
	}
}

func TestFreelistVacuumRatioThresholdDefault(t *testing.T) {
	t.Setenv("PTXT_FREELIST_VACUUM_RATIO", "")
	if got := freelistVacuumRatioThreshold(); got != 0.15 {
		t.Fatalf("ratio threshold = %v, want 0.15", got)
	}
}

func TestDiskUsagePercentEmptyPath(t *testing.T) {
	if _, ok := DiskUsagePercent(""); ok {
		t.Fatal("expected false for empty path")
	}
}

func TestDBDiskUsagePercentUsesEnvDir(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "db.sqlite"), []byte("x"), 0o644)
	if _, ok := DBDiskUsagePercent(filepath.Join(dir, "db.sqlite")); !ok {
		t.Fatal("expected statfs on db parent dir")
	}
}

func TestDBFileBytesIncludesSQLiteSidecars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.sqlite")
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("123"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-shm", []byte("12"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DBFileBytes(path); got != 10 {
		t.Fatalf("DBFileBytes = %d, want 10", got)
	}
}

func TestEstimateDiskPruneKeepEvents(t *testing.T) {
	keep := estimateDiskPruneKeepEvents(1000, 90, 100, 70)
	if keep >= 1000 || keep <= 0 {
		t.Fatalf("keep = %d, want a positive reduced count", keep)
	}
	if got := estimateDiskPruneKeepEvents(1000, 60, 100, 70); got != 1000 {
		t.Fatalf("keep below target = %d, want all events", got)
	}
}
