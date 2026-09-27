package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"zonenan-backend/internal/adminauth"
	"zonenan-backend/internal/config"
	"zonenan-backend/internal/db"
	"zonenan-backend/internal/maptiles"
)

func TestAdminManagementRoutesRequireLogin(t *testing.T) {
	s := &Server{cfg: &config.Config{}, mapTiles: maptiles.New(maptiles.Config{CacheDir: t.TempDir()})}
	router := s.Router()
	for _, route := range [][2]string{{"POST", "/admins"}, {"POST", "/roles"}, {"PUT", "/roles"}, {"PUT", "/admins/roles"}, {"GET", "/permissions"}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(route[0], "/admin-api/v1"+route[1], strings.NewReader(`{}`)))
		if w.Code != 401 {
			t.Fatalf("%v: %d", route, w.Code)
		}
	}
}

func TestAdminManagementHTTPIntegration(t *testing.T) {
	url := os.Getenv("ZONENAN_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires explicit local disposable database")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	email := "http-admin-" + suffix + "@example.invalid"
	var id int64
	if err = pool.QueryRow(ctx, `INSERT INTO admin_users(email,display_name,password_hash) VALUES($1,'HTTP test','unused') RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	key := "http_role_" + suffix
	defer func() {
		pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_type='admin' AND actor_id=$1`, id)
		pool.Exec(ctx, `DELETE FROM admin_users WHERE id=$1`, id)
		pool.Exec(ctx, `DELETE FROM admin_roles WHERE role_key=$1`, key)
	}()
	accounts := adminauth.NewStore(pool)
	token, csrf, _, err := accounts.CreateSession(ctx, id, "127.0.0.1", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: &config.Config{Env: "production"}, adminAccounts: accounts, mapTiles: maptiles.New(maptiles.Config{CacheDir: t.TempDir()})}
	router := s.Router()
	request := func(method, path, body, csrfHeader string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, "/admin-api/v1"+path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: adminSessionCookie, Value: token})
		req.Header.Set("X-CSRF-Token", csrfHeader)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
	}
	for _, route := range [][2]string{{"POST", "/admins"}, {"POST", "/roles"}, {"PUT", "/roles"}, {"PUT", "/admins/roles"}, {"GET", "/permissions"}} {
		request(route[0], route[1], `{}`, csrf, 403)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO admin_user_roles(admin_user_id,role_key) VALUES($1,'super_admin')`, id); err != nil {
		t.Fatal(err)
	}
	for _, route := range [][2]string{{"POST", "/admins"}, {"POST", "/roles"}, {"PUT", "/roles"}, {"PUT", "/admins/roles"}} {
		request(route[0], route[1], `{}`, "", 403)
	}
	request("GET", "/permissions", "", csrf, 200)
	body := fmt.Sprintf(`{"key":%q,"title":"HTTP 测试","permissions":["analytics.read"]}`, key)
	request("POST", "/roles", body, csrf, 200)
	request("POST", "/roles", body, csrf, 409)
	request("PUT", "/roles", body, csrf, 200)
	request("POST", "/admins", `{"email":"invalid"}`, csrf, 400)
}
