package adminauth

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"os"
	"testing"
	"time"
	"zonenan-backend/internal/db"
)

// Only runs against an explicitly supplied local disposable test database.
func TestChangePasswordIntegration(t *testing.T) {
	url := os.Getenv("ZONENAN_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set ZONENAN_TEST_DATABASE_URL to a local test database")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	email := fmt.Sprintf("password-test-%d@example.invalid", time.Now().UnixNano())
	current, next := "test-current-password", "test-replacement-password"
	hash, _ := bcrypt.GenerateFromPassword([]byte(current), bcrypt.DefaultCost)
	var id int64
	if err = pool.QueryRow(ctx, `INSERT INTO admin_users(email,display_name,password_hash) VALUES($1,'password test',$2) RETURNING id`, email, string(hash)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_type='admin' AND actor_id=$1`, id)
		pool.Exec(ctx, `DELETE FROM admin_login_attempts WHERE email=$1`, email)
		pool.Exec(ctx, `DELETE FROM admin_sessions WHERE admin_user_id=$1`, id)
		pool.Exec(ctx, `DELETE FROM admin_users WHERE id=$1`, id)
	}()
	s := NewStore(pool)
	first, _, _, err := s.CreateSession(ctx, id, "127.0.0.1", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, _, _, err := s.CreateSession(ctx, id, "127.0.0.1", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ChangePassword(ctx, id, "wrong-password", next); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, err = s.Resolve(ctx, first); err != nil {
		t.Fatalf("failed attempt revoked session: %v", err)
	}
	if err = s.ChangePassword(ctx, id, current, next); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first, second} {
		if _, _, err = s.Resolve(ctx, token); err == nil {
			t.Fatal("old session survived")
		}
	}
	if _, err = s.Authenticate(ctx, email, current, "127.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password accepted: %v", err)
	}
	if _, err = s.Authenticate(ctx, email, next, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_type='admin' AND actor_id=$1 AND action='admin.password.change'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit count %d: %v", count, err)
	}
}
