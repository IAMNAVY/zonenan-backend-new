package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"zonenan-backend/internal/config"
	"zonenan-backend/internal/maptiles"
)

func TestLegacyManagementRetired(t *testing.T) {
	s := &Server{cfg: &config.Config{AdminSecret: "still-not-accepted"}, mapTiles: maptiles.New(maptiles.Config{CacheDir: t.TempDir()})}
	handler := s.Router()
	for _, path := range []string{"/panel", "/panel/", "/admin", "/admin/users", "/admin/settings", "/admin/memberships/grant"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodPut} {
			req := httptest.NewRequest(method, path, nil)
			req.Header.Set("X-Admin-Secret", "still-not-accepted")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusGone {
				t.Errorf("%s %s: got %d", method, path, w.Code)
			}
		}
	}
	for _, path := range []string{"/admin-api/v1/auth/me", "/admin-api/v1/auth/password"} {
		method := http.MethodGet
		if path == "/admin-api/v1/auth/password" {
			method = http.MethodPost
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("new route %s: %d", path, w.Code)
		}
	}
}
