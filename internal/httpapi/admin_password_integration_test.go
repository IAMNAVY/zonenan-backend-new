package httpapi

import (
	"context"
	"fmt"
	"golang.org/x/crypto/bcrypt"
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

func TestAdminPasswordHTTPIntegration(t *testing.T) {
	url := os.Getenv("ZONENAN_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires explicit local test database")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	hash, _ := bcrypt.GenerateFromPassword([]byte("original-password"), bcrypt.DefaultCost)
	var id int64
	email := fmt.Sprintf("http-password-%d@example.invalid", time.Now().UnixNano())
	if err = pool.QueryRow(ctx, `INSERT INTO admin_users(email,display_name,password_hash) VALUES($1,'HTTP test',$2) RETURNING id`, email, string(hash)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	defer func() {
		pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_type='admin' AND actor_id=$1`, id)
		pool.Exec(ctx, `DELETE FROM admin_sessions WHERE admin_user_id=$1`, id)
		pool.Exec(ctx, `DELETE FROM admin_users WHERE id=$1`, id)
	}()
	accounts := adminauth.NewStore(pool)
	token, csrf, _, err := accounts.CreateSession(ctx, id, "127.0.0.1", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: &config.Config{Env: "production"}, adminAccounts: accounts, mapTiles: maptiles.New(maptiles.Config{CacheDir: t.TempDir()})}
	router := s.Router()
	body := `{"current_password":"original-password","new_password":"replacement-password"}`
	for _, tt := range []struct {
		csrf string
		want int
	}{{"", 403}, {"incorrect", 403}, {csrf, 200}, {csrf, 401}} {
		req := httptest.NewRequest(http.MethodPost, "/admin-api/v1/auth/password", strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: adminSessionCookie, Value: token})
		req.Header.Set("X-CSRF-Token", tt.csrf)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != tt.want {
			t.Fatalf("got %d want %d: %s", w.Code, tt.want, w.Body.String())
		}
		if tt.want == 200 {
			if len(w.Result().Cookies()) != 2 {
				t.Fatal("session and CSRF cookies not cleared")
			}
			for _, c := range w.Result().Cookies() {
				if c.MaxAge != -1 || !c.Secure {
					t.Fatal("incorrect cookie deletion")
				}
			}
		}
	}
}
