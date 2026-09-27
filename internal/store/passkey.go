package store

import (
	"context"

	"zonenan-backend/internal/db"
)

// PasskeyCredential 存储一个 WebAuthn 公钥凭证。
type PasskeyCredential struct {
	ID              int64    `json:"id"`
	UserID          int64    `json:"user_id"`
	CredentialID    []byte   `json:"credential_id"`
	PublicKey       []byte   `json:"public_key"`
	AttestationType string   `json:"attestation_type"`
	AAGUID          []byte   `json:"aaguid"`
	SignCount       uint32   `json:"sign_count"`
	Transports      []string `json:"transports"`
	Name            string   `json:"name"`
}

// PasskeyStore 负责 passkey 凭证持久化。
type PasskeyStore struct{ pool *db.Pool }

func NewPasskeyStore(p *db.Pool) *PasskeyStore { return &PasskeyStore{pool: p} }

// Create 保存新凭证。
func (s *PasskeyStore) Create(ctx context.Context, c *PasskeyCredential) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO passkey_credentials(user_id, credential_id, public_key,
		   attestation_type, aaguid, sign_count, transports, name)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		c.UserID, c.CredentialID, c.PublicKey,
		c.AttestationType, c.AAGUID, c.SignCount, c.Transports, c.Name)
	return err
}

// ListByUser 取用户所有 passkey 凭证。
func (s *PasskeyStore) ListByUser(ctx context.Context, userID int64) ([]PasskeyCredential, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, credential_id, public_key, attestation_type,
		        aaguid, sign_count, transports, name
		   FROM passkey_credentials WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PasskeyCredential
	for rows.Next() {
		var c PasskeyCredential
		if err := rows.Scan(&c.ID, &c.UserID, &c.CredentialID, &c.PublicKey,
			&c.AttestationType, &c.AAGUID, &c.SignCount, &c.Transports, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// FindByCredentialID 按凭证ID查找(认证时用)。
func (s *PasskeyStore) FindByCredentialID(ctx context.Context, credID []byte) (*PasskeyCredential, error) {
	var c PasskeyCredential
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, credential_id, public_key, attestation_type,
		        aaguid, sign_count, transports, name
		   FROM passkey_credentials WHERE credential_id=$1`, credID,
	).Scan(&c.ID, &c.UserID, &c.CredentialID, &c.PublicKey,
		&c.AttestationType, &c.AAGUID, &c.SignCount, &c.Transports, &c.Name)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateSignCount 更新凭证使用计数。
func (s *PasskeyStore) UpdateSignCount(ctx context.Context, credID []byte, newCount uint32) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE passkey_credentials SET sign_count=$1, last_used_at=NOW() WHERE credential_id=$2`,
		newCount, credID)
	return err
}

// Delete 删除凭证。
func (s *PasskeyStore) Delete(ctx context.Context, id int64, userID int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM passkey_credentials WHERE id=$1 AND user_id=$2`, id, userID)
	return err
}
