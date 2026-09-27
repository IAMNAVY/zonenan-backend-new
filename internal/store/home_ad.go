package store

import (
	"context"
	"time"

	"zonenan-backend/internal/db"
)

// HomeAd 「我的」页广告位/运营位一条。
type HomeAd struct {
	ID           int64      `json:"id"`
	IconURL      string     `json:"icon_url"`
	Title        string     `json:"title"`
	Subtitle     string     `json:"subtitle"`
	Link         string     `json:"link"`
	OpenMode     string     `json:"open_mode"`
	Sort         int        `json:"sort"`
	Active       bool       `json:"active"`
	StartsAt     *time.Time `json:"starts_at,omitempty"`
	EndsAt       *time.Time `json:"ends_at,omitempty"`
	ReleaseState string     `json:"release_state"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// HomeAdStore 读写广告位。
type HomeAdStore struct{ pool *db.Pool }

func NewHomeAdStore(p *db.Pool) *HomeAdStore { return &HomeAdStore{pool: p} }

// Active 返回当前生效且对 viewer 可见的广告位。
func (s *HomeAdStore) Active(ctx context.Context, viewer AudienceViewer) ([]HomeAd, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, icon_url, title, subtitle, link, open_mode, sort, active,
		        starts_at, ends_at, release_state, updated_at
		   FROM home_ads
		  WHERE active = TRUE
		    AND (starts_at IS NULL OR starts_at <= NOW())
		    AND (ends_at   IS NULL OR ends_at   >= NOW())
		    AND (release_state = 'enabled'
		         OR (release_state = 'beta' AND $1)
		         OR (release_state = 'admin' AND $2))
		  ORDER BY sort DESC, id DESC`, viewer.IsBeta, viewer.IsAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHomeAds(rows)
}

// All 返回全部广告位(admin 用)。
func (s *HomeAdStore) All(ctx context.Context) ([]HomeAd, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, icon_url, title, subtitle, link, open_mode, sort, active,
		        starts_at, ends_at, release_state, updated_at
		   FROM home_ads ORDER BY sort DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHomeAds(rows)
}

func scanHomeAds(rows interface {
	Next() bool
	Scan(...any) error
}) ([]HomeAd, error) {
	out := []HomeAd{}
	for rows.Next() {
		var a HomeAd
		if err := rows.Scan(&a.ID, &a.IconURL, &a.Title, &a.Subtitle, &a.Link,
			&a.OpenMode, &a.Sort, &a.Active, &a.StartsAt, &a.EndsAt,
			&a.ReleaseState, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Upsert 新增(ID=0)或更新,返回 ID。
func (s *HomeAdStore) Upsert(ctx context.Context, a HomeAd) (int64, error) {
	if a.ID == 0 {
		err := s.pool.QueryRow(ctx,
			`INSERT INTO home_ads(icon_url, title, subtitle, link, open_mode, sort, active, starts_at, ends_at, release_state)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
			a.IconURL, a.Title, a.Subtitle, a.Link, a.OpenMode, a.Sort, a.Active,
			a.StartsAt, a.EndsAt, normalizedReleaseState(a.ReleaseState)).Scan(&a.ID)
		return a.ID, err
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE home_ads SET icon_url=$2, title=$3, subtitle=$4, link=$5, open_mode=$6,
		   sort=$7, active=$8, starts_at=$9, ends_at=$10, release_state=$11, updated_at=NOW()
		 WHERE id=$1`,
		a.ID, a.IconURL, a.Title, a.Subtitle, a.Link, a.OpenMode, a.Sort, a.Active,
		a.StartsAt, a.EndsAt, normalizedReleaseState(a.ReleaseState))
	return a.ID, err
}

// Delete 删除广告位。
func (s *HomeAdStore) Delete(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM home_ads WHERE id=$1`, id)
	return err
}
