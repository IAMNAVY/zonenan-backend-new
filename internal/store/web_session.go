package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	"zonenan-backend/internal/db"
)

type WebSessionStore struct{ pool *db.Pool }

func NewWebSessionStore(pool *db.Pool) *WebSessionStore { return &WebSessionStore{pool: pool} }

func newRefreshToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

func (s *WebSessionStore) Create(ctx context.Context, userID int64, device string, remember bool, ttl time.Duration) (string, string, time.Time, error) {
	idRaw := make([]byte, 16)
	if _, err := rand.Read(idRaw); err != nil {
		return "", "", time.Time{}, err
	}
	id := base64.RawURLEncoding.EncodeToString(idRaw)
	token, hash, err := newRefreshToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	expires := time.Now().Add(ttl)
	_, err = s.pool.Exec(ctx, `INSERT INTO web_sessions(id,user_id,refresh_token_hash,device_name,remember_me,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, id, userID, hash, device, remember, expires)
	return id, token, expires, err
}

func (s *WebSessionStore) Rotate(ctx context.Context, token string) (string, int64, string, time.Time, error) {
	hash := sha256.Sum256([]byte(token))
	next, nextHash, err := newRefreshToken()
	if err != nil {
		return "", 0, "", time.Time{}, err
	}
	var id string
	var uid int64
	var expires time.Time
	err = s.pool.QueryRow(ctx, `UPDATE web_sessions SET refresh_token_hash=$2,last_used_at=NOW() WHERE refresh_token_hash=$1 AND revoked_at IS NULL AND expires_at>NOW() RETURNING id,user_id,expires_at`, hash[:], nextHash).Scan(&id, &uid, &expires)
	return id, uid, next, expires, err
}

func (s *WebSessionStore) Active(ctx context.Context, id string, userID int64) bool {
	var ok bool
	_ = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM web_sessions WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>NOW())`, id, userID).Scan(&ok)
	return ok
}

func (s *WebSessionStore) Revoke(ctx context.Context, id string, userID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE web_sessions SET revoked_at=COALESCE(revoked_at,NOW()) WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}
