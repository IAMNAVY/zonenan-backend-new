package httpapi

import (
	"encoding/json"
	"net/http"
)

func (s *Server) handleWebConfig(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	allowed := false
	for _, item := range s.cfg.WebAllowedOrigins {
		if origin == item {
			allowed = true
			break
		}
	}
	if !allowed {
		Fail(w, http.StatusForbidden, "Web 来源未授权")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	OK(w, map[string]any{"tianditu_web_key": s.cfg.TiandituWebKey, "passkey_rp_id": s.cfg.PasskeyRPID, "tile_cache_max_bytes": 268435456})
}

func (s *Server) handleWebAuthnRelatedOrigins(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(map[string]any{"origins": s.cfg.PasskeyRelatedOrigins})
}
