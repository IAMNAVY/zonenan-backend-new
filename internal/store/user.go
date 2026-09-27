package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"zonenan-backend/internal/db"
)

var ErrEmailTaken = errors.New("该邮箱已注册")
var ErrNotFound = errors.New("不存在")
var ErrPasswordTooLong = errors.New("密码过长")
var ErrWrongPassword = errors.New("原密码错误")

type User struct {
	ID        int64  `json:"id"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatar_url"`
	Email     string `json:"email,omitempty"`
	IsBanned  bool   `json:"is_banned"`
	Role      string `json:"role"`
	IsWhite   bool   `json:"is_whitelisted"`
	IsBeta    bool   `json:"is_beta"`
	IsPremium bool   `json:"is_premium"`
	HasCampus bool   `json:"has_campus"`
}

type UserStore struct{ pool *db.Pool }

func NewUserStore(p *db.Pool) *UserStore { return &UserStore{pool: p} }

func (s *UserStore) GetByID(ctx context.Context, id int64) (*User, error) {
	var u User
	var email *string
	err := s.pool.QueryRow(ctx,
		`SELECT u.id, u.nickname, u.avatar_url, u.is_banned, u.role, u.is_whitelisted,
		        EXISTS(SELECT 1 FROM beta_memberships b WHERE b.user_id=u.id),
		        EXISTS(SELECT 1 FROM membership_grants g
		          WHERE g.user_id=u.id AND g.membership_type_key='premium'
		            AND g.revoked_at IS NULL AND g.starts_at <= NOW() AND g.expires_at > NOW()),
		        EXISTS(SELECT 1 FROM zonenan_identities i WHERE i.user_id=u.id AND i.provider='cas'),
		        (SELECT provider_uid FROM zonenan_identities WHERE user_id=u.id AND provider='email' LIMIT 1)
		   FROM zonenan_users u WHERE u.id=$1`, id,
	).Scan(&u.ID, &u.Nickname, &u.AvatarURL, &u.IsBanned, &u.Role, &u.IsWhite, &u.IsBeta, &u.IsPremium, &u.HasCampus, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if email != nil {
		u.Email = *email
	}
	return &u, err
}

func (s *UserStore) TouchLastOnline(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE zonenan_users SET last_online_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *UserStore) SetBanned(ctx context.Context, id int64, banned bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE zonenan_users SET is_banned=$2, updated_at=NOW() WHERE id=$1`, id, banned)
	return err
}

func (s *UserStore) SetRole(ctx context.Context, id int64, role string) error {
	_, err := s.pool.Exec(ctx, `UPDATE zonenan_users SET role=$2, updated_at=NOW() WHERE id=$1`, id, role)
	return err
}

func (s *UserStore) SetWhitelisted(ctx context.Context, id int64, white bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE zonenan_users SET is_whitelisted=$2, updated_at=NOW() WHERE id=$1`, id, white)
	return err
}

// IsPrivileged 判断用户是否为白名单或 admin(免风控、免免费查询额度)。
func (s *UserStore) IsPrivileged(ctx context.Context, id int64) bool {
	var role string
	var white bool
	err := s.pool.QueryRow(ctx, `SELECT role, is_whitelisted FROM zonenan_users WHERE id=$1`, id).Scan(&role, &white)
	if err != nil {
		return false
	}
	return white || role == "admin"
}

type UserStatus int

const (
	UserOK UserStatus = iota
	UserNotFound
	UserBanned
)

func (s *UserStore) CheckStatus(ctx context.Context, id int64) UserStatus {
	var banned bool
	err := s.pool.QueryRow(ctx, `SELECT is_banned FROM zonenan_users WHERE id=$1`, id).Scan(&banned)
	if err != nil {
		return UserNotFound
	}
	if banned {
		return UserBanned
	}
	return UserOK
}

func (s *UserStore) UpdateProfile(ctx context.Context, id int64, nickname, avatarURL string) error {
	_, err := s.pool.Exec(ctx, `UPDATE zonenan_users SET nickname=$2, avatar_url=$3, updated_at=NOW() WHERE id=$1`, id, nickname, avatarURL)
	return err
}

func (s *UserStore) GetByStudentHash(ctx context.Context, studentHash string) (*User, error) {
	var userID int64
	err := s.pool.QueryRow(ctx, `SELECT user_id FROM zonenan_identities WHERE provider='cas' AND provider_uid=$1`, studentHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetByID(ctx, userID)
}

func (s *UserStore) StudentHashOf(ctx context.Context, userID int64) (string, error) {
	var h string
	err := s.pool.QueryRow(ctx, `SELECT provider_uid FROM zonenan_identities WHERE user_id=$1 AND provider='cas' LIMIT 1`, userID).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return h, err
}
