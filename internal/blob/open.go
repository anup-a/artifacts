package blob

import (
	"fmt"

	"github.com/anup-a/byagent/internal/config"
)

func Open(cfg config.Config) (Store, error) {
	switch cfg.Storage {
	case "local":
		return newLocal(cfg.DataDir)
	case "s3":
		return newS3(cfg.S3Endpoint, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3UseSSL)
	}
	return nil, fmt.Errorf("unknown STORAGE %q", cfg.Storage)
}
