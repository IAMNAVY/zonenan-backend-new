package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

// ReserveCampusClaim registers the stable account without giving the claimant
// ownership. Untrusted names and devices must not alter this account.
func (s *UserStore) ReserveCampusClaim(ctx context.Context, studentHash string) (*User, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, studentHash); err != nil {
		return nil, false, err
	}
	var uid int64
	err = tx.QueryRow(ctx, `SELECT user_id FROM zonenan_identities WHERE provider='cas' AND provider_uid=$1`, studentHash).Scan(&uid)
	isNew := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !isNew {
		return nil, false, err
	}
	if isNew {
		if err = tx.QueryRow(ctx, `INSERT INTO zonenan_users(nickname) VALUES ('中南同学') RETURNING id`).Scan(&uid); err != nil {
			return nil, false, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO zonenan_identities(user_id,provider,provider_uid,verified,display_name) VALUES ($1,'cas',$2,FALSE,'')`, uid, studentHash); err != nil {
			// A legacy verified login can race the reservation. Its existing account wins.
			tx.Rollback(ctx)
			if isUniqueViolation(err) {
				u, lookupErr := s.GetByStudentHash(ctx, studentHash)
				return u, false, lookupErr
			}
			return nil, false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	u, err := s.GetByID(ctx, uid)
	return u, isNew, err
}
