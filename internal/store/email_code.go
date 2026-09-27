package store

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"zonenan-backend/internal/db"
)

// maskLogEmail 遮蔽邮箱本地部分(仅日志用):a***@csu.edu.cn,不打明文学号。
func maskLogEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 1 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}

// ErrCodeInvalid 表示验证码错误或已过期。
var ErrCodeInvalid = errors.New("验证码错误或已过期")

// ErrCodeLocked 表示尝试次数过多,验证码已作废。
var ErrCodeLocked = errors.New("验证码尝试次数过多,请重新获取")

// maxCodeAttempts 是单个验证码允许的最大校验尝试次数(H2:防爆破)。
const maxCodeAttempts = 5

// EmailCodeStore 管理邮箱验证码。
type EmailCodeStore struct{ pool *db.Pool }

func NewEmailCodeStore(p *db.Pool) *EmailCodeStore { return &EmailCodeStore{pool: p} }

// Save 写入/覆盖验证码,ttl 后过期。重置尝试计数。
func (s *EmailCodeStore) Save(ctx context.Context, email, code, scope string, ttl time.Duration) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO email_codes(email, code, scope, expires_at, created_at, attempts)
		 VALUES ($1,$2,$3,NOW()+$4::interval,NOW(),0)
		 ON CONFLICT(email) DO UPDATE SET code=EXCLUDED.code, scope=EXCLUDED.scope,
		   expires_at=EXCLUDED.expires_at, created_at=NOW(), attempts=0`,
		email, code, scope, ttl.String())
	return err
}

// Verify 校验并原子消费验证码(成功即删除)。scope 必须匹配(M3:防跨用途滥用)。
// H2:每次失败计数 +1,达 maxCodeAttempts 即作废该验证码,防爆破。
func (s *EmailCodeStore) Verify(ctx context.Context, email, code, scope string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		log.Printf("email code verify begin failed for %s: %v", maskLogEmail(email), err)
		return errors.New("验证码校验失败,请稍后重试")
	}
	defer tx.Rollback(ctx)

	var stored, storedScope string
	var expires time.Time
	var attempts int
	err = tx.QueryRow(ctx,
		`SELECT code, scope, expires_at, attempts FROM email_codes WHERE email=$1 FOR UPDATE`, email,
	).Scan(&stored, &storedScope, &expires, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCodeInvalid
	}
	if err != nil {
		// M4:底层 DB 错误不外泄给客户端。H6:邮箱脱敏。
		log.Printf("email code verify query failed for %s: %v", maskLogEmail(email), err)
		return errors.New("验证码校验失败,请稍后重试")
	}

	result := error(nil)
	switch {
	case time.Now().After(expires):
		_, err = tx.Exec(ctx, `DELETE FROM email_codes WHERE email=$1`, email)
		result = ErrCodeInvalid
	case attempts >= maxCodeAttempts:
		_, err = tx.Exec(ctx, `DELETE FROM email_codes WHERE email=$1`, email)
		result = ErrCodeLocked
	case stored != code || storedScope != scope:
		if attempts+1 >= maxCodeAttempts {
			_, err = tx.Exec(ctx, `DELETE FROM email_codes WHERE email=$1`, email)
		} else {
			_, err = tx.Exec(ctx, `UPDATE email_codes SET attempts=attempts+1 WHERE email=$1`, email)
		}
		result = ErrCodeInvalid
	default:
		_, err = tx.Exec(ctx, `DELETE FROM email_codes WHERE email=$1`, email)
	}
	if err != nil {
		log.Printf("email code verify update failed for %s: %v", maskLogEmail(email), err)
		return errors.New("验证码校验失败,请稍后重试")
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("email code verify commit failed for %s: %v", maskLogEmail(email), err)
		return errors.New("验证码校验失败,请稍后重试")
	}
	return result
}

// SecondsSinceLast 返回上次发码至今的秒数(用于冷却);无记录返回一个大数。
func (s *EmailCodeStore) SecondsSinceLast(ctx context.Context, email string) int {
	var created time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT created_at FROM email_codes WHERE email=$1`, email).Scan(&created)
	if err != nil {
		return 1 << 30
	}
	return int(time.Since(created).Seconds())
}
