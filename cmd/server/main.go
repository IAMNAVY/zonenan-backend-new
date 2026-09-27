package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"zonenan-backend/internal/adminauth"
	"zonenan-backend/internal/config"
	"zonenan-backend/internal/db"
	"zonenan-backend/internal/httpapi"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	if err := pool.Migrate(ctx); err != nil {
		log.Fatalf("db migrate: %v", err)
	}
	if err := adminauth.EnsureBootstrap(ctx, pool, cfg.AdminBootstrapEmail, cfg.AdminBootstrapPassword, cfg.AdminBootstrapName); err != nil {
		log.Fatalf("admin bootstrap: %v", err)
	}
	log.Printf("migrations applied")
	go runAnalyticsCleanup(ctx, pool)

	srv := httpapi.New(cfg, pool)
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("zonenan-backend listening on :%s (env=%s)", cfg.Port, cfg.Env)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	// 优雅关闭。
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	cancel()
	log.Printf("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

func runAnalyticsCleanup(ctx context.Context, pool *db.Pool) {
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		var deleted int64
		if err := pool.QueryRow(cleanupCtx, `SELECT cleanup_analytics_events()`).Scan(&deleted); err != nil {
			if ctx.Err() == nil {
				log.Printf("analytics cleanup: %v", err)
			}
			return
		}
		if deleted > 0 {
			log.Printf("analytics cleanup removed %d expired events", deleted)
		}
	}
	cleanup()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}
