package adminauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"zonenan-backend/internal/db"
)

var ErrInvalidCredentials = errors.New("invalid admin credentials")
var ErrInvalidPassword = errors.New("password must be 12 to 72 bytes and different from current password")

// ChangePassword serializes concurrent changes and revokes every existing session.
func (s *Store) ChangePassword(ctx context.Context, adminID int64, current, next string) error {
	if len(next) < 12 || len(next) > 72 || next == current {
		return ErrInvalidPassword
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var hash string
	if err = tx.QueryRow(ctx, `SELECT password_hash FROM admin_users WHERE id=$1 AND active=TRUE FOR UPDATE`, adminID).Scan(&hash); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return ErrInvalidCredentials
	}
	encoded, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE admin_users SET password_hash=$1,updated_at=NOW() WHERE id=$2`, string(encoded), adminID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE admin_sessions SET revoked_at=NOW() WHERE admin_user_id=$1 AND revoked_at IS NULL`, adminID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,resource_type,resource_id,metadata) VALUES('admin',$1,'admin.password.change','admin_user',$2,'{}'::jsonb)`, adminID, fmt.Sprint(adminID)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type Store struct{ pool *db.Pool }

func NewStore(pool *db.Pool) *Store { return &Store{pool: pool} }

type Principal struct {
	ID          int64    `json:"id"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	SessionID   string   `json:"-"`
}

