package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"zonenan-backend/internal/config"
)

func TestCORSLeavesAppRequestsUntouched(t *testing.T) {
	s := &Server{cfg: &config.Config{WebAllowedOrigins: []string{"https://web.zonenan.pro"}}}
	called := false
	h := s.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(204) }))
	req := httptest.NewRequest(http.MethodGet, "/campus-map/incidents", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("App request without Origin must pass unchanged")
	}
}

func TestCORSAllowsConfiguredWebOrigin(t *testing.T) {
	s := &Server{cfg: &config.Config{WebAllowedOrigins: []string{"https://web.zonenan.pro"}}}
	h := s.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodOptions, "/auth/login", nil)
	req.Header.Set("Origin", "https://web.zonenan.pro")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "https://web.zonenan.pro" {
		t.Fatalf("unexpected CORS response: %d %v", rec.Code, rec.Header())
	}
}
