package staticfs

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// FS contains embedded static web assets.
//
//go:embed css/*.css js/*.js js/*/*.js build/*.js build/chunks/*.js lib/*.js img/*.png img/*.ico fonts/*
var FS embed.FS

var releaseVersion = computeReleaseVersion()

func ReleaseVersion() string {
	return releaseVersion
}

func VersionedBasePath() string {
	return "/static/" + releaseVersion
}

func VersionedPath(rel string) string {
	trimmed := strings.TrimLeft(strings.TrimSpace(rel), "/")
	if trimmed == "" {
		return VersionedBasePath()
	}
	return VersionedBasePath() + "/" + trimmed
}

func computeReleaseVersion() string {
	entries := make([]string, 0, 32)
	if err := fs.WalkDir(FS, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		entries = append(entries, name)
		return nil
	}); err != nil {
		return "dev"
	}
	sort.Strings(entries)
	h := sha256.New()
	for _, name := range entries {
		data, err := FS.ReadFile(name)
		if err != nil {
			return "dev"
		}
		_, _ = h.Write([]byte(path.Clean(name)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if len(sum) < 12 {
		return "dev"
	}
	return sum[:12]
}
