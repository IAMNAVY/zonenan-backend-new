package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleRentalMedia(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	file, header, e := r.FormFile("file")
	if e != nil {
		Fail(w, 400, "请选择图片")
		return
	}
	defer file.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	kind := http.DetectContentType(head[:n])
	ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[kind]
	if ext == "" {
		Fail(w, 400, "仅支持 JPEG、PNG 和 WebP")
		return
	}
	file.Seek(0, 0)
	raw, _, e := merchantFileToken()
	if e != nil {
		Fail(w, 500, "生成文件名失败")
		return
	}
	dir := filepath.Join(s.cfg.MerchantUploadDir, "rental", strconv.FormatInt(uid, 10))
	if e = os.MkdirAll(dir, 0750); e != nil {
		Fail(w, 500, "创建目录失败")
		return
	}
	name := raw + ext
	target, e := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if e != nil {
		Fail(w, 500, "保存失败")
		return
	}
	written, e := io.Copy(target, io.LimitReader(file, 5<<20+1))
	target.Close()
	if e != nil || written > 5<<20 {
		os.Remove(filepath.Join(dir, name))
		Fail(w, 400, "图片不能超过 5 MB")
		return
	}
	OK(w, map[string]any{"url": fmt.Sprintf("/rental-media/%d/%s", uid, name), "name": header.Filename, "size_bytes": written})
}

func (s *Server) handleAppPOIs(w http.ResponseWriter, r *http.Request) {
	args := []any{}
	where := []string{"status='active'"}
	for _, filter := range []struct{ key, column string }{{"campus_id", "campus_id"}, {"type", "type"}} {
		if value := strings.TrimSpace(r.URL.Query().Get(filter.key)); value != "" {
			args = append(args, value)
			where = append(where, filter.column+"=$"+strconv.Itoa(len(args)))
		}
	}
	for _, filter := range []struct{ key, op string }{{"min_lat", "latitude>="}, {"max_lat", "latitude<="}, {"min_lon", "longitude>="}, {"max_lon", "longitude<="}} {
		if raw := r.URL.Query().Get(filter.key); raw != "" {
			if value, e := strconv.ParseFloat(raw, 64); e == nil {
				args = append(args, value)
				where = append(where, filter.op+"$"+strconv.Itoa(len(args)))
			}
		}
	}
	rows, err := s.pool.Query(r.Context(), `SELECT id,type,category,name,aliases,latitude,longitude,coordinate_system,campus_id,address,building_id,parent_poi_id,floor,description,verified_level,sort_order,updated_at FROM pois WHERE `+strings.Join(where, " AND ")+` ORDER BY sort_order DESC,verified_level DESC,name LIMIT 1000`, args...)
	if err != nil {
		Fail(w, 500, "查询 POI 失败")
		return
	}
	values, _ := queryMaps(rows)
	OK(w, values)
}

func (s *Server) handlePOIManifest(w http.ResponseWriter, r *http.Request) {
	var revision int64
	var updated time.Time
	if e := s.pool.QueryRow(r.Context(), `SELECT revision,updated_at FROM poi_metadata WHERE singleton=TRUE`).Scan(&revision, &updated); e != nil {
		Fail(w, 500, "读取 POI 版本失败")
		return
	}
	OK(w, map[string]any{"revision": revision, "updated_at": updated})
}

func (s *Server) handlePOIContribution(w http.ResponseWriter, r *http.Request) {
	var q map[string]any
	if !decodeJSON(w, r, &q) {
		return
	}
	if strings.TrimSpace(anyString(q["name"])) == "" {
		Fail(w, 400, "POI 名称必填")
		return
	}
	var id int64
	e := s.pool.QueryRow(r.Context(), `INSERT INTO poi_contributions(user_id,proposed_data) VALUES($1,$2) RETURNING id`, userIDFrom(r), q).Scan(&id)
	if e != nil {
		Fail(w, 500, "提交失败")
		return
	}
	OK(w, map[string]any{"id": id, "status": "pending"})
}
func anyString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

