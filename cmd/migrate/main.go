package main

import (
	"context"
	"log"
	"os"
	"time"

	"zonenan-backend/internal/db"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		log.Fatalf("database connection: %v", err)
	}
	defer pool.Close()

	if err := pool.Migrate(ctx); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	log.Print("migrations applied successfully")
}
