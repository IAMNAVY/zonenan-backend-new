package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

// isUniqueViolation 判断是否 Postgres 唯一约束冲突(SQLSTATE 23505)。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// RegisterEmail 新建账号 + email 身份(密码 bcrypt,verified=true 因为已过验证码校验)。
func (s *UserStore) RegisterEmail(ctx context.Context, email, password, nickname string) (*User, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM zonenan_identities WHERE provider='email' AND provider_uid=$1)`,
		email).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrEmailTaken
	}
	// bcrypt 上限 72 字节;超长直接拒(而非静默截断)。调用方也应先校验。
	if len(password) > 72 {
		return nil, ErrPasswordTooLong
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var uid int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO zonenan_users(nickname) VALUES ($1) RETURNING id`,
		defaultNickname(nickname),
	).Scan(&uid); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO zonenan_identities(user_id, provider, provider_uid, secret, verified, display_name)
		 VALUES ($1,'email',$2,$3,TRUE,$4)`,
		uid, email, string(hash), nickname,
	); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetByID(ctx, uid)
}

// BindEmail 给已有账号绑定邮箱+密码(该账号当前无 email identity)。
func (s *UserStore) BindEmail(ctx context.Context, userID int64, email, password string) error {
	if len(password) > 72 {
		return ErrPasswordTooLong
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO zonenan_identities(user_id, provider, provider_uid, secret, verified, display_name)
		 VALUES ($1,'email',$2,$3,TRUE,$4)`,
		userID, email, string(hash), email,
	)
	if err != nil && isUniqueViolation(err) {
		return ErrEmailTaken
	}
	return err
}

// EmailRegistered 报告该邮箱是否已有 email 身份(S5 找回密码防枚举:仅对已注册邮箱发码)。
func (s *UserStore) EmailRegistered(ctx context.Context, email string) bool {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM zonenan_identities WHERE provider='email' AND provider_uid=$1)`,
		email).Scan(&exists); err != nil {
		return false
	}
	return exists
}

// ResetEmailPassword 按邮箱重设密码(S5 找回密码用,调用方须先过邮箱验证码校验)。
// 邮箱无对应 email 身份时返回 ErrNotFound。
func (s *UserStore) ResetEmailPassword(ctx context.Context, email, newPassword string) error {
	if len(newPassword) > 72 {
		return ErrPasswordTooLong
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	ct, err := s.pool.Exec(ctx,
		`UPDATE zonenan_identities SET secret=$1
		  WHERE provider='email' AND provider_uid=$2`,
		string(hash), email)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ChangeEmailPassword 校验旧密码后改密(S5 登录态改密用)。
// 账号无 email 身份返回 ErrNotFound;旧密码不符返回 ErrWrongPassword。
func (s *UserStore) ChangeEmailPassword(ctx context.Context, userID int64, oldPassword, newPassword string) error {
	if len(newPassword) > 72 {
		return ErrPasswordTooLong
	}
	var secret string
	err := s.pool.QueryRow(ctx,
		`SELECT secret FROM zonenan_identities WHERE provider='email' AND user_id=$1`,
		userID).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(secret), []byte(oldPassword)) != nil {
		return ErrWrongPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE zonenan_identities SET secret=$1 WHERE provider='email' AND user_id=$2`,
		string(hash), userID)
	return err
}

// LoginEmail 校验邮箱+密码,返回账号。失败返回 ErrNotFound(不区分账号/密码错,防枚举)。
func (s *UserStore) LoginEmail(ctx context.Context, email, password string) (*User, error) {
	var userID int64
	var secret string
	err := s.pool.QueryRow(ctx,
		`SELECT user_id, secret FROM zonenan_identities WHERE provider='email' AND provider_uid=$1`,
		email,
	).Scan(&userID, &secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(secret), []byte(password)) != nil {
		return nil, ErrNotFound
	}
	return s.GetByID(ctx, userID)
}