type rentalInput struct {
	ID            int64          `json:"id"`
	ListingType   string         `json:"listing_type"`
	Title         string         `json:"title"`
	Description   string         `json:"description"`
	CampusID      string         `json:"campus_id"`
	POIID         *int64         `json:"poi_id"`
	Address       string         `json:"address"`
	Latitude      *float64       `json:"latitude"`
	Longitude     *float64       `json:"longitude"`
	RentCents     *int           `json:"rent_cents"`
	AreaSQM       *float64       `json:"area_sqm"`
	PaymentTerms  string         `json:"payment_terms"`
	AvailableFrom *string        `json:"available_from"`
	ContactName   string         `json:"contact_name"`
	ContactValue  string         `json:"contact_value"`
	ImageURLs     []string       `json:"image_urls"`
	Attributes    map[string]any `json:"attributes"`
}

func validRentalType(value string) bool {
	return map[string]bool{"student_sublet": true, "student_wanted": true}[value]
}

func (s *Server) handleAppRentals(w http.ResponseWriter, r *http.Request) {
	args := []any{}
	where := []string{"l.status='published'", "(l.expires_at IS NULL OR l.expires_at>NOW())"}
	for _, filter := range []struct{ key, column string }{{"campus_id", "l.campus_id"}, {"type", "l.listing_type"}} {
		if value := strings.TrimSpace(r.URL.Query().Get(filter.key)); value != "" {
			args = append(args, value)
			where = append(where, filter.column+"=$"+strconv.Itoa(len(args)))
		}
	}
	rows, err := s.pool.Query(r.Context(), `SELECT l.id,l.listing_type,l.title,l.description,l.campus_id,l.poi_id,CASE WHEN l.listing_type='commercial_apartment' THEN l.address ELSE '具体位置联系后可见' END address,CASE WHEN l.listing_type='commercial_apartment' THEN round(l.latitude::numeric,4) ELSE round(l.latitude::numeric,2) END latitude,CASE WHEN l.listing_type='commercial_apartment' THEN round(l.longitude::numeric,4) ELSE round(l.longitude::numeric,2) END longitude,l.rent_cents,l.area_sqm,l.payment_terms,l.available_from,l.image_urls,l.attributes,l.published_at,l.expires_at,l.view_count,l.contact_count,l.favorite_count FROM rental_listings l WHERE `+strings.Join(where, " AND ")+` ORDER BY l.published_at DESC NULLS LAST,l.id DESC LIMIT 100`, args...)
	if err != nil {
		Fail(w, 500, "查询房源失败")
		return
	}
	values, _ := queryMaps(rows)
	OK(w, values)
}

func (s *Server) handleAppRentalDetail(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var value map[string]any
	rows, err := s.pool.Query(r.Context(), `UPDATE rental_listings SET view_count=view_count+1 WHERE id=$1 AND status='published' AND (expires_at IS NULL OR expires_at>NOW()) RETURNING id,listing_type,title,description,campus_id,poi_id,CASE WHEN listing_type='commercial_apartment' THEN address ELSE '具体位置联系后可见' END address,CASE WHEN listing_type='commercial_apartment' THEN round(latitude::numeric,4) ELSE round(latitude::numeric,2) END latitude,CASE WHEN listing_type='commercial_apartment' THEN round(longitude::numeric,4) ELSE round(longitude::numeric,2) END longitude,rent_cents,area_sqm,payment_terms,available_from,image_urls,attributes,published_at,expires_at,view_count,contact_count,favorite_count`, id)
	if err == nil {
		items, _ := queryMaps(rows)
		if len(items) > 0 {
			value = items[0]
		}
	}
	if value == nil {
		Fail(w, 404, "房源不存在或已下架")
		return
	}
	OK(w, value)
}

