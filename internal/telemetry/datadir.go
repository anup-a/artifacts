package telemetry

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// diskSampleEvery bounds how often the data directory is measured. Walking
// the blob tree touches every file, so it is done at most once a minute
// however often the reader collects.
const diskSampleEvery = time.Minute

var sqliteFileKey = attribute.Key("byagent.sqlite.file") // db, wal

// ObserveDataDir reports the size of the SQLite database and of the local
// blob store as gauges. Pass "" for a part this server does not keep on
// local disk (DB=postgres, STORAGE=s3). It registers callbacks on the
// global MeterProvider, so call it after Start, and only when Enabled.
func ObserveDataDir(dbFile, blobDir string) error {
	if dbFile == "" && blobDir == "" {
		return nil
	}
	m := otel.Meter(scope)
	s := &diskSampler{dbFile: dbFile, blobDir: blobDir}
	var insts []metric.Observable
	var dbSize, blobSize, blobCount metric.Int64ObservableGauge
	var err error
	if dbFile != "" {
		dbSize, err = m.Int64ObservableGauge("byagent.sqlite.size", metric.WithUnit("By"),
			metric.WithDescription("Size on disk of the SQLite database file and its write-ahead log."))
		if err != nil {
			return err
		}
		insts = append(insts, dbSize)
	}
	if blobDir != "" {
		blobSize, err = m.Int64ObservableGauge("byagent.blobs.size", metric.WithUnit("By"),
			metric.WithDescription("Total size of the files in the local blob store."))
		if err != nil {
			return err
		}
		blobCount, err = m.Int64ObservableGauge("byagent.blobs.count", metric.WithUnit("{file}"),
			metric.WithDescription("Number of files in the local blob store."))
		if err != nil {
			return err
		}
		insts = append(insts, blobSize, blobCount)
	}
	_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		d := s.sample()
		if dbFile != "" {
			o.ObserveInt64(dbSize, d.db, metric.WithAttributes(sqliteFileKey.String("db")))
			o.ObserveInt64(dbSize, d.wal, metric.WithAttributes(sqliteFileKey.String("wal")))
		}
		if blobDir != "" {
			o.ObserveInt64(blobSize, d.blobBytes)
			o.ObserveInt64(blobCount, d.blobFiles)
		}
		return nil
	}, insts...)
	return err
}

type diskSample struct {
	db, wal, blobBytes, blobFiles int64
}

// diskSampler caches the last measurement for diskSampleEvery.
type diskSampler struct {
	dbFile, blobDir string

	mu   sync.Mutex
	at   time.Time
	last diskSample
}

func (s *diskSampler) sample() diskSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.at.IsZero() && time.Since(s.at) < diskSampleEvery {
		return s.last
	}
	var d diskSample
	if s.dbFile != "" {
		d.db = fileSize(s.dbFile)
		d.wal = fileSize(s.dbFile + "-wal")
	}
	if s.blobDir != "" {
		// A file that vanishes mid-walk (the GC deleting it) is skipped,
		// not an error: the next sample will be right.
		filepath.WalkDir(s.blobDir, func(_ string, e fs.DirEntry, err error) error {
			if err != nil || !e.Type().IsRegular() {
				return nil
			}
			if info, err := e.Info(); err == nil {
				d.blobBytes += info.Size()
				d.blobFiles++
			}
			return nil
		})
	}
	s.at, s.last = time.Now(), d
	return d
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
