package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"
	"zonenan-backend/internal/db"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("DATABASE_URL is required")
	}
	p, e := db.Connect(ctx, url)
	if e != nil {
		log.Fatal(e)
	}
	defer p.Close()
	result := map[string]any{"database_reachable": true}
	var version string
	p.QueryRow(ctx, `SHOW server_version`).Scan(&version)
	var postgisAvailable bool
	_ = p.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_available_extensions WHERE name='postgis')`).Scan(&postgisAvailable)
	result["postgres_version"] = version
	result["postgis_extension_available"] = postgisAvailable
	var legacy, admins int
	p.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('zonenan_users','campus_map_places','app_settings')`).Scan(&legacy)
	p.QueryRow(ctx, `SELECT count(*) FROM admin_users`).Scan(&admins)
	result["legacy_core_tables"] = legacy
	result["admin_count"] = admins
	result["ready"] = legacy == 3 && postgisAvailable
	json.NewEncoder(os.Stdout).Encode(result)
	if result["ready"] != true {
		os.Exit(2)
	}
}
