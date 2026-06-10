package archive

import (
	"encoding/json"
	"os"
	"syscall"

	ahaclock "github.com/adewale/aha/internal/clock"
	"github.com/adewale/aha/internal/fileutil"
	"github.com/adewale/aha/internal/model"
)

// CaptureCache is the advisory scan cache from docs/depot-v2-spec.md: a
// (path, size, mtime_ns, inode) -> sha256 map that lets capture skip
// re-reading files that look unchanged. It is a work-skipping hint, never
// a correctness input — a wiped or corrupt cache means one slow full
// re-hash, and every consumer offers --force to bypass it.
//
// Entries follow git's racy-mtime rule: a hit additionally requires the
// file's mtime to be strictly older than the cache's write time, so a
// modification in the same instant the cache was written can never be
// masked.
type CaptureCache struct {
	path      string
	clk       ahaclock.Clock
	writtenAt int64
	entries   map[string]captureCacheEntry
}

const captureCacheSchema = "aha-capture-cache/v1"

type captureCacheEntry struct {
	Size    int64  `json:"size"`
	MtimeNS int64  `json:"mtime_ns"`
	Inode   uint64 `json:"inode,omitempty"`
	SHA256  string `json:"sha256"`
}

type captureCacheFile struct {
	Schema      string                       `json:"schema"`
	WrittenAtNS int64                        `json:"written_at_ns"`
	Entries     map[string]captureCacheEntry `json:"entries"`
}

// LoadCaptureCache reads the cache at path. A missing, corrupt, or
// wrong-schema file yields an empty cache (self-healing), never an error.
func LoadCaptureCache(path string, clk ahaclock.Clock) *CaptureCache {
	c := &CaptureCache{path: path, clk: clk, entries: map[string]captureCacheEntry{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var f captureCacheFile
	if json.Unmarshal(b, &f) != nil || f.Schema != captureCacheSchema || f.Entries == nil {
		return c
	}
	c.writtenAt = f.WrittenAtNS
	c.entries = f.Entries
	return c
}

// Lookup returns the cached content hash for path if the stat data
// matches and the entry is outside the racy-mtime window.
func (c *CaptureCache) Lookup(path string, st os.FileInfo) (string, bool) {
	e, ok := c.entries[path]
	if !ok {
		return "", false
	}
	if st.Size() != e.Size || st.ModTime().UnixNano() != e.MtimeNS {
		return "", false
	}
	if ino := statInode(st); ino != 0 && e.Inode != 0 && ino != e.Inode {
		return "", false
	}
	if st.ModTime().UnixNano() >= c.writtenAt {
		return "", false // racy window: file as new as the cache itself
	}
	if _, err := model.ParseSHA256Hex(e.SHA256); err != nil {
		return "", false
	}
	return e.SHA256, true
}

// Record remembers the content hash observed for path at stat time st.
func (c *CaptureCache) Record(path string, st os.FileInfo, sha string) {
	c.entries[path] = captureCacheEntry{Size: st.Size(), MtimeNS: st.ModTime().UnixNano(), Inode: statInode(st), SHA256: sha}
}

// Save atomically writes the cache, stamping the write time the racy
// rule compares against.
func (c *CaptureCache) Save() error {
	f := captureCacheFile{Schema: captureCacheSchema, WrittenAtNS: c.clk.Now().UnixNano(), Entries: c.entries}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteBytes(c.path, append(b, '\n'), fileutil.AtomicOptions{TempPattern: ".tmp-cache-*.json"})
}

func statInode(st os.FileInfo) uint64 {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return sys.Ino
	}
	return 0
}
