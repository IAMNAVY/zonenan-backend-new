package store

import (
	"context"
	"time"

	"zonenan-backend/internal/db"
)

// Announcement 公告(三形态:modal | status_card | mine_section)。
type Announcement struct {
	ID               int64      `json:"id"`
	Kind             string     `json:"kind"`
	Level            string     `json:"level"`
	Title            string     `json:"title"`
	Body             string     `json:"body"`
	ForceReadSeconds int        `json:"force_read_seconds"`
	Dismissible      bool       `json:"dismissible"`
	Sort             int        `json:"sort"`
	Active           bool       `json:"active"`
	StartsAt         *time.Time `json:"starts_at,omitempty"`
	EndsAt           *time.Time `json:"ends_at,omitempty"`
	ReleaseState     string     `json:"release_state"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// AnnouncementStore 读写公告。
type AnnouncementStore struct{ pool *db.Pool }

func NewAnnouncementStore(p *db.Pool) *AnnouncementStore { return &AnnouncementStore{pool: p} }

// ActiveByKind 返回当前生效且对 viewer 可见的公告。
func (s *AnnouncementStore) ActiveByKind(ctx context.Context, kind string, viewer AudienceViewer) ([]Announcement, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, kind, level, title, body, force_read_seconds, dismissible, sort,
		        active, starts_at, ends_at, release_state, updated_at
		   FROM announcements
		  WHERE active = TRUE
		    AND ($1 = '' OR kind = $1)
		    AND (starts_at IS NULL OR starts_at <= NOW())
		    AND (ends_at   IS NULL OR ends_at   >= NOW())
		    AND (release_state = 'enabled'
		         OR (release_state = 'beta' AND $2)
		         OR (release_state = 'admin' AND $3))
		  ORDER BY sort DESC, id DESC`, kind, viewer.IsBeta, viewer.IsAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnnouncements(rows)
}

// All 返回全部公告(admin 用)。
func (s *AnnouncementStore) All(ctx context.Context) ([]Announcement, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, kind, level, title, body, force_read_seconds, dismissible, sort,
		        active, starts_at, ends_at, release_state, updated_at
		   FROM announcements ORDER BY sort DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnnouncements(rows)
}

func scanAnnouncements(rows interface {
	Next() bool
	Scan(...any) error
}) ([]Announcement, error) {
	out := []Announcement{}
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ID, &a.Kind, &a.Level, &a.Title, &a.Body,
			&a.ForceReadSeconds, &a.Dismissible, &a.Sort, &a.Active,
			&a.StartsAt, &a.EndsAt, &a.ReleaseState, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Upsert 新增(ID=0)或更新公告,返回 ID。
func (s *AnnouncementStore) Upsert(ctx context.Context, a Announcement) (int64, error) {
	if a.ID == 0 {
		err := s.pool.QueryRow(ctx,
			`INSERT INTO announcements(kind, level, title, body, force_read_seconds,
			   dismissible, sort, active, starts_at, ends_at, release_state)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
			a.Kind, a.Level, a.Title, a.Body, a.ForceReadSeconds,
			a.Dismissible, a.Sort, a.Active, a.StartsAt, a.EndsAt, normalizedReleaseState(a.ReleaseState)).Scan(&a.ID)
		return a.ID, err
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE announcements SET kind=$2, level=$3, title=$4, body=$5,
		   force_read_seconds=$6, dismissible=$7, sort=$8, active=$9,
		   starts_at=$10, ends_at=$11, release_state=$12, updated_at=NOW()
		 WHERE id=$1`,
		a.ID, a.Kind, a.Level, a.Title, a.Body, a.ForceReadSeconds,
		a.Dismissible, a.Sort, a.Active, a.StartsAt, a.EndsAt, normalizedReleaseState(a.ReleaseState))
	return a.ID, err
}

func normalizedReleaseState(state string) string {
	if state == "beta" || state == "disabled" || state == "admin" {
		return state
	}
	return "enabled"
}

// Delete 删除公告。
func (s *AnnouncementStore) Delete(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM announcements WHERE id=$1`, id)
	return err
}
