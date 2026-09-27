package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"zonenan-backend/internal/db"
)

// GradeStore 负责给分数据的读写(档案/成绩/关联/授权/评教)。
type GradeStore struct{ pool *db.Pool }

func NewGradeStore(p *db.Pool) *GradeStore { return &GradeStore{pool: p} }

// AuthStatus 是给分授权(同意状态机)快照。
type AuthStatus struct {
	ConsentStatus    string     `json:"consent_status"` // active | revoked | none
	FirstConsentedAt *time.Time `json:"first_consented_at,omitempty"`
	LastSyncAt       *time.Time `json:"last_sync_at,omitempty"`
	LastFullSyncAt   *time.Time `json:"last_full_sync_at,omitempty"`
	// CanQuery = consent_status='active' 且 last_sync_at 非空。
	CanQuery bool `json:"can_query"`
}

// GetAuthStatus 读取用户授权状态;无记录返回 none。
func (s *GradeStore) GetAuthStatus(ctx context.Context, userID int64) (*AuthStatus, error) {
	st := &AuthStatus{ConsentStatus: "none"}
	err := s.pool.QueryRow(ctx,
		`SELECT consent_status, first_consented_at, last_sync_at, last_full_sync_at
		   FROM grade_authorizations WHERE user_id=$1`, userID,
	).Scan(&st.ConsentStatus, &st.FirstConsentedAt, &st.LastSyncAt, &st.LastFullSyncAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &AuthStatus{ConsentStatus: "none"}, nil // 从未授权
	}
	if err != nil {
		return nil, err // 真实 DB 错误,上抛 → 500,不静默吞
	}
	st.CanQuery = st.ConsentStatus == "active" && st.LastSyncAt != nil
	return st, nil
}

// Consent 记录/恢复同意(状态 → active)。首次创建,重复则复位为 active。
func (s *GradeStore) Consent(ctx context.Context, userID int64, fingerprint string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO grade_authorizations(user_id, consent_status, first_consented_at, consent_updated_at, device_fingerprint)
		 VALUES ($1, 'active', NOW(), NOW(), $2)
		 ON CONFLICT (user_id) DO UPDATE SET
		   consent_status='active', consent_updated_at=NOW(),
		   device_fingerprint=EXCLUDED.device_fingerprint`,
		userID, fingerprint)
	return err
}

// Revoke 撤销同意(状态 → revoked)。已上传数据不删。
func (s *GradeStore) Revoke(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE grade_authorizations SET consent_status='revoked', consent_updated_at=NOW()
		  WHERE user_id=$1`, userID)
	return err
}

// ShouldSync 判断是否该后台重抓:active 且(从没抓过 或 距上次抓 > 节流窗口)。
func (s *GradeStore) ShouldSync(st *AuthStatus, throttleHours int) bool {
	if st.ConsentStatus != "active" {
		return false
	}
	if st.LastSyncAt == nil {
		return true
	}
	return time.Since(*st.LastSyncAt) > time.Duration(throttleHours)*time.Hour
}

// ShouldFullSync 判断是否该全量抓:从没全量过 或 距上次全量 > 7 天(往期改分兜底)。
func (s *GradeStore) ShouldFullSync(st *AuthStatus) bool {
	if st.ConsentStatus != "active" {
		return false
	}
	if st.LastFullSyncAt == nil {
		return true
	}
	return time.Since(*st.LastFullSyncAt) > 7*24*time.Hour
}
