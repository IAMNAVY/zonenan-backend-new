package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"zonenan-backend/internal/store"
)

const analyticsBatchLimit = 20

var (
	canonicalUUID   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	safeAnalyticsID = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._:-]{0,63}$`)
	appVersionID    = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,31}$`)
	deviceMetadata  = regexp.MustCompile(`^[ -~]{1,64}$`)
)

var analyticsNames = map[string]bool{
	"timetable": true, "status": true, "functions": true, "news": true,
	"campus_card": true, "library": true, "grades": true, "grade_rating": true,
	"schedule": true, "shuttle": true, "classroom": true, "academic_calendar": true,
	"campus_map": true,
}

var analyticsWidgetKinds = map[string]bool{"today": true, "weekly": true}
var analyticsWidgetSizes = map[string]bool{"small": true, "medium": true, "large": true}
var analyticsWidgetRefreshModes = map[string]bool{"auto": true, "manual": true}
var analyticsWidgetActions = map[string]bool{
	"open_app": true, "refresh": true, "previous": true,
	"next": true, "today": true, "content": true,
}

var analyticsSources = map[string]bool{
	"bottom_nav": true, "status_card": true, "functions_grid": true,
	"mine_grid": true, "deep_link": true, "system": true,
}

type analyticsBatchRequest struct {
	ProtocolVersion int                     `json:"protocol_version"`
	Device          *analyticsDeviceRequest `json:"device,omitempty"`
	Events          []analyticsEventRequest `json:"events"`
}

type analyticsDeviceRequest struct {
	Platform  string  `json:"platform"`
	OSVersion *string `json:"os_version,omitempty"`
	Brand     *string `json:"brand,omitempty"`
	Model     *string `json:"model,omitempty"`
}

type analyticsEventRequest struct {
	EventID           string  `json:"event_id"`
	InstallationID    string  `json:"installation_id"`
	SessionID         string  `json:"session_id"`
	EventType         string  `json:"event_type"`
	OccurredAt        string  `json:"occurred_at"`
	Platform          string  `json:"platform"`
	AppVersion        string  `json:"app_version"`
	Screen            *string `json:"screen,omitempty"`
	Feature           *string `json:"feature,omitempty"`
	Reason            *string `json:"reason,omitempty"`
	Source            *string `json:"source,omitempty"`
	Result            *string `json:"result,omitempty"`
	ErrorCategory     *string `json:"error_category,omitempty"`
	DurationSeconds   *int    `json:"duration_seconds,omitempty"`
	PlacementID       *string `json:"placement_id,omitempty"`
	CampaignID        *string `json:"campaign_id,omitempty"`
	CreativeID        *string `json:"creative_id,omitempty"`
	WidgetKind        *string `json:"widget_kind,omitempty"`
	WidgetSize        *string `json:"widget_size,omitempty"`
	WidgetRefreshMode *string `json:"widget_refresh_mode,omitempty"`
	WidgetAction      *string `json:"widget_action,omitempty"`
}

func (s *Server) handleAnalyticsEvents(w http.ResponseWriter, r *http.Request) {
	var req analyticsBatchRequest
	if err := decodeStrictAnalyticsJSON(w, r, &req); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ProtocolVersion != 1 {
		Fail(w, http.StatusBadRequest, "不支持的 protocol_version")
		return
	}
	if len(req.Events) == 0 || len(req.Events) > analyticsBatchLimit {
		Fail(w, http.StatusBadRequest, "events 数量须为 1 到 20")
		return
	}
	device, err := validateAnalyticsDevice(req.Device)
	if err != nil {
		Fail(w, http.StatusBadRequest, "device: "+err.Error())
		return
	}

	uid := userIDFrom(r)
	userHash := []byte(nil)
	if uid != 0 {
		userHash = analyticsHMAC(s.cfg.AnalyticsPepper, "user", strconv.FormatInt(uid, 10))
	}
	loc := shanghaiLocation()
	now := time.Now().In(loc)
	events := make([]store.AnalyticsEvent, 0, len(req.Events))
	for i := range req.Events {
		e, err := validateAnalyticsEvent(req.Events[i], now)
		if err != nil {
			Fail(w, http.StatusBadRequest, "events["+strconv.Itoa(i)+"]: "+err.Error())
			return
		}
		e.UserHash = userHash
		e.UserID = uid
		e.InstallationHash = analyticsHMAC(s.cfg.AnalyticsPepper, "installation", req.Events[i].InstallationID)
		events = append(events, e)
	}

	inserted, err := s.analytics.InsertBatch(r.Context(), events, device)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "事件保存失败")
		return
	}
	OK(w, map[string]int64{"accepted": inserted, "duplicates": int64(len(events)) - inserted})
}

func decodeStrictAnalyticsJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("请求体解析失败")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("请求体只能包含一个 JSON 对象")
	}
	return nil
}

func validateAnalyticsDevice(in *analyticsDeviceRequest) (*store.AnalyticsDeviceInfo, error) {
	if in == nil {
		return nil, nil
	}
	platforms := map[string]bool{"android": true, "ios": true, "windows": true, "macos": true, "linux": true, "web": true}
	if !platforms[in.Platform] {
		return nil, errors.New("platform 不在允许列表")
	}
	for _, value := range []*string{in.OSVersion, in.Brand, in.Model} {
		if value != nil && !deviceMetadata.MatchString(*value) {
			return nil, errors.New("设备元数据格式无效")
		}
	}
	return &store.AnalyticsDeviceInfo{
		Platform: in.Platform, OSVersion: in.OSVersion, Brand: in.Brand, Model: in.Model,
	}, nil
}

func validateAnalyticsEvent(in analyticsEventRequest, now time.Time) (store.AnalyticsEvent, error) {
	normalizeAnalyticsOptionals(&in)
	if !canonicalUUID.MatchString(in.EventID) || !canonicalUUID.MatchString(in.InstallationID) || !canonicalUUID.MatchString(in.SessionID) {
		return store.AnalyticsEvent{}, errors.New("UUID 字段必须为规范小写 UUID")
	}
	if !strings.HasSuffix(in.OccurredAt, "Z") {
		return store.AnalyticsEvent{}, errors.New("occurred_at 必须为 UTC RFC3339 时间")
	}
	occurred, err := time.Parse(time.RFC3339Nano, in.OccurredAt)
	if err != nil || occurred.Location() != time.UTC {
		return store.AnalyticsEvent{}, errors.New("occurred_at 必须为 UTC RFC3339 时间")
	}
	if occurred.Before(now.AddDate(0, 0, -30)) || occurred.After(now.Add(5*time.Minute)) {
		return store.AnalyticsEvent{}, errors.New("occurred_at 超出允许范围")
	}
	platforms := map[string]bool{"android": true, "ios": true, "windows": true, "macos": true, "linux": true, "web": true}
	if !platforms[in.Platform] {
		return store.AnalyticsEvent{}, errors.New("platform 不在允许列表")
	}
	if !appVersionID.MatchString(in.AppVersion) {
		return store.AnalyticsEvent{}, errors.New("app_version 格式无效")
	}
	if in.Screen != nil && !analyticsNames[*in.Screen] {
		return store.AnalyticsEvent{}, errors.New("screen 不在允许列表")
	}
	if in.Feature != nil && !analyticsNames[*in.Feature] {
		return store.AnalyticsEvent{}, errors.New("feature 不在允许列表")
	}
	if in.Source != nil && !analyticsSources[*in.Source] {
		return store.AnalyticsEvent{}, errors.New("source 不在允许列表")
	}
	if in.Reason != nil && *in.Reason != "cold_start" && *in.Reason != "background_15m" {
		return store.AnalyticsEvent{}, errors.New("reason 不在允许列表")
	}
	if in.Result != nil && *in.Result != "success" && *in.Result != "empty" && *in.Result != "cancelled" && *in.Result != "error" {
		return store.AnalyticsEvent{}, errors.New("result 不在允许列表")
	}
	if in.ErrorCategory != nil && !map[string]bool{"network": true, "auth": true, "permission": true, "server": true, "parse": true, "unavailable": true, "unknown": true}[*in.ErrorCategory] {
		return store.AnalyticsEvent{}, errors.New("error_category 不在允许列表")
	}
	if in.WidgetKind != nil && !analyticsWidgetKinds[*in.WidgetKind] {
		return store.AnalyticsEvent{}, errors.New("widget_kind 不在允许列表")
	}
	if in.WidgetSize != nil && !analyticsWidgetSizes[*in.WidgetSize] {
		return store.AnalyticsEvent{}, errors.New("widget_size 不在允许列表")
	}
	if in.WidgetRefreshMode != nil && !analyticsWidgetRefreshModes[*in.WidgetRefreshMode] {
		return store.AnalyticsEvent{}, errors.New("widget_refresh_mode 不在允许列表")
	}
	if in.WidgetAction != nil && !analyticsWidgetActions[*in.WidgetAction] {
		return store.AnalyticsEvent{}, errors.New("widget_action 不在允许列表")
	}
	if in.DurationSeconds != nil && (*in.DurationSeconds < 0 || *in.DurationSeconds > 86400) {
		return store.AnalyticsEvent{}, errors.New("duration_seconds 超出范围")
	}
	for _, id := range []*string{in.PlacementID, in.CampaignID, in.CreativeID} {
		if id != nil && !safeAnalyticsID.MatchString(*id) {
			return store.AnalyticsEvent{}, errors.New("广告标识符格式无效")
		}
	}

	if err := validateAnalyticsShape(in); err != nil {
		return store.AnalyticsEvent{}, err
	}
	return store.AnalyticsEvent{
		EventID: in.EventID, SessionID: in.SessionID, EventType: in.EventType,
		OccurredAt: occurred, Platform: in.Platform, AppVersion: in.AppVersion,
		Screen: in.Screen, Feature: in.Feature, Reason: in.Reason, Source: in.Source,
		Result: in.Result, ErrorCategory: in.ErrorCategory, DurationSeconds: in.DurationSeconds,
		PlacementID: in.PlacementID, CampaignID: in.CampaignID, CreativeID: in.CreativeID,
		WidgetKind: in.WidgetKind, WidgetSize: in.WidgetSize,
		WidgetRefreshMode: in.WidgetRefreshMode, WidgetAction: in.WidgetAction,
	}, nil
}

