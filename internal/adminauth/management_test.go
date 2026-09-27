package adminauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"zonenan-backend/internal/db"
)

func TestValidateNewAdmin(t *testing.T) {
	valid := NewAdmin{Email: " USER@Example.invalid ", DisplayName: " Operator ", Password: "long-test-password", Roles: []string{"analyst"}}
	if err := validateAdmin(&valid); err != nil || valid.Email != "user@example.invalid" || valid.DisplayName != "Operator" {
		t.Fatalf("normalization: %#v %v", valid, err)
	}
	for _, modify := range []func(*NewAdmin){func(v *NewAdmin) { v.Email = "Name <user@example.invalid>" }, func(v *NewAdmin) { v.Password = "short" }, func(v *NewAdmin) { v.Password = strings.Repeat("a", 73) }, func(v *NewAdmin) { v.DisplayName = " " }, func(v *NewAdmin) { v.Roles = nil }, func(v *NewAdmin) { v.Roles = []string{"analyst", "analyst"} }} {
		v := valid
		modify(&v)
		if !errors.Is(validateAdmin(&v), ErrManagementValidation) {
			t.Fatal("invalid input accepted")
		}
	}
}

func TestManagementIntegration(t *testing.T) {
	url := os.Getenv("ZONENAN_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set local disposable ZONENAN_TEST_DATABASE_URL")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	email := "manage-" + suffix + "@example.invalid"
	key := "test_role_" + suffix
	hash, _ := bcrypt.GenerateFromPassword([]byte("initial-test-password"), bcrypt.MinCost)
	var actor int64
	if err = pool.QueryRow(ctx, `INSERT INTO admin_users(email,display_name,password_hash) VALUES($1,'test manager',$2) RETURNING id`, "actor-"+email, string(hash)).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_type='admin' AND actor_id=$1`, actor)
		pool.Exec(ctx, `DELETE FROM admin_users WHERE email=ANY($1)`, []string{email, "actor-" + email})
		pool.Exec(ctx, `DELETE FROM admin_roles WHERE role_key=$1`, key)
	}()
	s := NewStore(pool)
	input := NewAdmin{Email: email, DisplayName: "test account", Password: "long-test-password", Roles: []string{"analyst"}}
	if _, err = s.CreateAdmin(ctx, actor, input); !errors.Is(err, ErrManagementDenied) {
		t.Fatalf("unprivileged create: %v", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO admin_user_roles(admin_user_id,role_key) VALUES($1,'super_admin')`, actor); err != nil {
		t.Fatal(err)
	}
	var superCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM admin_users u JOIN admin_user_roles ur ON ur.admin_user_id=u.id WHERE u.active AND ur.role_key='super_admin'`).Scan(&superCount); err != nil {
		t.Fatal(err)
	}
	if superCount == 1 {
		if err = s.AssignRoles(ctx, actor, actor, []string{"analyst"}); !errors.Is(err, ErrLastSuperAdmin) {
			t.Fatalf("last super admin was not protected: %v", err)
		}
	}
	role := RoleInput{Key: key, Title: "测试角色", Description: "合成测试", Permissions: []string{"analytics.read", "admin.read"}}
	if err = s.SaveRole(ctx, actor, role, true); err != nil {
		t.Fatal(err)
	}
	input.Roles = []string{key}
	id, err := s.CreateAdmin(ctx, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateAdmin(ctx, actor, input); err == nil {
		t.Fatal("duplicate email accepted")
	}
	token, _, _, err := s.CreateSession(ctx, id, "127.0.0.1", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM admin_sessions WHERE admin_user_id=$1`, id)
	role.Permissions = []string{"admin.read"}
	if err = s.SaveRole(ctx, actor, role, false); err != nil {
		t.Fatal(err)
	}
	p, _, err := s.Resolve(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	for _, permission := range p.Permissions {
		if permission == "analytics.read" {
			t.Fatal("permission change did not apply to existing session")
		}
	}
	role.Permissions = []string{"invented.permission"}
	if !errors.Is(s.SaveRole(ctx, actor, role, false), ErrManagementValidation) {
		t.Fatal("unknown permission accepted")
	}
	role.Key = "super_admin"
	role.Permissions = []string{"admin.read"}
	if !errors.Is(s.SaveRole(ctx, actor, role, false), ErrManagementValidation) {
		t.Fatal("protected role changed")
	}
	if err = s.AssignRoles(ctx, actor, id, []string{"analyst"}); err != nil {
		t.Fatal(err)
	}
	if err = s.AssignRoles(ctx, actor, id, []string{"missing_role"}); !errors.Is(err, ErrManagementValidation) {
		t.Fatalf("unknown role: %v", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE actor_type='admin' AND actor_id=$1`, actor).Scan(&count); err != nil || count != 4 {
		t.Fatalf("audit records %d: %v", count, err)
	}
}
