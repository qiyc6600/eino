package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"sort"
	"sync"
)

//go:embed index.html assets/*
var StaticFS embed.FS

// AssetVersionPlaceholder is replaced in index.html with the running build's
// version, so a page can tell whether the files it is executing are still the
// ones the server is serving.
const AssetVersionPlaceholder = "{{ASSET_VERSION}}"

var (
	versionOnce sync.Once
	version     string
)

// AssetVersion is a content hash of the embedded UI.
//
// It is derived from the bytes rather than from a build stamp on purpose: the
// question it answers is "did the files change", and a timestamp would report a
// change on every rebuild of identical content.
func AssetVersion() string {
	versionOnce.Do(func() {
		// Sorted, so the hash does not depend on walk order.
		var names []string
		_ = fs.WalkDir(StaticFS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			names = append(names, path)
			return nil
		})
		sort.Strings(names)

		h := sha256.New()
		for _, name := range names {
			data, err := StaticFS.ReadFile(name)
			if err != nil {
				continue
			}
			h.Write([]byte(name))
			h.Write([]byte{0})
			h.Write(data)
			h.Write([]byte{0})
		}
		version = hex.EncodeToString(h.Sum(nil))[:12]
	})
	return version
}
