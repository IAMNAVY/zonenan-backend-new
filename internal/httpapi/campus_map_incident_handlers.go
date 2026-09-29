package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func incidentBounds(r *http.Request) (float64, float64, float64, float64, bool) {
	parse := func(name string, fallback float64) (float64, bool) {
		raw := strings.TrimSpace(r.URL.Query().Get(name))
		if raw == "" {
			return fallback, true
		}
		value, err := strconv.ParseFloat(raw, 64)
		return value, err == nil
	}
	minLat, a := parse("min_lat", -90)
	maxLat, b := parse("max_lat", 90)
	minLon, c := parse("min_lon", -180)
	maxLon, d := parse("max_lon", 180)
	valid := a && b && c && d && minLat >= -90 && maxLat <= 90 && minLon >= -180 && maxLon <= 180 && minLat <= maxLat && minLon <= maxLon
	return minLat, maxLat, minLon, maxLon, valid
}

func (s *Server) handleCampusMapIncidentPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := s.campusMapIncidents.Policy(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "读取突发事件配置失败")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("ETag", `"`+policy.Version+`"`)
	OK(w, policy)
}

func (s *Server) handleCampusMapIncidents(w http.ResponseWriter, r *http.Request) {
	minLat, maxLat, minLon, maxLon, valid := incidentBounds(r)
	if !valid {
		Fail(w, http.StatusBadRequest, "地图范围参数无效")
		return
	}
	types := []string{}
	for _, value := range strings.Split(r.URL.Query().Get("types"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			types = append(types, value)
		}
	}
	items, err := s.campusMapIncidents.List(r.Context(), userIDFrom(r), minLat, maxLat, minLon, maxLon, types, false)
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	policy, err := s.campusMapIncidents.Policy(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "读取突发事件配置失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	OK(w, map[string]any{
		"server_time":     time.Now().UTC(),
		"feature_enabled": policy.Enabled,
		"policy_version":  policy.Version,
		"incidents":       items,
	})
}

func (s *Server) handleSubmitCampusMapIncident(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Type      string  `json:"type"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	report, err := s.campusMapIncidents.Submit(r.Context(), userIDFrom(r), request.Type, request.Latitude, request.Longitude)
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	// The dedup key guarantees that merged nearby reports never fan out a
	// second notification for the same incident.
	_, _ = s.push.Enqueue(
		r.Context(),
		"campus_incidents",
		"校园突发事件",
		incidentPushBody(request.Type),
		fmt.Sprintf("campus-incident:%d", report.IncidentID),
		map[string]any{"kind": "campus_incident", "incident_id": report.IncidentID, "type": request.Type},
	)
	s.pushDispatcher.Kick()
	OK(w, report)
}

func incidentPushBody(kind string) string {
	switch kind {
	case "traffic_enforcement":
		return "附近有新的交通执法提醒，点击查看地图"
	case "road_closed":
		return "附近有新的封路信息，点击查看地图"
	case "congestion":
		return "附近有新的拥堵信息，点击查看地图"
	case "cat":
		return "校园地图有新的小猫动态"
	default:
		return "校园地图有新的突发事件"
	}
}

func (s *Server) handleVoteCampusMapIncident(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "incidentID"), 10, 64)
	if err != nil || id <= 0 {
		Fail(w, http.StatusBadRequest, "事件 ID 无效")
		return
	}
	var request struct {
		Value int `json:"value"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.campusMapIncidents.Vote(r.Context(), userIDFrom(r), id, request.Value); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	OK(w, map[string]any{"id": id, "value": request.Value})
}

func (s *Server) handleMyCampusMapIncidentReports(w http.ResponseWriter, r *http.Request) {
	items, err := s.campusMapIncidents.MyReports(r.Context(), userIDFrom(r))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "读取我的上报失败")
		return
	}
	OK(w, items)
}

func (s *Server) handleWithdrawCampusMapIncidentReport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "reportID"), 10, 64)
	if err != nil || id <= 0 {
		Fail(w, http.StatusBadRequest, "上报 ID 无效")
		return
	}
	if err := s.campusMapIncidents.Withdraw(r.Context(), userIDFrom(r), id); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	OK(w, map[string]any{"id": id, "status": "withdrawn"})
}

func (s *Server) handleAdminCampusMapIncidentPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := s.campusMapIncidents.Policy(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "读取突发事件配置失败")
		return
	}
	OK(w, policy)
}

func (s *Server) handleAdminSetCampusMapIncidentPolicy(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.campusMapIncidents.SetPolicyValue(r.Context(), strings.TrimSpace(request.Key), strings.TrimSpace(request.Value)); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	policy, err := s.campusMapIncidents.Policy(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "读取更新后的配置失败")
		return
	}
	OK(w, policy)
}

func (s *Server) handleAdminCampusMapIncidents(w http.ResponseWriter, r *http.Request) {
	items, err := s.campusMapIncidents.List(r.Context(), 0, -90, 90, -180, 180, nil, true)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "读取突发事件失败")
		return
	}
	OK(w, items)
}

func (s *Server) handleAdminRemoveCampusMapIncident(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID     int64  `json:"id"`
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.campusMapIncidents.AdminRemove(r.Context(), request.ID, request.Reason); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	OK(w, map[string]any{"id": request.ID, "status": "admin_removed"})
}