func normalizeAnalyticsOptionals(e *analyticsEventRequest) {
	for _, p := range []**string{
		&e.Screen, &e.Feature, &e.Reason, &e.Source, &e.Result, &e.ErrorCategory,
		&e.PlacementID, &e.CampaignID, &e.CreativeID, &e.WidgetKind, &e.WidgetSize,
		&e.WidgetRefreshMode, &e.WidgetAction,
	} {
		if *p != nil && **p == "" {
			*p = nil
		}
	}
}

func validateAnalyticsShape(e analyticsEventRequest) error {
	no := func(values ...any) bool {
		for _, value := range values {
			switch v := value.(type) {
			case *string:
				if v != nil {
					return false
				}
			case *int:
				if v != nil {
					return false
				}
			}
		}
		return true
	}
	adIDs := e.PlacementID != nil && e.CampaignID != nil && e.CreativeID != nil
	switch e.EventType {
	case "session_start":
		if e.Reason == nil || !no(e.Screen, e.Feature, e.Source, e.Result, e.ErrorCategory, e.DurationSeconds, e.PlacementID, e.CampaignID, e.CreativeID) {
			return errors.New("session_start 字段组合无效")
		}
	case "screen_click":
		if e.Screen == nil || e.Source == nil || !no(e.Feature, e.Reason, e.Result, e.ErrorCategory, e.DurationSeconds, e.PlacementID, e.CampaignID, e.CreativeID) {
			return errors.New("screen_click 字段组合无效")
		}
	case "screen_view":
		if e.Screen == nil || !no(e.Feature, e.Reason, e.Result, e.ErrorCategory, e.DurationSeconds, e.PlacementID, e.CampaignID, e.CreativeID) {
			return errors.New("screen_view 字段组合无效")
		}
	case "screen_duration":
		if e.Screen == nil || e.DurationSeconds == nil || !no(e.Feature, e.Reason, e.Source, e.Result, e.ErrorCategory, e.PlacementID, e.CampaignID, e.CreativeID) {
			return errors.New("screen_duration 字段组合无效")
		}
	case "feature_open":
		if e.Feature == nil || e.Source == nil || !no(e.Screen, e.Reason, e.Result, e.ErrorCategory, e.DurationSeconds, e.PlacementID, e.CampaignID, e.CreativeID) {
			return errors.New("feature_open 字段组合无效")
		}
	case "feature_result":
		if e.Feature == nil || e.Result == nil || (*e.Result == "error") != (e.ErrorCategory != nil) || !no(e.Screen, e.Reason, e.Source, e.DurationSeconds, e.PlacementID, e.CampaignID, e.CreativeID) {
			return errors.New("feature_result 字段组合无效")
		}
	case "ad_impression", "ad_click":
		if !adIDs || !no(e.Screen, e.Feature, e.Reason, e.Source, e.Result, e.ErrorCategory, e.DurationSeconds) {
			return errors.New("广告事件字段组合无效")
		}
	case "widget_enabled", "widget_disabled", "widget_render", "widget_refresh":
		if e.WidgetKind == nil || e.WidgetSize == nil || e.WidgetRefreshMode == nil || !no(e.WidgetAction, e.ErrorCategory) {
			return errors.New("widget 字段组合无效")
		}
	case "widget_click", "widget_click_open_app":
		if e.WidgetKind == nil || e.WidgetSize == nil || e.WidgetRefreshMode == nil || e.WidgetAction == nil || !no(e.ErrorCategory) {
			return errors.New("widget_click 字段组合无效")
		}
	case "widget_refresh_failed":
		if e.WidgetKind == nil || e.WidgetSize == nil || e.WidgetRefreshMode == nil || e.ErrorCategory == nil || !no(e.WidgetAction) {
			return errors.New("widget_refresh_failed 字段组合无效")
		}
	default:

		return errors.New("event_type 不在允许列表")
	}
	return nil
}

