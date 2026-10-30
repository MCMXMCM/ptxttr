package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
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

func TestEstimateBytePruneKeepEvents(t *testing.T) {
	if got := estimateBytePruneKeepEvents(1000, 2_000, 1_800); got != 900 {
		t.Fatalf("keep = %d, want 900", got)
	}
	if got := estimateBytePruneKeepEvents(1000, 1_700, 1_800); got != 1000 {
		t.Fatalf("keep below target = %d, want all events", got)
	}
}

func TestFullVacuumEnabledMatchesVacuumScript(t *testing.T) {
	for raw, want := range map[string]bool{
		"": true, "1": true, "true": true, "yes": true, "on": true,
		"0": false, "false": false, "off": false, "no": false, "TRUE": false,
	} {
		t.Setenv("PTXT_VACUUM_FULL_ENABLED", raw)
		if got := FullVacuumEnabled(); got != want {
			t.Fatalf("FullVacuumEnabled(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestAutomaticFullVacuumsRespectDisabledFlag(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	st.SetDiskRetentionPolicy(80, 70)
	ids := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		event := nostrx.Event{
			ID:        fmt.Sprintf("%064x", 9000+i),
			PubKey:    strings.Repeat("5", 64),
			CreatedAt: int64(1000 + i),
			Kind:      nostrx.KindTextNote,
			Content:   strings.Repeat("vacuum fodder ", 200),
			Sig:       strings.Repeat("1", 128),
		}
		mustSaveEvent(t, ctx, st, event)
		ids = append(ids, event.ID)
	}
	if err := st.DeleteEventsForTesting(ctx, ids); err != nil {
		t.Fatal(err)
	}
	freePages := func() int64 {
		t.Helper()
		var pages int64
		if err := st.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&pages); err != nil {
			t.Fatal(err)
		}
		return pages
	}
	if freePages() == 0 {
		t.Fatal("test setup left no free pages to vacuum")
	}

	t.Setenv("PTXT_VACUUM_FULL_ENABLED", "0")
	st.periodicFullVacuum()
	st.runVacuumWithTimeout("test")
	if freePages() == 0 {
		t.Fatal("a disabled full vacuum still rewrote the database")
	}

	t.Setenv("PTXT_VACUUM_FULL_ENABLED", "1")
	st.periodicFullVacuum()
	if got := freePages(); got != 0 {
		t.Fatalf("enabled periodic full vacuum left %d free pages", got)
	}
}
