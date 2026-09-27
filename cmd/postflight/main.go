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
	out := map[string]any{}
	var migrations, legacy, mapped, orphans, badOwners int64
	p.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&migrations)
	p.QueryRow(ctx, `SELECT count(*) FROM campus_map_places`).Scan(&legacy)
	p.QueryRow(ctx, `SELECT count(*) FROM pois WHERE legacy_place_id IS NOT NULL`).Scan(&mapped)
	p.QueryRow(ctx, `SELECT count(*) FROM pois p WHERE p.building_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM pois b WHERE b.id=p.building_id)`).Scan(&orphans)
	p.QueryRow(ctx, `SELECT count(*) FROM rental_listings WHERE owner_user_id IS NULL AND merchant_id IS NULL`).Scan(&badOwners)
	out["migration_count"] = migrations
	out["legacy_places"] = legacy
	out["mapped_pois"] = mapped
	out["orphan_poi_references"] = orphans
	out["ownerless_rentals"] = badOwners
	out["ready"] = mapped == legacy && orphans == 0 && badOwners == 0
	json.NewEncoder(os.Stdout).Encode(out)
	if out["ready"] != true {
		os.Exit(2)
	}
}