func (s *Server) handleMyRentals(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	if r.Method == http.MethodGet {
		rows, e := s.pool.Query(r.Context(), `SELECT * FROM rental_listings WHERE owner_user_id=$1 ORDER BY id DESC`, uid)
		if e != nil {
			Fail(w, 500, "查询失败")
			return
		}
		v, _ := queryMaps(rows)
		OK(w, v)
		return
	}
	if r.Method == http.MethodDelete {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		tag, e := s.pool.Exec(r.Context(), `UPDATE rental_listings SET status='off_shelf',off_shelf_at=NOW(),updated_at=NOW() WHERE id=$1 AND owner_user_id=$2 AND status NOT IN ('expired','removed')`, id, uid)
		if e != nil || tag.RowsAffected() == 0 {
			Fail(w, 404, "房源不存在")
			return
		}
		OK(w, map[string]any{"id": id, "status": "off_shelf"})
		return
	}
	var q rentalInput
	if !decodeJSON(w, r, &q) {
		return
	}
	if q.ID == 0 && r.Method == http.MethodPut {
		q.ID, _ = strconv.ParseInt(r.PathValue("id"), 10, 64)
	}
	if !validRentalType(q.ListingType) || strings.TrimSpace(q.Title) == "" || q.CampusID == "" || q.ContactValue == "" {
		Fail(w, 400, "房源类型、标题、校区和联系方式必填")
		return
	}
	var id int64
	if q.ID > 0 {
		e := s.pool.QueryRow(r.Context(), `UPDATE rental_listings SET listing_type=$1,title=$2,description=$3,campus_id=$4,poi_id=$5,address=$6,latitude=$7,longitude=$8,rent_cents=$9,area_sqm=$10,payment_terms=$11,available_from=NULLIF($12,'')::date,contact_name=$13,contact_value=$14,image_urls=$15,attributes=$16,status='pending',review_note='',updated_at=NOW() WHERE id=$17 AND owner_user_id=$18 RETURNING id`, q.ListingType, q.Title, q.Description, q.CampusID, q.POIID, q.Address, q.Latitude, q.Longitude, q.RentCents, q.AreaSQM, q.PaymentTerms, stringValue(q.AvailableFrom), q.ContactName, q.ContactValue, q.ImageURLs, q.Attributes, q.ID, uid).Scan(&id)
		if e != nil {
			Fail(w, 404, "房源不存在")
			return
		}
	} else {
		e := s.pool.QueryRow(r.Context(), `INSERT INTO rental_listings(owner_user_id,listing_type,title,description,campus_id,poi_id,address,latitude,longitude,rent_cents,area_sqm,payment_terms,available_from,contact_name,contact_value,image_urls,attributes) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,'')::date,$14,$15,$16,$17) RETURNING id`, uid, q.ListingType, q.Title, q.Description, q.CampusID, q.POIID, q.Address, q.Latitude, q.Longitude, q.RentCents, q.AreaSQM, q.PaymentTerms, stringValue(q.AvailableFrom), q.ContactName, q.ContactValue, q.ImageURLs, q.Attributes).Scan(&id)
		if e != nil {
			Fail(w, 500, "发布失败")
			return
		}
	}
	OK(w, map[string]any{"id": id, "status": "pending"})
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Server) handleRentalFavorite(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	favorite := r.Method != http.MethodDelete
	if favorite {
		tag, e := s.pool.Exec(r.Context(), `INSERT INTO rental_favorites(user_id,listing_id) SELECT $1,id FROM rental_listings WHERE id=$2 AND status='published' ON CONFLICT DO NOTHING`, uid, id)
		if e != nil || tag.RowsAffected() == 0 {
			Fail(w, 404, "房源不存在或已经收藏")
			return
		}
	} else {
		s.pool.Exec(r.Context(), `DELETE FROM rental_favorites WHERE user_id=$1 AND listing_id=$2`, uid, id)
	}
	s.pool.Exec(r.Context(), `UPDATE rental_listings SET favorite_count=(SELECT count(*) FROM rental_favorites WHERE listing_id=$1) WHERE id=$1`, id)
	OK(w, map[string]bool{"favorite": favorite})
}
func (s *Server) handleRentalReport(w http.ResponseWriter, r *http.Request) {
	var q struct{ Reason, Detail string }
	if !decodeJSON(w, r, &q) {
		return
	}
	uid := userIDFrom(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var rid int64
	e := s.pool.QueryRow(r.Context(), `INSERT INTO rental_reports(listing_id,reporter_user_id,reason,detail) SELECT id,$1,$2,$3 FROM rental_listings WHERE id=$4 AND status='published' RETURNING id`, uid, q.Reason, q.Detail, id).Scan(&rid)
	if e != nil {
		Fail(w, 404, "房源不存在")
		return
	}
	OK(w, map[string]any{"id": rid})
}
func (s *Server) handleRentalContact(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var contact string
	e := s.pool.QueryRow(r.Context(), `UPDATE rental_listings SET contact_count=contact_count+1 WHERE id=$1 AND status='published' RETURNING contact_value`, id).Scan(&contact)
	if e != nil {
		Fail(w, 404, "房源不存在")
		return
	}
	s.pool.Exec(r.Context(), `INSERT INTO rental_contacts(listing_id,user_id) VALUES($1,$2)`, id, uid)
	OK(w, map[string]string{"contact": contact})
}
func (s *Server) handleRentalRenew(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	tag, e := s.pool.Exec(r.Context(), `UPDATE rental_listings SET status='pending',submitted_at=NOW(),review_note='',updated_at=NOW() WHERE id=$1 AND owner_user_id=$2 AND status IN ('published','expired','off_shelf','rejected')`, id, uid)
	if e != nil || tag.RowsAffected() == 0 {
		Fail(w, 404, "房源不可续期")
		return
	}
	OK(w, map[string]any{"id": id, "status": "pending"})
}

func (s *Server) handleMerchantRentals(w http.ResponseWriter, r *http.Request) {
	p := merchantPrincipal(r)
	if r.Method == http.MethodGet {
		rows, err := s.pool.Query(r.Context(), `SELECT * FROM rental_listings WHERE merchant_id=$1 ORDER BY id DESC`, p.MerchantID)
		if err != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		values, _ := queryMaps(rows)
		v1Data(w, r, 200, values)
		return
	}
	if r.Method == http.MethodDelete {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		tag, err := s.pool.Exec(r.Context(), `UPDATE rental_listings SET status='off_shelf',off_shelf_at=NOW(),updated_at=NOW() WHERE id=$1 AND merchant_id=$2 AND status NOT IN ('expired','removed')`, id, p.MerchantID)
		if err != nil || tag.RowsAffected() == 0 {
			v1Error(w, r, 404, "NOT_FOUND", "房源不存在")
			return
		}
		merchantWriteOK(s, w, r, p, "rental.off_shelf", "rental", id)
		return
	}
	var q rentalInput
	if !decodeJSON(w, r, &q) {
		return
	}
	expected := "individual_landlord"
	if p.MerchantType == "commercial_apartment" {
		expected = "commercial_apartment"
	}
	if q.ListingType != "" && q.ListingType != expected {
		v1Error(w, r, 403, "MERCHANT_TYPE_FORBIDDEN", "不能发布其他类型房源")
		return
	}
	if strings.TrimSpace(q.Title) == "" || q.CampusID == "" || q.ContactValue == "" {
		v1Error(w, r, 400, "VALIDATION_ERROR", "标题、校区和联系方式必填")
		return
	}
	var id int64
	if q.ID > 0 {
		err := s.pool.QueryRow(r.Context(), `UPDATE rental_listings SET title=$1,description=$2,campus_id=$3,poi_id=$4,address=$5,latitude=$6,longitude=$7,rent_cents=$8,area_sqm=$9,payment_terms=$10,available_from=NULLIF($11,'')::date,contact_name=$12,contact_value=$13,image_urls=$14,attributes=$15,status='pending',submitted_at=NOW(),review_note='',updated_at=NOW() WHERE id=$16 AND merchant_id=$17 AND listing_type=$18 RETURNING id`, q.Title, q.Description, q.CampusID, q.POIID, q.Address, q.Latitude, q.Longitude, q.RentCents, q.AreaSQM, q.PaymentTerms, stringValue(q.AvailableFrom), q.ContactName, q.ContactValue, q.ImageURLs, q.Attributes, q.ID, p.MerchantID, expected).Scan(&id)
		if err != nil {
			v1Error(w, r, 404, "NOT_FOUND", "房源不存在")
			return
		}
	} else {
		err := s.pool.QueryRow(r.Context(), `INSERT INTO rental_listings(merchant_id,listing_type,title,description,campus_id,poi_id,address,latitude,longitude,rent_cents,area_sqm,payment_terms,available_from,contact_name,contact_value,image_urls,attributes,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,'')::date,$14,$15,$16,$17,'pending') RETURNING id`, p.MerchantID, expected, q.Title, q.Description, q.CampusID, q.POIID, q.Address, q.Latitude, q.Longitude, q.RentCents, q.AreaSQM, q.PaymentTerms, stringValue(q.AvailableFrom), q.ContactName, q.ContactValue, q.ImageURLs, q.Attributes).Scan(&id)
		if err != nil {
			v1Error(w, r, 500, "SAVE_FAILED", "保存失败")
			return
		}
	}
	merchantWriteOK(s, w, r, p, "rental.save", "rental", id)
}

func (s *Server) handleMerchantRentalRenew(w http.ResponseWriter, r *http.Request) {
	p := merchantPrincipal(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	tag, err := s.pool.Exec(r.Context(), `UPDATE rental_listings SET status='pending',submitted_at=NOW(),review_note='',updated_at=NOW() WHERE id=$1 AND merchant_id=$2 AND status IN ('published','expired','off_shelf','rejected')`, id, p.MerchantID)
	if err != nil || tag.RowsAffected() == 0 {
		v1Error(w, r, 404, "NOT_FOUND", "房源不可续期")
		return
	}
	merchantWriteOK(s, w, r, p, "rental.renew", "rental", id)
}

func (s *Server) handleAdminPOIs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, e := s.pool.Query(r.Context(), `SELECT * FROM pois ORDER BY id DESC LIMIT 500`)
		if e != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		v, _ := queryMaps(rows)
		v1Data(w, r, 200, v)
		return
	}
	var q struct {
		ID               int64   `json:"id"`
		Type             string  `json:"type"`
		Category         string  `json:"category"`
		Name             string  `json:"name"`
		CampusID         *string `json:"campus_id"`
		Address          string  `json:"address"`
		Floor            string  `json:"floor"`
		Description      string  `json:"description"`
		Status           string  `json:"status"`
		CoordinateSystem string  `json:"coordinate_system"`
		Latitude         float64 `json:"latitude"`
		Longitude        float64 `json:"longitude"`
		BuildingID       *int64  `json:"building_id"`
		VerifiedLevel    int     `json:"verified_level"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	if q.Name == "" || q.Type == "" || !map[string]bool{"CGCS2000": true, "WGS84": true, "GCJ02": true}[q.CoordinateSystem] {
		v1Error(w, r, 400, "VALIDATION_ERROR", "名称、类型和有效坐标系必填")
		return
	}
	var id int64
	if q.ID > 0 {
		e := s.pool.QueryRow(r.Context(), `UPDATE pois SET type=$1,category=$2,name=$3,latitude=$4,longitude=$5,campus_id=$6,address=$7,building_id=$8,floor=$9,description=$10,status=$11,coordinate_system=$12,verified_level=$13,verified_at=CASE WHEN $13>0 THEN NOW() ELSE NULL END,updated_at=NOW() WHERE id=$14 RETURNING id`, q.Type, q.Category, q.Name, q.Latitude, q.Longitude, q.CampusID, q.Address, q.BuildingID, q.Floor, q.Description, q.Status, q.CoordinateSystem, q.VerifiedLevel, q.ID).Scan(&id)
		if e != nil {
			v1Error(w, r, 404, "NOT_FOUND", "POI 不存在")
			return
		}
	} else {
		e := s.pool.QueryRow(r.Context(), `INSERT INTO pois(type,category,name,latitude,longitude,campus_id,address,building_id,floor,description,status,coordinate_system,verified_level,verified_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,CASE WHEN $13>0 THEN NOW() END) RETURNING id`, q.Type, q.Category, q.Name, q.Latitude, q.Longitude, q.CampusID, q.Address, q.BuildingID, q.Floor, q.Description, q.Status, q.CoordinateSystem, q.VerifiedLevel).Scan(&id)
		if e != nil {
			v1Error(w, r, 400, "SAVE_FAILED", "保存失败")
			return
		}
	}
	v1Data(w, r, 200, map[string]any{"id": id})
}

func (s *Server) handleAdminPOIContributions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, e := s.pool.Query(r.Context(), `SELECT c.*,u.nickname user_name FROM poi_contributions c LEFT JOIN zonenan_users u ON u.id=c.user_id ORDER BY c.id DESC`)
		if e != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		v, _ := queryMaps(rows)
		v1Data(w, r, 200, v)
		return
	}
	var q struct {
		ID       int64  `json:"id"`
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	if q.Decision != "approved" && q.Decision != "rejected" {
		v1Error(w, r, 400, "VALIDATION_ERROR", "审核决定无效")
		return
	}
	p := adminPrincipalFrom(r)
	tx, e := s.pool.Begin(r.Context())
	if e != nil {
		v1Error(w, r, 500, "SAVE_FAILED", "审核失败")
		return
	}
	defer tx.Rollback(r.Context())
	var data map[string]any
	e = tx.QueryRow(r.Context(), `UPDATE poi_contributions SET status=$1,review_note=$2,reviewed_by=$3,reviewed_at=NOW() WHERE id=$4 AND status='pending' RETURNING proposed_data`, q.Decision, q.Note, p.ID, q.ID).Scan(&data)
	if e != nil {
		v1Error(w, r, 404, "NOT_FOUND", "待审核贡献不存在")
		return
	}
	if q.Decision == "approved" {
		lat, _ := data["latitude"].(float64)
		lon, _ := data["longitude"].(float64)
		_, e = tx.Exec(r.Context(), `INSERT INTO pois(type,category,name,latitude,longitude,campus_id,address,description,coordinate_system,status,source,source_user_id,verified_level,verified_at) SELECT COALESCE($1,'other'),COALESCE($2,'other'),$3,$4,$5,NULLIF($6,''),COALESCE($7,''),COALESCE($8,''),COALESCE($9,'CGCS2000'),'active','user',user_id,1,NOW() FROM poi_contributions WHERE id=$10`, anyString(data["type"]), anyString(data["category"]), anyString(data["name"]), lat, lon, anyString(data["campus_id"]), anyString(data["address"]), anyString(data["description"]), anyString(data["coordinate_system"]), q.ID)
	}
	if e != nil || tx.Commit(r.Context()) != nil {
		v1Error(w, r, 500, "SAVE_FAILED", "审核失败")
		return
	}
	v1Data(w, r, 200, map[string]any{"id": q.ID, "status": q.Decision})
}

func (s *Server) handleAdminRentals(w http.ResponseWriter, r *http.Request) {
	rows, e := s.pool.Query(r.Context(), `SELECT l.*,u.nickname owner_name,m.name merchant_name,(SELECT count(*) FROM rental_reports rr WHERE rr.listing_id=l.id AND rr.status='pending') pending_reports FROM rental_listings l LEFT JOIN zonenan_users u ON u.id=l.owner_user_id LEFT JOIN merchants m ON m.id=l.merchant_id ORDER BY l.id DESC LIMIT 500`)
	if e != nil {
		v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
		return
	}
	v, _ := queryMaps(rows)
	v1Data(w, r, 200, v)
}
func (s *Server) handleAdminRentalReview(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID             int64
		Decision, Note string
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	status := map[string]string{"approved": "published", "rejected": "rejected", "off_shelf": "off_shelf", "removed": "removed"}[q.Decision]
	if status == "" {
		v1Error(w, r, 400, "VALIDATION_ERROR", "决定无效")
		return
	}
	p := adminPrincipalFrom(r)
	tag, e := s.pool.Exec(r.Context(), `UPDATE rental_listings SET status=$1,review_note=$2,reviewed_by=$3,reviewed_at=NOW(),published_at=CASE WHEN $1='published' THEN COALESCE(published_at,NOW()) ELSE published_at END,expires_at=CASE WHEN $1='published' THEN NOW()+INTERVAL '30 days' ELSE expires_at END,off_shelf_at=CASE WHEN $1='off_shelf' THEN NOW() ELSE off_shelf_at END,removed_at=CASE WHEN $1='removed' THEN NOW() ELSE removed_at END,removal_reason=CASE WHEN $1='removed' THEN $2 ELSE removal_reason END,updated_at=NOW() WHERE id=$4`, status, q.Note, p.ID, q.ID)
	if e != nil || tag.RowsAffected() == 0 {
		v1Error(w, r, 404, "NOT_FOUND", "房源不存在")
		return
	}
	v1Data(w, r, 200, map[string]any{"id": q.ID, "status": status})
}
func (s *Server) handleAdminRentalReports(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, e := s.pool.Query(r.Context(), `SELECT rr.*,l.title listing_title,u.nickname reporter_name FROM rental_reports rr JOIN rental_listings l ON l.id=rr.listing_id LEFT JOIN zonenan_users u ON u.id=rr.reporter_user_id ORDER BY rr.id DESC`)
		if e != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		v, _ := queryMaps(rows)
		v1Data(w, r, 200, v)
		return
	}
	var q struct {
		ID       int64
		Decision string
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	status := map[string]string{"processed": "processed", "rejected": "rejected"}[q.Decision]
	if status == "" {
		v1Error(w, r, 400, "VALIDATION_ERROR", "处理决定无效")
		return
	}
	p := adminPrincipalFrom(r)
	tag, e := s.pool.Exec(r.Context(), `UPDATE rental_reports SET status=$1,handled_by=$2,handled_at=NOW() WHERE id=$3 AND status='pending'`, status, p.ID, q.ID)
	if e != nil || tag.RowsAffected() == 0 {
		v1Error(w, r, 404, "NOT_FOUND", "待处理举报不存在")
		return
	}
	v1Data(w, r, 200, map[string]any{"id": q.ID, "status": status})
}
func (s *Server) handleAdminRentalStats(w http.ResponseWriter, r *http.Request) {
	var v map[string]any
	rows, e := s.pool.Query(r.Context(), `SELECT status,count(*) value FROM rental_listings GROUP BY status`)
	if e != nil {
		v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
		return
	}
	items, _ := queryMaps(rows)
	v = map[string]any{"by_status": items}
	var totalViews, totalContacts, totalFavorites int64
	s.pool.QueryRow(r.Context(), `SELECT COALESCE(sum(view_count),0),COALESCE(sum(contact_count),0),COALESCE(sum(favorite_count),0) FROM rental_listings`).Scan(&totalViews, &totalContacts, &totalFavorites)
	v["views"] = totalViews
	v["contacts"] = totalContacts
	v["favorites"] = totalFavorites
	v1Data(w, r, 200, v)
}

func (s *Server) handleAdminJobs(w http.ResponseWriter, r *http.Request) {
	rows, e := s.pool.Query(r.Context(), `SELECT id,job_name,run_key,scheduled_at,started_at,finished_at,status,processed_count,retry_count,error_summary FROM job_runs ORDER BY id DESC LIMIT 200`)
	if e != nil {
		v1Error(w, r, 500, "QUERY_FAILED", "查询任务失败")
		return
	}
	v, _ := queryMaps(rows)
	v1Data(w, r, 200, v)
}

func ExpireRentals(ctx interface{ Done() <-chan struct{} }, exec func() error) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = exec()
		}
	}
}
