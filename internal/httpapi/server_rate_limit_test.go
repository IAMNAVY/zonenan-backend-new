package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"zonenan-backend/internal/config"
	"zonenan-backend/internal/maptiles"
)

func TestMapTileRoutesDoNotUseAPIRateLimits(t *testing.T) {
	server := &Server{
		cfg: &config.Config{AdminSecret: "test-admin-secret"},
		mapTiles: maptiles.New(maptiles.Config{
			CacheDir:         t.TempDir(),
			UploadSigningKey: "test-upload-key",
		}),
	}
	router := server.Router()
	path := "/map/tiles/vec/18/213302/109671.png"

	// This exceeds the global API allowance (240/minute). A cache miss should
	// keep returning its normal 404 response instead of becoming a 429.
	for i := 0; i < 241; i++ {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET request %d status = %d, want 404", i+1, response.Code)
		}
	}

	// This exceeds the former tile-upload allowance (120/minute). Invalid
	// uploads must continue to be rejected by the signed-token check, not by
	// an IP quota shared by every user behind the same NAT.
	for i := 0; i < 121; i++ {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("PUT request %d status = %d, want 401", i+1, response.Code)
		}
	}
}