func analyticsHMAC(pepper, domain, value string) []byte {
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(domain))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return mac.Sum(nil)
}

func analyticsRange(r *http.Request) (time.Time, time.Time, int) {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || days < 1 || days > 90 {
		days = 30
	}
	loc := shanghaiLocation()
	now := time.Now().In(loc)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	return to.AddDate(0, 0, 1-days), to, days
}

func shanghaiLocation() *time.Location {
	return locationOrFixed("Asia/Shanghai", 8*60*60)
}

func locationOrFixed(name string, fallbackOffsetSeconds int) *time.Location {
	loc, err := time.LoadLocation(name)
	if err == nil {
		return loc
	}
	return time.FixedZone(name, fallbackOffsetSeconds)
}

func (s *Server) handleAdminAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	from, to, days := analyticsRange(r)
	out, err := s.analytics.Summary(r.Context(), from, to, days)
	if err != nil {
		log.Printf("admin analytics summary query failed: %v", err)
		Fail(w, http.StatusInternalServerError, "分析汇总查询失败")
		return
	}
	OK(w, out)
}

func (s *Server) handleAdminAnalyticsTrends(w http.ResponseWriter, r *http.Request) {
	from, to, _ := analyticsRange(r)
	out, err := s.analytics.Trends(r.Context(), from, to)
	if err != nil {
		log.Printf("admin analytics trends query failed: %v", err)
		Fail(w, http.StatusInternalServerError, "分析趋势查询失败")
		return
	}
	OK(w, out)
}

func (s *Server) handleAdminAnalyticsExclusions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		ids, err := s.analytics.ExcludedUsers(r.Context())
		if err != nil {
			Fail(w, http.StatusInternalServerError, "排除列表查询失败")
			return
		}
		OK(w, ids)
		return
	}
	var req struct {
		UserID   int64 `json:"user_id"`
		Excluded bool  `json:"excluded"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.UserID <= 0 {
		Fail(w, http.StatusBadRequest, "user_id 无效")
		return
	}
	hash := analyticsHMAC(s.cfg.AnalyticsPepper, "user", strconv.FormatInt(req.UserID, 10))
	if err := s.analytics.SetUserExcluded(r.Context(), req.UserID, hash, req.Excluded); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			Fail(w, http.StatusNotFound, "用户不存在")
			return
		}
		Fail(w, http.StatusInternalServerError, "排除设置失败")
		return
	}
	OK(w, map[string]any{"user_id": req.UserID, "excluded": req.Excluded})
}
