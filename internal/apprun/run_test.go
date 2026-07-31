package apprun

import (
	"context"
	"path/filepath"
	"testing"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/store"
)

func TestApplySavedDesktopCacheLimitOverridesPackagedDefault(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache-limit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const saved = int64(4 * 1024 * 1024 * 1024)
	if err := st.SetAppMeta(ctx, store.AppMetaKeyCacheMaxBytes, "4294967296"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		DesktopMode:        true,
		DBMaxBytes:         store.DefaultCacheMaxBytes,
		DBPruneTargetBytes: store.DefaultCacheMaxBytes * 9 / 10,
	}

	applySavedDesktopCacheLimit(ctx, &cfg, st)

	if cfg.DBMaxBytes != saved || cfg.DBPruneTargetBytes != saved*9/10 {
		t.Fatalf("cache policy = (%d, %d), want (%d, %d)", cfg.DBMaxBytes, cfg.DBPruneTargetBytes, saved, saved*9/10)
	}
	if !cfg.RetentionByAccess {
		t.Fatal("saved desktop cache limit did not enable LRU retention")
	}
}

func TestApplySavedDesktopCacheLimitDoesNotAffectHostedServer(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "cache-limit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SetAppMeta(ctx, store.AppMetaKeyCacheMaxBytes, "4294967296"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DBMaxBytes: 123, DBPruneTargetBytes: 100}

	applySavedDesktopCacheLimit(ctx, &cfg, st)

	if cfg.DBMaxBytes != 123 || cfg.DBPruneTargetBytes != 100 {
		t.Fatalf("hosted cache policy changed to (%d, %d)", cfg.DBMaxBytes, cfg.DBPruneTargetBytes)
	}
}
