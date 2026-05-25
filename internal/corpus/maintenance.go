package corpus

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type SizeReport struct {
	Root            string `json:"root"`
	TotalBytes      int64  `json:"total_bytes"`
	DatabaseBytes   int64  `json:"database_bytes"`
	BundleBlobBytes int64  `json:"bundle_blob_bytes"`
	FileBlobBytes   int64  `json:"file_blob_bytes"`
	ImageBlobBytes  int64  `json:"image_blob_bytes"`
	OtherBytes      int64  `json:"other_bytes"`
	Files           int    `json:"files"`
}

type OrphanBlob struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Bytes int64  `json:"bytes"`
}

type PruneOrphansReport struct {
	Root         string       `json:"root"`
	DryRun       bool         `json:"dry_run"`
	Orphans      []OrphanBlob `json:"orphans,omitempty"`
	OrphanBytes  int64        `json:"orphan_bytes"`
	DeletedFiles int          `json:"deleted_files"`
	DeletedBytes int64        `json:"deleted_bytes"`
}

func Size(store *Store) (SizeReport, error) {
	report := SizeReport{Root: filepath.Clean(store.Root)}
	err := filepath.WalkDir(store.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size := info.Size()
		report.TotalBytes += size
		report.Files++
		rel, _ := filepath.Rel(store.Root, path)
		rel = filepath.ToSlash(rel)
		switch {
		case strings.HasSuffix(rel, "corpus.sqlite") || strings.HasPrefix(rel, "corpus.sqlite-"):
			report.DatabaseBytes += size
		case strings.HasPrefix(rel, "blobs/bundles/"):
			report.BundleBlobBytes += size
		case strings.HasPrefix(rel, "blobs/files/"):
			report.FileBlobBytes += size
		case strings.HasPrefix(rel, "blobs/images/"):
			report.ImageBlobBytes += size
		default:
			report.OtherBytes += size
		}
		return nil
	})
	return report, err
}

func Vacuum(db *sql.DB) error {
	_, err := db.Exec(`vacuum`)
	return err
}

func PruneOrphanBlobs(store *Store, force bool) (PruneOrphansReport, error) {
	referenced, err := referencedBlobPaths(store.DB)
	if err != nil {
		return PruneOrphansReport{}, err
	}
	report := PruneOrphansReport{Root: filepath.Clean(store.Root), DryRun: !force}
	for _, base := range []struct{ rel, kind string }{
		{filepath.ToSlash(filepath.Join("blobs", "bundles")), "bundle"},
		{filepath.ToSlash(filepath.Join("blobs", "files")), "file"},
		{filepath.ToSlash(filepath.Join("blobs", "images")), "image"},
	} {
		root := filepath.Join(store.Root, filepath.FromSlash(base.rel))
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(store.Root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if referenced[rel] {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			orphan := OrphanBlob{Path: rel, Kind: base.kind, Bytes: info.Size()}
			report.Orphans = append(report.Orphans, orphan)
			report.OrphanBytes += orphan.Bytes
			if force {
				if err := os.Remove(path); err != nil {
					return err
				}
				report.DeletedFiles++
				report.DeletedBytes += orphan.Bytes
			}
			return nil
		}); err != nil {
			return report, err
		}
	}
	return report, nil
}

func referencedBlobPaths(db *sql.DB) (map[string]bool, error) {
	refs := map[string]bool{}
	rows, err := db.Query(`select bundle_sha256 from bundles where bundle_sha256 is not null and bundle_sha256<>''`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			_ = rows.Close()
			return nil, err
		}
		refs[filepath.ToSlash(filepath.Join("blobs", "bundles", sha+".tar.zst"))] = true
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, query := range []string{
		`select compressed_blob_path from files where compressed_blob_path is not null and compressed_blob_path<>''`,
		`select blob_path from images where blob_path is not null and blob_path<>''`,
	} {
		rows, err := db.Query(query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var rel string
			if err := rows.Scan(&rel); err != nil {
				_ = rows.Close()
				return nil, err
			}
			refs[filepath.ToSlash(rel)] = true
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return refs, nil
}
