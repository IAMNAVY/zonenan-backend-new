package main

import (
	"context"
	"log"
	"os"
	"time"
	"zonenan-backend/internal/db"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
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
	tag, e := p.Exec(ctx, `INSERT INTO pois(type,category,name,aliases,latitude,longitude,campus_id,address,description,coordinate_system,sort_order,status,source,legacy_place_id,verified_level,verified_at,created_at,updated_at) SELECT COALESCE(NULLIF(place_types[1],''),place_type),category,name,aliases,latitude,longitude,campus_id,address,description,coordinate_system,sort,CASE WHEN active THEN 'active' ELSE 'disabled' END,'campus_map',id,2,updated_at,created_at,updated_at FROM campus_map_places ON CONFLICT(legacy_place_id) DO UPDATE SET name=EXCLUDED.name,aliases=EXCLUDED.aliases,latitude=EXCLUDED.latitude,longitude=EXCLUDED.longitude,address=EXCLUDED.address,description=EXCLUDED.description,coordinate_system=EXCLUDED.coordinate_system,sort_order=EXCLUDED.sort_order,updated_at=EXCLUDED.updated_at`)
	if e != nil {
		log.Fatal(e)
	}
	log.Printf("poi backfill upserted %d rows", tag.RowsAffected())
}