func EnsureBootstrap(ctx context.Context, pool *db.Pool, email, password, name string) error {
	if email == "" && password == "" {
		return nil
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return fmt.Errorf("invalid ADMIN_BOOTSTRAP_EMAIL")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if name == "" {
		name = "ZoneNaN Admin"
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO admin_users(email,display_name,password_hash) VALUES($1,$2,$3) RETURNING id`, email, name, string(hash)).Scan(&id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO admin_user_roles(admin_user_id,role_key) VALUES($1,'super_admin')`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,resource_type,resource_id,metadata) VALUES('system',$1,'admin.bootstrap','admin_user',$2,'{"source":"environment"}'::jsonb)`, id, fmt.Sprint(id)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Authenticate(ctx context.Context, email, password, ip string) (*Principal, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var principal Principal
	var passwordHash string
	var active bool
	err := s.pool.QueryRow(ctx, `SELECT id,email,display_name,password_hash,active FROM admin_users WHERE email=$1`, email).
		Scan(&principal.ID, &principal.Email, &principal.DisplayName, &passwordHash, &active)
	succeeded := err == nil && active && bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) == nil
	_, _ = s.pool.Exec(ctx, `INSERT INTO admin_login_attempts(email,ip,succeeded) VALUES($1,$2,$3)`, email, ip, succeeded)
	if !succeeded {
		return nil, ErrInvalidCredentials
	}
	if _, err := s.pool.Exec(ctx, `UPDATE admin_users SET last_login_at=NOW(),updated_at=NOW() WHERE id=$1`, principal.ID); err != nil {
		return nil, err
	}
	if err := s.loadAccess(ctx, &principal); err != nil {
		return nil, err
	}
	return &principal, nil
}

func randomToken(bytes int) (string, []byte, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

func (s *Store) CreateSession(ctx context.Context, adminID int64, ip, userAgent string, ttl time.Duration) (sessionToken, csrfToken string, expires time.Time, err error) {
	sessionToken, tokenHash, err := randomToken(32)
	if err != nil {
		return "", "", time.Time{}, err
	}
	csrfToken, csrfHash, err := randomToken(32)
	if err != nil {
		return "", "", time.Time{}, err
	}
	sessionID, _, err := randomToken(18)
	if err != nil {
		return "", "", time.Time{}, err
	}
	expires = time.Now().Add(ttl)
	_, err = s.pool.Exec(ctx, `INSERT INTO admin_sessions(id,admin_user_id,token_hash,csrf_hash,ip,user_agent,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, sessionID, adminID, tokenHash, csrfHash, ip, userAgent, expires)
	return sessionToken, csrfToken, expires, err
}

func (s *Store) Resolve(ctx context.Context, sessionToken string) (*Principal, []byte, error) {
	tokenHash := sha256.Sum256([]byte(sessionToken))
	var principal Principal
	var csrfHash []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE admin_sessions s SET last_seen_at=NOW()
		FROM admin_users u
		WHERE s.token_hash=$1 AND s.admin_user_id=u.id AND s.revoked_at IS NULL
		  AND s.expires_at>NOW() AND u.active=TRUE
		RETURNING u.id,u.email,u.display_name,s.id,s.csrf_hash`, tokenHash[:]).
		Scan(&principal.ID, &principal.Email, &principal.DisplayName, &principal.SessionID, &csrfHash)
	if err != nil {
		return nil, nil, err
	}
	if err := s.loadAccess(ctx, &principal); err != nil {
		return nil, nil, err
	}
	return &principal, csrfHash, nil
}

func (s *Store) loadAccess(ctx context.Context, p *Principal) error {
	roleRows, err := s.pool.Query(ctx, `SELECT role_key FROM admin_user_roles WHERE admin_user_id=$1 ORDER BY role_key`, p.ID)
	if err != nil {
		return err
	}
	defer roleRows.Close()
	for roleRows.Next() {
		var role string
		if err := roleRows.Scan(&role); err != nil {
			return err
		}
		p.Roles = append(p.Roles, role)
	}
	if err := roleRows.Err(); err != nil {
		return err
	}
	permissionRows, err := s.pool.Query(ctx, `
		SELECT DISTINCT rp.permission_key
		FROM admin_user_roles ur JOIN admin_role_permissions rp ON rp.role_key=ur.role_key
		WHERE ur.admin_user_id=$1 ORDER BY rp.permission_key`, p.ID)
	if err != nil {
		return err
	}
	defer permissionRows.Close()
	for permissionRows.Next() {
		var permission string
		if err := permissionRows.Scan(&permission); err != nil {
			return err
		}
		p.Permissions = append(p.Permissions, permission)
	}
	return permissionRows.Err()
}

func (s *Store) Revoke(ctx context.Context, sessionToken string) error {
	tokenHash := sha256.Sum256([]byte(sessionToken))
	_, err := s.pool.Exec(ctx, `UPDATE admin_sessions SET revoked_at=COALESCE(revoked_at,NOW()) WHERE token_hash=$1`, tokenHash[:])
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]map[string]any, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id,actor_type,actor_id,action,resource_type,resource_id,request_id,ip,user_agent,status_code,metadata,created_at FROM audit_logs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var actorType, action, resourceType, resourceID, requestID, ip, userAgent string
		var actorID *int64
		var status int
		var metadata []byte
		var created time.Time
		if err := rows.Scan(&id, &actorType, &actorID, &action, &resourceType, &resourceID, &requestID, &ip, &userAgent, &status, &metadata, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "actor_type": actorType, "actor_id": actorID, "action": action, "resource_type": resourceType, "resource_id": resourceID, "request_id": requestID, "ip": ip, "user_agent": userAgent, "status_code": status, "metadata": string(metadata), "created_at": created})
	}
	return out, rows.Err()
}

func (s *Store) RecordAudit(ctx context.Context, principal *Principal, action, resourceType, resourceID, requestID, ip, userAgent string, status int) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,session_id,action,resource_type,resource_id,request_id,ip,user_agent,status_code) VALUES('admin',$1,$2,$3,$4,$5,$6,$7,$8,$9)`, principal.ID, principal.SessionID, action, resourceType, resourceID, requestID, ip, userAgent, status)
	return err
}

func (s *Store) ListAdmins(ctx context.Context) ([]Principal, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,email,display_name FROM admin_users WHERE active=TRUE ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Principal
	for rows.Next() {
		var p Principal
		if err := rows.Scan(&p.ID, &p.Email, &p.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadAccess(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) ListRoles(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.role_key,r.title,r.description,COALESCE(array_agg(rp.permission_key ORDER BY rp.permission_key) FILTER (WHERE rp.permission_key IS NOT NULL),'{}') FROM admin_roles r LEFT JOIN admin_role_permissions rp ON rp.role_key=r.role_key GROUP BY r.role_key,r.title,r.description ORDER BY r.role_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var key, title, description string
		var permissions []string
		if err := rows.Scan(&key, &title, &description, &permissions); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"key": key, "title": title, "description": description, "permissions": permissions})
	}
	return out, rows.Err()
}

func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
