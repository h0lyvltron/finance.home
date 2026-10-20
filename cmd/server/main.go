package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"finance.home/internal/config"
	"finance.home/internal/db"
	"finance.home/internal/handlers"
	"finance.home/internal/store"
	"finance.home/internal/web"
)

func main() {
	cfg := config.Load()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}

	sqlDB, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()

	if err := db.MigrateWithBackup(sqlDB, filepath.Join(cfg.DataDir, "backups")); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if undoID, changed, err := store.New(sqlDB).RepairTransferOutliers(0); err != nil {
		log.Printf("transfer repair: %v", err)
	} else if changed {
		log.Printf("repaired card-payment links (undo %s)", undoID)
	}

	renderer, err := web.NewRenderer()
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	app := &handlers.App{
		DB:     sqlDB,
		Store:  store.New(sqlDB),
		Render: renderer,
		Cfg:    cfg,
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           logging(app.Routes()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("household finance listening on http://127.0.0.1%s (db=%s)", cfg.Addr, cfg.DBPath)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Truncate(time.Millisecond))
	})
}
