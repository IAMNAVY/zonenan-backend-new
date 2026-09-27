package httpapi

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"zonenan-backend/internal/config"
	"zonenan-backend/internal/maptiles"
)

func TestLegacyRouteSurfaceContainsCriticalClientContracts(t *testing.T) {
	server := &Server{
		cfg: &config.Config{AdminSecret: "contract-test-admin-secret"},
		mapTiles: maptiles.New(maptiles.Config{
			CacheDir:         t.TempDir(),
			UploadSigningKey: "contract-test-upload-key",
		}),
	}

	found := map[string]bool{}
	routes, ok := server.Router().(chi.Routes)
	if !ok {
		t.Fatal("router does not expose chi routes")
	}
	if err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		found[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"GET /healthz",
		"GET /app/version",
		"GET /app/changelog",
		"GET /app/download-url",
		"POST /auth/login",
		"POST /auth/refresh",
		"POST /auth/cas-login",
		"GET /auth/me",
		"POST /grade/sync",
		"POST /grade/search",
		"GET /membership/me",
		"GET /announcements",
		"GET /home/ads",
		"GET /legal/{docType}",
		"POST /analytics/events",
		"POST /telemetry/crash",
		"GET /classroom-data/manifest",
		"GET /campus-map/manifest",
		"GET /campus-map/places",
		"POST /campus-map/contributions",
		"GET /map/tiles/{layer}/{z}/{x}/{y}.png",
		"GET /panel",
		"GET /admin/users",
	}
	for _, contract := range want {
		if !found[contract] {
			t.Errorf("legacy route missing: %s", contract)
		}
	}
}
