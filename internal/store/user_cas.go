package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// BindOrLoginCAS 处理信息门户登录后的账号归属(反"一人两号"):
//   - cas 身份已存在 → 登入该账号(忽略 currentUserID)。
//   - cas 身份不存在 且 currentUserID>0 → 把 cas 绑到当前账号。
//   - cas 身份不存在 且 无当前登录 → 自动新建账号并绑定。
//
// studentHash = sha256(学号|pepper),由调用方算好。返回账号 + 是否新建。
func (s *UserStore) BindOrLoginCAS(ctx context.Context, studentHash, displayName string, currentUserID int64) (*User, bool, error) {
	var existingUserID int64
	err := s.pool.QueryRow(ctx,
		`SELECT user_id FROM zonenan_identities WHERE provider='cas' AND provider_uid=$1`,
		studentHash,
	).Scan(&existingUserID)

	switch {
	case err == nil:
		// 已存在 cas 身份 → 登入该账号。
		u, gerr := s.GetByID(ctx, existingUserID)
		return u, false, gerr
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, false, err
	}

	// cas 身份不存在。
	if currentUserID > 0 {
		// 绑到当前账号。
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO zonenan_identities(user_id, provider, provider_uid, verified, display_name)
			 VALUES ($1,'cas',$2,TRUE,$3)`,
			currentUserID, studentHash, displayName,
		); err != nil {
			return nil, false, err
		}
		u, gerr := s.GetByID(ctx, currentUserID)
		return u, false, gerr
	}

	// 自动新建账号并绑定 cas。
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	var uid int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO zonenan_users(nickname) VALUES ($1) RETURNING id`,
		defaultNickname(displayName),
	).Scan(&uid); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO zonenan_identities(user_id, provider, provider_uid, verified, display_name)
		 VALUES ($1,'cas',$2,TRUE,$3)`,
		uid, studentHash, displayName,
	); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	u, gerr := s.GetByID(ctx, uid)
	return u, true, gerr
}

func defaultNickname(displayName string) string {
	if displayName != "" {
		return displayName
	}
	return "中南同学"
}
