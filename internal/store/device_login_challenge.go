package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"zonenan-backend/internal/auth"
	"zonenan-backend/internal/db"
)

const (
	ChallengePending  = "pending"
	ChallengeApproved = "approved"
	ChallengeRejected = "rejected"
	ChallengeConsumed = "consumed"
)

var (
	ErrChallengeInvalid  = errors.New("设备登录请求不存在或凭据无效")
	ErrChallengeExpired  = errors.New("设备登录请求已过期")
	ErrChallengePending  = errors.New("设备登录请求尚未批准")
	ErrChallengeRejected = errors.New("设备登录请求已拒绝")
	ErrChallengeConsumed = errors.New("设备登录请求已使用")
	ErrChallengeState    = errors.New("设备登录请求状态已变更")
)

// DeviceLoginChallenge binds an approved login to exactly one campus identity
// and target device. SecretHash is deliberately excluded from JSON responses.
type DeviceLoginChallenge struct {
	ID                      string     `json:"challenge_id"`
	StudentHash             string     `json:"-"`
	TargetDeviceFingerprint string     `json:"target_device_fingerprint"`
	TargetDeviceName        string     `json:"target_device_name"`
	SecretHash              []byte     `json:"-"`
	Status                  string     `json:"status"`
	ExpiresAt               time.Time  `json:"expires_at"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	ApprovedAt              *time.Time `json:"approved_at,omitempty"`
	ConsumedAt              *time.Time `json:"consumed_at,omitempty"`
}

// EffectiveStatus reports expiry without introducing an out-of-schema stored status.
func (c *DeviceLoginChallenge) EffectiveStatus(now time.Time) string {
	if now.After(c.ExpiresAt) && c.Status != ChallengeConsumed && c.Status != ChallengeRejected {
		return "expired"
	}
	return c.Status
}

type DeviceLoginChallengeStore struct{ pool *db.Pool }

func NewDeviceLoginChallengeStore(p *db.Pool) *DeviceLoginChallengeStore {
	return &DeviceLoginChallengeStore{pool: p}
}

// Create invalidates older pending requests for the same target and persists only
// the SHA-256 digest of the newly generated secret.
func (s *DeviceLoginChallengeStore) Create(ctx context.Context, studentHash, deviceFP, deviceName string, ttl time.Duration) (*DeviceLoginChallenge, string, error) {
	challengeID, err := auth.RandomOpaqueToken(16)
	if err != nil {
		return nil, "", err
	}
	secret, err := auth.RandomOpaqueToken(32)
	if err != nil {
		return nil, "", err
	}
	digest := auth.OpaqueSecretHash(secret)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE device_login_challenges
		    SET status='rejected', updated_at=NOW()
		  WHERE student_hash=$1 AND target_device_fingerprint=$2
		    AND status='pending'`,
		studentHash, deviceFP,
	); err != nil {
		return nil, "", err
	}

	var c DeviceLoginChallenge
	err = tx.QueryRow(ctx,
		`INSERT INTO device_login_challenges(
		   challenge_id, student_hash, target_device_fingerprint,
		   target_device_name, secret_hash, expires_at)
		 VALUES ($1,$2,$3,$4,$5,NOW()+$6::interval)
		 RETURNING challenge_id, student_hash, target_device_fingerprint,
		           target_device_name, secret_hash, status, expires_at,
		           created_at, updated_at, approved_at, consumed_at`,
		challengeID, studentHash, deviceFP, deviceName, digest[:], ttl.String(),
	).Scan(
		&c.ID, &c.StudentHash, &c.TargetDeviceFingerprint, &c.TargetDeviceName,
		&c.SecretHash, &c.Status, &c.ExpiresAt, &c.CreatedAt, &c.UpdatedAt,
		&c.ApprovedAt, &c.ConsumedAt,
	)
	if err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return &c, secret, nil
}

type challengeRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadChallenge(ctx context.Context, q challengeRowQuerier, challengeID string, forUpdate bool) (*DeviceLoginChallenge, error) {
	query := `SELECT challenge_id, student_hash, target_device_fingerprint,
	                 target_device_name, secret_hash, status, expires_at,
	                 created_at, updated_at, approved_at, consumed_at
	            FROM device_login_challenges WHERE challenge_id=$1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var c DeviceLoginChallenge
	err := q.QueryRow(ctx, query, challengeID).Scan(
		&c.ID, &c.StudentHash, &c.TargetDeviceFingerprint, &c.TargetDeviceName,
		&c.SecretHash, &c.Status, &c.ExpiresAt, &c.CreatedAt, &c.UpdatedAt,
		&c.ApprovedAt, &c.ConsumedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrChallengeInvalid
	}
	return &c, err
}

func authenticateChallenge(c *DeviceLoginChallenge, secret string) error {
	if c == nil || !auth.OpaqueSecretMatches(secret, c.SecretHash) {
		return ErrChallengeInvalid
	}
	return nil
}

func challengeBelongsToStudent(c *DeviceLoginChallenge, studentHash string) bool {
	return c != nil && c.StudentHash == studentHash
}

func stateError(c *DeviceLoginChallenge, required string, now time.Time) error {
	// Terminal states remain observable after expiry so replay/rejection is not
	// misclassified as an ordinary timeout.
	switch c.Status {
	case ChallengeRejected:
		return ErrChallengeRejected
	case ChallengeConsumed:
		return ErrChallengeConsumed
	}
	if now.After(c.ExpiresAt) {
		return ErrChallengeExpired
	}
	if c.Status == required {
		return nil
	}
	if c.Status == ChallengePending {
		return ErrChallengePending
	}
	return ErrChallengeState
}

// Authenticate loads a challenge after constant-time secret verification.
func (s *DeviceLoginChallengeStore) Authenticate(ctx context.Context, challengeID, secret string) (*DeviceLoginChallenge, error) {
	c, err := loadChallenge(ctx, s.pool, challengeID, false)
	if errors.Is(err, ErrChallengeInvalid) {
		// Perform the same digest comparison work for unknown IDs.
		_ = auth.OpaqueSecretMatches(secret, make([]byte, 32))
		return nil, ErrChallengeInvalid
	}
	if err != nil {
		return nil, err
	}
	if err := authenticateChallenge(c, secret); err != nil {
		return nil, err
	}
	return c, nil
}

// ListPending returns live requests belonging to one campus identity.
func (s *DeviceLoginChallengeStore) ListPending(ctx context.Context, studentHash string) ([]DeviceLoginChallenge, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT challenge_id, student_hash, target_device_fingerprint,
		        target_device_name, secret_hash, status, expires_at,
		        created_at, updated_at, approved_at, consumed_at
		   FROM device_login_challenges
		  WHERE student_hash=$1 AND status='pending' AND expires_at>NOW()
		  ORDER BY created_at DESC`, studentHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceLoginChallenge{}
	for rows.Next() {
		var c DeviceLoginChallenge
		if err := rows.Scan(
			&c.ID, &c.StudentHash, &c.TargetDeviceFingerprint, &c.TargetDeviceName,
			&c.SecretHash, &c.Status, &c.ExpiresAt, &c.CreatedAt, &c.UpdatedAt,
			&c.ApprovedAt, &c.ConsumedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *DeviceLoginChallengeStore) transitionForStudent(ctx context.Context, challengeID, studentHash, targetStatus string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := loadChallenge(ctx, tx, challengeID, true)
	if err != nil {
		return err
	}
	if !challengeBelongsToStudent(c, studentHash) {
		return ErrChallengeInvalid
	}
	if err := stateError(c, ChallengePending, time.Now()); err != nil {
		return err
	}
	approvedAt := "NULL"
	if targetStatus == ChallengeApproved {
		approvedAt = "NOW()"
	}
	if _, err := tx.Exec(ctx,
		`UPDATE device_login_challenges
		    SET status=$2, updated_at=NOW(), approved_at=`+approvedAt+`
		  WHERE challenge_id=$1 AND status='pending'`,
		challengeID, targetStatus,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Approve records approval by an authenticated trusted device. The handler is
// responsible for checking the approver's current device fingerprint.
func (s *DeviceLoginChallengeStore) Approve(ctx context.Context, challengeID, studentHash string) error {
	return s.transitionForStudent(ctx, challengeID, studentHash, ChallengeApproved)
}

func (s *DeviceLoginChallengeStore) Reject(ctx context.Context, challengeID, studentHash string) error {
	return s.transitionForStudent(ctx, challengeID, studentHash, ChallengeRejected)
}

// ApproveWithSecret records successful campus-email verification while binding
// it to both the challenge secret and the expected student hash.
func (s *DeviceLoginChallengeStore) ApproveWithSecret(ctx context.Context, challengeID, secret, studentHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := loadChallenge(ctx, tx, challengeID, true)
	if err != nil {
		return err
	}
	if err := authenticateChallenge(c, secret); err != nil || !challengeBelongsToStudent(c, studentHash) {
		return ErrChallengeInvalid
	}
	if err := stateError(c, ChallengePending, time.Now()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE device_login_challenges
		    SET status='approved', approved_at=NOW(), updated_at=NOW()
		  WHERE challenge_id=$1 AND status='pending'`, challengeID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ConsumeAndTrust atomically consumes an approved challenge exactly once and
// trusts only its bound target device.
func (s *DeviceLoginChallengeStore) ConsumeAndTrust(ctx context.Context, challengeID, secret string) (*DeviceLoginChallenge, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	c, err := loadChallenge(ctx, tx, challengeID, true)
	if err != nil {
		return nil, err
	}
	if err := authenticateChallenge(c, secret); err != nil {
		return nil, err
	}
	if err := stateError(c, ChallengeApproved, time.Now()); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO trusted_devices(student_hash, device_fingerprint, device_name)
		 VALUES ($1,$2,$3)
		 ON CONFLICT (student_hash, device_fingerprint)
		 DO UPDATE SET last_seen_at=NOW(),
		   device_name=CASE WHEN EXCLUDED.device_name<>'' THEN EXCLUDED.device_name
		                    ELSE trusted_devices.device_name END`,
		c.StudentHash, c.TargetDeviceFingerprint, c.TargetDeviceName,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE device_login_challenges
		    SET status='consumed', consumed_at=NOW(), updated_at=NOW()
		  WHERE challenge_id=$1 AND status='approved'`, challengeID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	c.Status = ChallengeConsumed
	now := time.Now()
	c.ConsumedAt = &now
	c.UpdatedAt = now
	return c, nil
}
