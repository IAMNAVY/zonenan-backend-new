package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"zonenan-backend/internal/db"
)

type job struct {
	name string
	sql  string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := db.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	jobs := []job{
		{"rental_expiry", `UPDATE rental_listings SET status='expired',updated_at=NOW() WHERE status='published' AND expires_at<=NOW()`},
		{"session_cleanup", `WITH a AS (DELETE FROM admin_sessions WHERE expires_at<NOW()-INTERVAL '7 days' RETURNING 1),m AS (DELETE FROM merchant_sessions WHERE expires_at<NOW()-INTERVAL '7 days' RETURNING 1),w AS (DELETE FROM web_sessions WHERE expires_at<NOW()-INTERVAL '7 days' RETURNING 1) SELECT count(*) FROM (SELECT * FROM a UNION ALL SELECT * FROM m UNION ALL SELECT * FROM w) x`},
		{"code_cleanup", `DELETE FROM email_codes WHERE expires_at<NOW()-INTERVAL '1 day'`},
		{"merchant_metrics", `INSERT INTO merchant_metrics_daily(merchant_id,date,views,favorites,contacts,map_impressions,search_impressions) SELECT merchant_id,(occurred_at AT TIME ZONE 'Asia/Shanghai')::date,count(*) FILTER(WHERE event_type='view'),count(*) FILTER(WHERE event_type='favorite'),count(*) FILTER(WHERE event_type='contact'),count(*) FILTER(WHERE event_type='map_impression'),count(*) FILTER(WHERE event_type='search_impression') FROM merchant_events WHERE occurred_at>=CURRENT_DATE-INTERVAL '2 days' GROUP BY merchant_id,(occurred_at AT TIME ZONE 'Asia/Shanghai')::date ON CONFLICT(merchant_id,date) DO UPDATE SET views=EXCLUDED.views,favorites=EXCLUDED.favorites,contacts=EXCLUDED.contacts,map_impressions=EXCLUDED.map_impressions,search_impressions=EXCLUDED.search_impressions`},
		{"rental_renewal_reminders", `INSERT INTO notification_outbox(recipient_type,recipient_id,event_type,payload,dedupe_key) SELECT CASE WHEN owner_user_id IS NOT NULL THEN 'user' ELSE 'merchant' END,COALESCE(owner_user_id,merchant_id),'rental.expiring',jsonb_build_object('listing_id',id,'expires_at',expires_at),'rental-expiring:'||id||':'||expires_at::date FROM rental_listings WHERE status='published' AND expires_at>=NOW() AND expires_at<NOW()+INTERVAL '3 days' ON CONFLICT DO NOTHING`},
		{"event_retention", `DELETE FROM merchant_events WHERE occurred_at<NOW()-INTERVAL '180 days'`},
		{"analytics_retention", `DELETE FROM analytics_events WHERE occurred_at<NOW()-INTERVAL '400 days'`},
	}
	run := func() {
		bucket := time.Now().UTC().Truncate(15 * time.Minute)
		for _, item := range jobs {
			key := item.name + ":" + bucket.Format(time.RFC3339)
			var runID int64
			err := pool.QueryRow(ctx, `INSERT INTO job_runs(job_name,run_key,scheduled_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING RETURNING id`, item.name, key, bucket).Scan(&runID)
			if err != nil {
				continue
			}
			var locked bool
			_ = pool.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, item.name).Scan(&locked)
			if !locked {
				pool.Exec(ctx, `UPDATE job_runs SET status='skipped',finished_at=NOW() WHERE id=$1`, runID)
				continue
			}
			tag, execErr := pool.Exec(ctx, item.sql)
			pool.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, item.name)
			if execErr != nil {
				pool.Exec(ctx, `UPDATE job_runs SET status='failed',error_summary=$1,finished_at=NOW() WHERE id=$2`, execErr.Error(), runID)
				log.Printf("job %s failed: %v", item.name, execErr)
				continue
			}
			pool.Exec(ctx, `UPDATE job_runs SET status='succeeded',processed_count=$1,finished_at=NOW() WHERE id=$2`, tag.RowsAffected(), runID)
		}
	}
	run()
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
