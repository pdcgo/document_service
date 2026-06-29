package main

import (
	"context"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"github.com/pdcgo/document_service/document"
	"github.com/pdcgo/san_collection/san_caches"
	"github.com/pdcgo/shared/configs"
	"github.com/pdcgo/shared/db_connect"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

func NewStorageClient() (*storage.Client, error) {
	return storage.NewClient(context.Background())
}

// NewCacheManager backs the access interceptor's role cache. The standalone
// deployment skips caching (no Redis); the omnibus uses a Redis-backed manager.
func NewCacheManager() san_caches.CacheManager {
	return san_caches.NewSkipCacheManager()
}

func NewDatabase(cfg *configs.AppConfig) (*gorm.DB, error) {
	return db_connect.NewProductionDatabase("document_service", &cfg.Database)
}

func NewDocumentConfig(cfg *configs.AppConfig) document.Config {
	bucket := os.Getenv("RESOURCE_DIR")
	if bucket == "" {
		bucket = "gudang_assets_test"
	}
	return document.Config{
		Bucket:        bucket,
		TokenSecret:   []byte(cfg.JwtSecret),
		URLTTL:        15 * time.Minute,
		MaxSizeBytes:  10 << 20, // 10 MiB
		PublicBaseURL: os.Getenv("PUBLIC_ASSET_BASE_URL"),
	}
}

func NewApp(serviceApiFunc ServiceApiFunc) *cli.Command {
	return &cli.Command{
		Name:           "document",
		DefaultCommand: "run",
		Commands: []*cli.Command{
			{
				Name:   "run",
				Action: cli.ActionFunc(serviceApiFunc),
			},
		},
	}
}

func main() {
	app, err := InitializeApp()
	if err != nil {
		panic(err)
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		panic(err)
	}
}
