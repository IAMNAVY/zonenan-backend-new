package adminauth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

var ErrManagementValidation = errors.New("invalid administrator or role")
var ErrManagementDenied = errors.New("administrator management denied")
var ErrLastSuperAdmin = errors.New("cannot remove the last active super administrator")
var roleKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

type NewAdmin struct {
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	Password    string   `json:"password"`
	Roles       []string `json:"roles"`
}

type RoleInput struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

func validateAdmin(v *NewAdmin) error {
	v.Email = strings.ToLower(strings.TrimSpace(v.Email))
	v.DisplayName = strings.TrimSpace(v.DisplayName)
	parsed, err := mail.ParseAddress(v.Email)
	if err != nil || parsed.Address != v.Email || len(v.Email) > 254 || v.DisplayName == "" || len(v.DisplayName) > 200 || len(v.Password) < 12 || len(v.Password) > 72 || !uniqueKeys(v.Roles) {
		return ErrManagementValidation
	}
	return nil
}

func uniqueKeys(keys []string) bool {
	if len(keys) == 0 || len(keys) > 128 {
		return false
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if key == "" || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

// Serialize access-control writes and recheck the actor inside the transaction.
func (s *Store) managementTx(ctx context.Context, actorID int64) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `LOCK TABLE admin_users, admin_roles, admin_user_roles, admin_role_permissions IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	var allowed bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_users u JOIN admin_user_roles ur ON ur.admin_user_id=u.id JOIN admin_role_permissions rp ON rp.role_key=ur.role_key WHERE u.id=$1 AND u.active AND rp.permission_key='admin.manage')`, actorID).Scan(&allowed)
	if err != nil || !allowed {
		tx.Rollback(ctx)
		if err != nil {
			return nil, err
		}
		return nil, ErrManagementDenied
	}
	return tx, nil
}

func managementAudit(ctx context.Context, tx pgx.Tx, actorID int64, action, resource, id string) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,resource_type,resource_id,status_code) VALUES('admin',$1,$2,$3,$4,200)`, actorID, action, resource, id)
	return err
}

func (s *Store) CreateAdmin(ctx context.Context, actorID int64, input NewAdmin) (int64, error) {
	if err := validateAdmin(&input); err != nil {
		return 0, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	tx, err := s.managementTx(ctx, actorID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM admin_roles WHERE role_key=ANY($1)`, input.Roles).Scan(&count); err != nil {
		return 0, err
	}
	if count != len(input.Roles) {
		return 0, ErrManagementValidation
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO admin_users(email,display_name,password_hash) VALUES($1,$2,$3) RETURNING id`, input.Email, input.DisplayName, string(hash)).Scan(&id); err != nil {
		return 0, err
	}
	for _, role := range input.Roles {
		if _, err = tx.Exec(ctx, `INSERT INTO admin_user_roles(admin_user_id,role_key) VALUES($1,$2)`, id, role); err != nil {
			return 0, err
		}
	}
	if err = managementAudit(ctx, tx, actorID, "admin.create", "admin_user", fmt.Sprint(id)); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) ListPermissions(ctx context.Context) ([]map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT permission_key,description FROM admin_permissions ORDER BY permission_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var key, description string
		if err = rows.Scan(&key, &description); err != nil {
			return nil, err
		}
		out = append(out, map[string]string{"key": key, "description": description})
	}
	return out, rows.Err()
}

func (s *Store) SaveRole(ctx context.Context, actorID int64, input RoleInput, create bool) error {
	input.Title = strings.TrimSpace(input.Title)
	if !roleKeyPattern.MatchString(input.Key) || input.Key == "super_admin" || input.Title == "" || len(input.Title) > 200 || len(input.Description) > 2000 || !uniqueKeys(input.Permissions) {
		return ErrManagementValidation
	}
	tx, err := s.managementTx(ctx, actorID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM admin_permissions WHERE permission_key=ANY($1)`, input.Permissions).Scan(&count); err != nil {
		return err
	}
	if count != len(input.Permissions) {
		return ErrManagementValidation
	}
	action := "admin.role.update"
	if create {
		action = "admin.role.create"
		_, err = tx.Exec(ctx, `INSERT INTO admin_roles(role_key,title,description,system_role) VALUES($1,$2,$3,FALSE)`, input.Key, input.Title, input.Description)
	} else {
		result, e := tx.Exec(ctx, `UPDATE admin_roles SET title=$2,description=$3 WHERE role_key=$1`, input.Key, input.Title, input.Description)
		err = e
		if err == nil && result.RowsAffected() != 1 {
			return ErrManagementValidation
		}
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM admin_role_permissions WHERE role_key=$1`, input.Key); err != nil {
		return err
	}
	for _, permission := range input.Permissions {
		if _, err = tx.Exec(ctx, `INSERT INTO admin_role_permissions(role_key,permission_key) VALUES($1,$2)`, input.Key, permission); err != nil {
			return err
		}
	}
	if err = managementAudit(ctx, tx, actorID, action, "admin_role", input.Key); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AssignRoles(ctx context.Context, actorID, adminID int64, roles []string) error {
	if adminID <= 0 || !uniqueKeys(roles) {
		return ErrManagementValidation
	}
	tx, err := s.managementTx(ctx, actorID)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM admin_roles WHERE role_key=ANY($1)`, roles).Scan(&count); err != nil {
		return err
	}
	if count != len(roles) {
		return ErrManagementValidation
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_users WHERE id=$1 AND active)`, adminID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrManagementValidation
	}
	if _, err = tx.Exec(ctx, `DELETE FROM admin_user_roles WHERE admin_user_id=$1`, adminID); err != nil {
		return err
	}
	for _, role := range roles {
		if _, err = tx.Exec(ctx, `INSERT INTO admin_user_roles(admin_user_id,role_key) VALUES($1,$2)`, adminID, role); err != nil {
			return err
		}
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM admin_users u JOIN admin_user_roles ur ON ur.admin_user_id=u.id WHERE u.active AND ur.role_key='super_admin'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return ErrLastSuperAdmin
	}
	if err = managementAudit(ctx, tx, actorID, "admin.roles.update", "admin_user", fmt.Sprint(adminID)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
