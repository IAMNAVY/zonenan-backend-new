package httpapi

import (
	"crypto/sha256"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) handleFoodRecommendations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	args := []any{}
	where := []string{"m.status='approved'", "st.status='published'", "m.merchant_type IN ('restaurant','beverage')"}
	add := func(value any, clause string) {
		args = append(args, value)
		where = append(where, strings.ReplaceAll(clause, "?", "$"+strconv.Itoa(len(args))))
	}
	if v := strings.TrimSpace(q.Get("campus_id")); v != "" {
		add(v, "p.campus_id=?")
	}
	if v := strings.TrimSpace(q.Get("type")); v != "" {
		args = append(args, v, v)
		where = append(where, "(st.category=$"+strconv.Itoa(len(args)-1)+" OR $"+strconv.Itoa(len(args))+"=ANY(st.tags))")
	}
	if v := q.Get("budget_cents"); v != "" {
		if n, e := strconv.Atoi(v); e == nil {
			add(n, "COALESCE(st.average_price*100,0)<=?")
		}
	}
	if q.Get("open_now") == "true" {
		where = append(where, "merchant_is_open(st.business_hours,NOW())")
	}
	lat, _ := strconv.ParseFloat(q.Get("latitude"), 64)
	lon, _ := strconv.ParseFloat(q.Get("longitude"), 64)
	radius, _ := strconv.Atoi(q.Get("radius_m"))
	distance := "NULL::float8 distance_m"
	if lat != 0 && lon != 0 {
		args = append(args, lon, lat)
		pointA := len(args) - 1
		pointB := len(args)
		distance = `ST_Distance(p.location,ST_SetSRID(ST_MakePoint($` + strconv.Itoa(pointA) + `,$` + strconv.Itoa(pointB) + `),4326)::geography) distance_m`
		if radius > 0 {
			args = append(args, radius)
			where = append(where, `ST_DWithin(p.location,ST_SetSRID(ST_MakePoint($`+strconv.Itoa(pointA)+`,$`+strconv.Itoa(pointB)+`),4326)::geography,$`+strconv.Itoa(len(args))+`)`)
		}
	}
	seed := q.Get("seed")
	if seed == "" {
		seed = "default"
	}
	args = append(args, seed)
	order := `md5(m.id::text || $` + strconv.Itoa(len(args)) + `)`
	rows, e := s.pool.Query(r.Context(), `SELECT m.id merchant_id,m.name merchant_name,m.merchant_type,st.id store_id,st.name,st.category,st.tags,st.address,st.average_price,st.logo_url,st.business_hours,merchant_is_open(st.business_hours,NOW()) open_now,p.id poi_id,p.latitude,p.longitude,`+distance+` FROM merchants m JOIN merchant_stores st ON st.merchant_id=m.id LEFT JOIN pois p ON p.id=st.unified_poi_id WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT 50`, args...)
	if e != nil {
		Fail(w, 500, "推荐查询失败")
		return
	}
	values, _ := queryMaps(rows)
	OK(w, map[string]any{"items": values, "seed": seed, "filters_applied": map[string]any{"campus_id": q.Get("campus_id"), "open_now": q.Get("open_now") == "true", "budget_cents": q.Get("budget_cents"), "type": q.Get("type"), "radius_m": radius}})
}

func (s *Server) handleMerchantEvent(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var q struct {
		EventType string `json:"event_type"`
		Source    string `json:"source"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	allowed := map[string]bool{"impression": true, "view": true, "contact": true, "map_impression": true, "search_impression": true}
	if !allowed[q.EventType] {
		Fail(w, 400, "事件类型无效")
		return
	}
	uid := userIDFrom(r)
	var installation []byte
	if uid == 0 {
		raw := strings.TrimSpace(r.Header.Get("X-Installation-ID"))
		if raw == "" {
			Fail(w, 400, "缺少匿名安装标识")
			return
		}
		h := sha256.Sum256([]byte(raw))
		installation = h[:]
	}
	if q.Source == "" {
		q.Source = "app"
	}
	_, e := s.pool.Exec(r.Context(), `INSERT INTO merchant_events(merchant_id,user_id,installation_hash,event_type,source) SELECT id,NULLIF($2,0),$3,$4,$5 FROM merchants WHERE id=$1 AND status='approved' ON CONFLICT DO NOTHING`, id, uid, installation, q.EventType, q.Source)
	if e != nil {
		Fail(w, 500, "记录失败")
		return
	}
	OK(w, map[string]bool{"recorded": true})
}

func (s *Server) handleMerchantFavorite(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	favorite := r.Method != http.MethodDelete
	if favorite {
		_, e := s.pool.Exec(r.Context(), `INSERT INTO merchant_favorites(user_id,merchant_id) SELECT $1,id FROM merchants WHERE id=$2 AND status='approved' ON CONFLICT DO NOTHING`, uid, id)
		if e != nil {
			Fail(w, 500, "收藏失败")
			return
		}
		s.pool.Exec(r.Context(), `INSERT INTO merchant_events(merchant_id,user_id,event_type,source) VALUES($1,$2,'favorite','app') ON CONFLICT DO NOTHING`, id, uid)
	} else {
		s.pool.Exec(r.Context(), `DELETE FROM merchant_favorites WHERE user_id=$1 AND merchant_id=$2`, uid, id)
	}
	OK(w, map[string]bool{"favorite": favorite})
}

func (s *Server) handleAdminMerchantAnalytics(w http.ResponseWriter, r *http.Request) {
	rows, e := s.pool.Query(r.Context(), `SELECT e.merchant_id,m.name merchant_name,e.event_type,count(*) events,count(DISTINCT COALESCE(e.user_id::text,encode(e.installation_hash,'hex'))) actors FROM merchant_events e JOIN merchants m ON m.id=e.merchant_id WHERE e.occurred_at>=NOW()-INTERVAL '30 days' GROUP BY e.merchant_id,m.name,e.event_type ORDER BY events DESC`)
	if e != nil {
		v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
		return
	}
	v, _ := queryMaps(rows)
	v1Data(w, r, 200, v)
}
