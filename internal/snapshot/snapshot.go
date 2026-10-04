// Package snapshot packs a sqlite + local-disk deployment into one .tar.gz
// that a new box can be started from: the database, copied consistently with
// VACUUM INTO while the server keeps running, and every blob under blobs/.
// Unpacking it into an empty DATA_DIR restores the service as it was.
package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// DBName is the database file inside DATA_DIR and inside the archive.
const DBName = "artifacts.db"

// Write streams the archive of dataDir to w. It only reads the live
// database, so it is safe beside a running server; turn on READ_ONLY first
// when no write may be lost.
func Write(ctx context.Context, dataDir string, w io.Writer) error {
	live := filepath.Join(dataDir, DBName)
	if _, err := os.Stat(live); err != nil {
		return fmt.Errorf("no database to snapshot: %w", err)
	}
	tmp, err := os.CreateTemp(dataDir, ".snapshot-*.db")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	// VACUUM INTO refuses to overwrite a file, even an empty one.
	os.Remove(tmpPath)
	defer os.Remove(tmpPath)

	db, err := sql.Open("sqlite", "file:"+live+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "VACUUM INTO ?", tmpPath)
	db.Close()
	if err != nil {
		return fmt.Errorf("copy database: %w", err)
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := addFile(tw, tmpPath, DBName); err != nil {
		return err
	}
	blobs := filepath.Join(dataDir, "blobs")
	err = filepath.WalkDir(blobs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// A half-written upload is not a blob yet.
		if d.IsDir() || strings.HasPrefix(d.Name(), ".tmp-") {
			return nil
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		return addFile(tw, path, filepath.ToSlash(rel))
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func addFile(tw *tar.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}
