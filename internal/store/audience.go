package store

import (
	"context"

	"zonenan-backend/internal/db"
)

// AudienceViewer is the authenticated identity used for release-state filtering.
type AudienceViewer struct {
	UserID int64
	// IsPremium is the active new membership entitlement used by content
	// audience filtering. Legacy beta_memberships is exposed separately.
	IsPremium bool
	// IsBeta remains a source-compatible legacy beta flag.
	IsBeta bool
	// IsReleaseBeta controls access to beta and rc application releases.
	IsReleaseBeta bool
	IsAdmin       bool
}

func (v AudienceViewer) CanSee(state string) bool {
	switch state {
	case "enabled":
		return true
	case "beta":
		return v.IsAdmin || v.IsBeta
	case "admin":
		return v.IsAdmin
	default:
		return false
	}
}

// BetaMembershipStore stores the internal-user-ID beta allowlist.
type BetaMembershipStore struct{ pool *db.Pool }

func NewBetaMembershipStore(p *db.Pool) *BetaMembershipStore { return &BetaMembershipStore{pool: p} }

func (s *BetaMembershipStore) IsMember(ctx context.Context, userID int64) bool {
	if userID <= 0 {
		return false
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM beta_memberships WHERE user_id=$1)`, userID).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func (s *BetaMembershipStore) SetMember(ctx context.Context, userID int64, member bool) error {
	if member {
		_, err := s.pool.Exec(ctx, `INSERT INTO beta_memberships(user_id) VALUES($1) ON CONFLICT(user_id) DO NOTHING`, userID)
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM beta_memberships WHERE user_id=$1`, userID)
	return err
}

func (s *BetaMembershipStore) List(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT user_id FROM beta_memberships ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
