package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zonenan-backend/internal/auth"
	"zonenan-backend/internal/config"
	"zonenan-backend/internal/maptiles"
)

func TestRestrictedCampusSessionDeniedAcrossAccountRoutes(t *testing.T) {
	s := &Server{cfg: &config.Config{}, tokens: auth.NewTokenManager("synthetic-key", time.Hour), mapTiles: maptiles.New(maptiles.Config{CacheDir: t.TempDir(), UploadSigningKey: "synthetic-key"})}
	token, _ := s.tokens.IssueCampusClaim(1, "test-device")
	router := s.Router()
	for _, route := range []struct{ method, path string }{
		{"POST", "/auth/profile"}, {"POST", "/auth/bind-email"}, {"POST", "/auth/change-password"},
		{"GET", "/auth/devices"}, {"POST", "/auth/devices/revoke"}, {"POST", "/auth/device-challenges/approve"},
		{"POST", "/auth/passkey/register-begin"}, {"GET", "/auth/passkey/list"},
		{"GET", "/membership/me"}, {"POST", "/membership/activate"},
		{"GET", "/grade/auth-status"}, {"POST", "/grade/search"}, {"POST", "/grade/authorize"}, {"GET", "/grade/mine"},
		{"POST", "/campus-map/contributions"}, {"GET", "/campus-map/incidents/mine"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			r := httptest.NewRequest(route.method, route.path, nil)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("restricted session got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestClaimUserDoesNotExposeStoredAccount(t *testing.T) {
	u := campusClaimUser(12)
	for _, key := range []string{"email", "avatar_url", "is_beta", "is_premium", "is_whitelisted"} {
		if _, ok := u[key]; ok {
			t.Fatalf("claim response exposes %s", key)
		}
	}
	if u["campus_verified"] != false || u["role"] != "user" {
		t.Fatal("claim acquired privileges")
	}
}
