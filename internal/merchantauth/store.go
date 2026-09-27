package merchantauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"zonenan-backend/internal/db"
)

var ErrInvalidCredentials = errors.New("invalid merchant credentials")

type Store struct{ pool *db.Pool }

func NewStore(pool *db.Pool) *Store { return &Store{pool: pool} }

type Principal struct {
	ID           int64  `json:"id"`
	MerchantID   int64  `json:"merchant_id"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	DisplayName  string `json:"display_name"`
	Role         string `json:"role"`
	MerchantName string `json:"merchant_name"`
	MerchantType string `json:"merchant_type"`
	SessionID    string `json:"-"`
}

func token() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, e := rand.Read(raw); e != nil {
		return "", nil, e
	}
	v := base64.RawURLEncoding.EncodeToString(raw)
	h := sha256.Sum256([]byte(v))
	return v, h[:], nil
}
func (s *Store) SelfRegistrationEnabled(ctx context.Context) bool {
	var v string
	return s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key='merchant_self_registration_enabled'`).Scan(&v) == nil && v == "true"
}
func (s *Store) Register(ctx context.Context, email, phone, password, name, merchantName, merchantType string) (int64, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	phone = strings.TrimSpace(phone)
	name = strings.TrimSpace(name)
	merchantName = strings.TrimSpace(merchantName)
	if !s.SelfRegistrationEnabled(ctx) {
		return 0, errors.New("商户自助注册当前未开放")
	}
	if (email == "" && phone == "") || len(password) < 12 || name == "" || merchantName == "" {
		return 0, errors.New("注册信息不完整，密码至少 12 位")
	}
	h, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		return 0, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	var merchantID, userID int64
	if e = tx.QueryRow(ctx, `INSERT INTO merchants(name,merchant_type,status,contact_name,contact_phone) VALUES($1,$2,'pending',$3,$4) RETURNING id`, merchantName, merchantType, name, phone).Scan(&merchantID); e != nil {
		return 0, e
	}
	if e = tx.QueryRow(ctx, `INSERT INTO merchant_users(merchant_id,email,phone,password_hash,display_name,status,registration_source) VALUES($1,NULLIF($2,''),NULLIF($3,''),$4,$5,'pending','self') RETURNING id`, merchantID, email, phone, string(h), name).Scan(&userID); e != nil {
		return 0, e
	}
	return userID, tx.Commit(ctx)
}
func (s *Store) AdminCreate(ctx context.Context, email, phone, password, name, merchantName, merchantType string) (int64, error) {
	if len(password) < 12 {
		return 0, errors.New("密码至少 12 位")
	}
	h, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		return 0, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	var mid, uid int64
	if e = tx.QueryRow(ctx, `INSERT INTO merchants(name,merchant_type,status,contact_name,contact_phone,reviewed_at) VALUES($1,$2,'approved',$3,$4,NOW()) RETURNING id`, merchantName, merchantType, name, phone).Scan(&mid); e != nil {
		return 0, e
	}
	if e = tx.QueryRow(ctx, `INSERT INTO merchant_users(merchant_id,email,phone,password_hash,display_name,status,registration_source) VALUES($1,NULLIF($2,''),NULLIF($3,''),$4,$5,'active','admin') RETURNING id`, mid, strings.ToLower(strings.TrimSpace(email)), strings.TrimSpace(phone), string(h), name).Scan(&uid); e != nil {
		return 0, e
	}
	if merchantType != "commercial_apartment" {
		_, e = tx.Exec(ctx, `INSERT INTO merchant_stores(merchant_id,name,status) VALUES($1,$2,'draft')`, mid, merchantName)
		if e != nil {
			return 0, e
		}
	}
	return uid, tx.Commit(ctx)
}
func (s *Store) Authenticate(ctx context.Context, identity, password string) (*Principal, error) {
	identity = strings.ToLower(strings.TrimSpace(identity))
	var p Principal
	var hash, userStatus, merchantStatus string
	e := s.pool.QueryRow(ctx, `SELECT u.id,u.merchant_id,COALESCE(u.email,''),COALESCE(u.phone,''),u.display_name,u.role,u.password_hash,u.status,m.status,m.name,m.merchant_type FROM merchant_users u JOIN merchants m ON m.id=u.merchant_id WHERE lower(COALESCE(u.email,''))=$1 OR u.phone=$1`, identity).Scan(&p.ID, &p.MerchantID, &p.Email, &p.Phone, &p.DisplayName, &p.Role, &hash, &userStatus, &merchantStatus, &p.MerchantName, &p.MerchantType)
	if e != nil || userStatus != "active" || merchantStatus != "approved" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	_, _ = s.pool.Exec(ctx, `UPDATE merchant_users SET last_login_at=NOW() WHERE id=$1`, p.ID)
	return &p, nil
}
func (s *Store) CreateSession(ctx context.Context, userID int64, ip, ua string, ttl time.Duration) (string, string, time.Time, error) {
	st, sh, e := token()
	if e != nil {
		return "", "", time.Time{}, e
	}
	ct, ch, e := token()
	if e != nil {
		return "", "", time.Time{}, e
	}
	id, _, e := token()
	if e != nil {
		return "", "", time.Time{}, e
	}
	ex := time.Now().Add(ttl)
	_, e = s.pool.Exec(ctx, `INSERT INTO merchant_sessions(id,merchant_user_id,token_hash,csrf_hash,ip,user_agent,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, userID, sh, ch, ip, ua, ex)
	return st, ct, ex, e
}
func (s *Store) Resolve(ctx context.Context, value string) (*Principal, []byte, error) {
	h := sha256.Sum256([]byte(value))
	var p Principal
	var csrf []byte
	e := s.pool.QueryRow(ctx, `SELECT u.id,u.merchant_id,COALESCE(u.email,''),COALESCE(u.phone,''),u.display_name,u.role,m.name,m.merchant_type,s.id,s.csrf_hash FROM merchant_sessions s JOIN merchant_users u ON u.id=s.merchant_user_id JOIN merchants m ON m.id=u.merchant_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>NOW() AND u.status='active' AND m.status='approved'`, h[:]).Scan(&p.ID, &p.MerchantID, &p.Email, &p.Phone, &p.DisplayName, &p.Role, &p.MerchantName, &p.MerchantType, &p.SessionID, &csrf)
	return &p, csrf, e
}
func (s *Store) Revoke(ctx context.Context, value string) error {
	h := sha256.Sum256([]byte(value))
	_, e := s.pool.Exec(ctx, `UPDATE merchant_sessions SET revoked_at=NOW() WHERE token_hash=$1`, h[:])
	return e
}
func (s *Store) Audit(ctx context.Context, p *Principal, action, resource, id, ip string) {
	_, _ = s.pool.Exec(ctx, `INSERT INTO merchant_audit_logs(actor_type,actor_id,merchant_id,action,resource_type,resource_id,ip) VALUES('merchant_user',$1,$2,$3,$4,$5,NULLIF($6,'')::inet)`, p.ID, p.MerchantID, action, resource, id, ip)
}
