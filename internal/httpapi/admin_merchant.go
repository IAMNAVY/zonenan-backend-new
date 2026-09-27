package httpapi

import "net/http"

func (s *Server) handleAdminMerchants(w http.ResponseWriter, r *http.Request) {
	rows, e := s.pool.Query(r.Context(), `SELECT m.id,m.name,m.merchant_type,m.status,m.contact_name,m.contact_phone,m.review_note,m.created_at,m.updated_at,u.id account_id,u.email,u.phone,u.display_name,u.status account_status,u.registration_source,st.id store_id,st.name store_name,st.status store_status FROM merchants m LEFT JOIN merchant_users u ON u.merchant_id=m.id LEFT JOIN merchant_stores st ON st.merchant_id=m.id ORDER BY m.id DESC`)
	if e != nil {
		v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
		return
	}
	v, _ := queryMaps(rows)
	v1Data(w, r, 200, map[string]any{"items": v, "self_registration_enabled": s.merchantAccounts.SelfRegistrationEnabled(r.Context())})
}
func (s *Server) handleAdminCreateMerchant(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Email        string `json:"email"`
		Phone        string `json:"phone"`
		Password     string `json:"password"`
		DisplayName  string `json:"display_name"`
		MerchantName string `json:"merchant_name"`
		MerchantType string `json:"merchant_type"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	id, e := s.merchantAccounts.AdminCreate(r.Context(), q.Email, q.Phone, q.Password, q.DisplayName, q.MerchantName, q.MerchantType)
	if e != nil {
		v1Error(w, r, 400, "CREATE_FAILED", e.Error())
		return
	}
	v1Data(w, r, 201, map[string]any{"account_id": id, "created": true})
}
func (s *Server) handleAdminReviewMerchant(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID             int64
		Decision, Note string
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	if q.Decision != "approved" && q.Decision != "rejected" && q.Decision != "suspended" {
		v1Error(w, r, 400, "VALIDATION_ERROR", "审核决定无效")
		return
	}
	tx, e := s.pool.Begin(r.Context())
	if e != nil {
		v1Error(w, r, 500, "SAVE_FAILED", "操作失败")
		return
	}
	defer tx.Rollback(r.Context())
	p := adminPrincipalFrom(r)
	tag, e := tx.Exec(r.Context(), `UPDATE merchants SET status=$1,review_note=$2,reviewed_by=$3,reviewed_at=NOW(),updated_at=NOW() WHERE id=$4`, q.Decision, q.Note, p.ID, q.ID)
	if e != nil || tag.RowsAffected() == 0 {
		v1Error(w, r, 404, "NOT_FOUND", "商户不存在")
		return
	}
	accountStatus := "rejected"
	if q.Decision == "approved" {
		accountStatus = "active"
		_, e = tx.Exec(r.Context(), `INSERT INTO merchant_stores(merchant_id,name,status) SELECT id,name,'draft' FROM merchants WHERE id=$1 AND merchant_type<>'commercial_apartment' ON CONFLICT(merchant_id) DO NOTHING`, q.ID)
	} else if q.Decision == "suspended" {
		accountStatus = "disabled"
	}
	if e == nil {
		_, e = tx.Exec(r.Context(), `UPDATE merchant_users SET status=$1,updated_at=NOW() WHERE merchant_id=$2`, accountStatus, q.ID)
	}
	if e != nil || tx.Commit(r.Context()) != nil {
		v1Error(w, r, 500, "SAVE_FAILED", "审核失败")
		return
	}
	v1Data(w, r, 200, map[string]any{"id": q.ID, "status": q.Decision})
}
func (s *Server) handleAdminMerchantSettings(w http.ResponseWriter, r *http.Request) {
	var q struct {
		SelfRegistrationEnabled bool `json:"self_registration_enabled"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	value := "false"
	if q.SelfRegistrationEnabled {
		value = "true"
	}
	if e := s.setting.Set(r.Context(), "merchant_self_registration_enabled", value); e != nil {
		v1Error(w, r, 500, "SAVE_FAILED", "保存失败")
		return
	}
	v1Data(w, r, 200, map[string]bool{"self_registration_enabled": q.SelfRegistrationEnabled})
}
func (s *Server) handleAdminMerchantClaims(w http.ResponseWriter, r *http.Request) {
	rows, e := s.pool.Query(r.Context(), `SELECT c.*,u.email,u.phone,u.display_name,m.name merchant_name FROM merchant_claims c JOIN merchant_users u ON u.id=c.merchant_user_id LEFT JOIN merchants m ON m.id=c.merchant_id ORDER BY c.id DESC`)
	if e != nil {
		v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
		return
	}
	v, _ := queryMaps(rows)
	v1Data(w, r, 200, v)
}
func (s *Server) handleAdminReviewMerchantClaim(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID             int64
		Decision, Note string
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
	var uid, mid int64
	e = tx.QueryRow(r.Context(), `UPDATE merchant_claims SET status=$1,review_note=$2,reviewed_by=$3,reviewed_at=NOW() WHERE id=$4 AND status='pending' RETURNING merchant_user_id,merchant_id`, q.Decision, q.Note, p.ID, q.ID).Scan(&uid, &mid)
	if e != nil {
		v1Error(w, r, 404, "NOT_FOUND", "待审认领不存在")
		return
	}
	if q.Decision == "approved" {
		_, e = tx.Exec(r.Context(), `UPDATE merchant_users SET merchant_id=$1,status='active' WHERE id=$2`, mid, uid)
	}
	if e != nil || tx.Commit(r.Context()) != nil {
		v1Error(w, r, 500, "SAVE_FAILED", "审核失败")
		return
	}
	v1Data(w, r, 200, map[string]any{"id": q.ID, "status": q.Decision})
}
func (s *Server) handleAdminMerchantPromotions(w http.ResponseWriter, r *http.Request) {
	rows, e := s.pool.Query(r.Context(), `SELECT p.*,m.name merchant_name FROM merchant_promotions p JOIN merchants m ON m.id=p.merchant_id ORDER BY p.id DESC`)
	if e != nil {
		v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
		return
	}
	v, _ := queryMaps(rows)
	v1Data(w, r, 200, v)
}
func (s *Server) handleAdminReviewMerchantPromotion(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID             int64
		Decision, Note string
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	if q.Decision != "approved" && q.Decision != "rejected" {
		v1Error(w, r, 400, "VALIDATION_ERROR", "审核决定无效")
		return
	}
	p := adminPrincipalFrom(r)
	tag, e := s.pool.Exec(r.Context(), `UPDATE merchant_promotions SET status=$1,review_note=$2,reviewed_by=$3,reviewed_at=NOW(),updated_at=NOW() WHERE id=$4 AND status='pending'`, q.Decision, q.Note, p.ID, q.ID)
	if e != nil || tag.RowsAffected() == 0 {
		v1Error(w, r, 404, "NOT_FOUND", "待审活动不存在")
		return
	}
	v1Data(w, r, 200, map[string]any{"id": q.ID, "status": q.Decision})
}

func (s *Server) handleAdminMerchantStores(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT st.*,m.name merchant_name,m.merchant_type FROM merchant_stores st JOIN merchants m ON m.id=st.merchant_id ORDER BY st.id DESC`)
	if err != nil {
		v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
		return
	}
	values, _ := queryMaps(rows)
	v1Data(w, r, 200, values)
}

func (s *Server) handleAdminReviewMerchantStore(w http.ResponseWriter, r *http.Request) {
	s.handleAdminReviewMerchantResource(w, r, "merchant_stores", "门店")
}

func (s *Server) handleAdminMerchantApartments(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT a.*,m.name merchant_name,(SELECT COUNT(*) FROM merchant_apartment_rooms room WHERE room.apartment_id=a.id) room_count FROM merchant_apartments a JOIN merchants m ON m.id=a.merchant_id ORDER BY a.id DESC`)
	if err != nil {
		v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
		return
	}
	values, _ := queryMaps(rows)
	v1Data(w, r, 200, values)
}

func (s *Server) handleAdminReviewMerchantApartment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID       int64  `json:"id"`
		Decision string `json:"decision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	status := map[string]string{"approved": "published", "rejected": "rejected", "suspended": "suspended"}[request.Decision]
	if status == "" {
		v1Error(w, r, 400, "VALIDATION_ERROR", "审核决定无效")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		v1Error(w, r, 500, "SAVE_FAILED", "审核失败")
		return
	}
	defer tx.Rollback(r.Context())
	var merchantID int64
	err = tx.QueryRow(r.Context(), `UPDATE merchant_apartments SET status=$1,updated_at=NOW() WHERE id=$2 RETURNING merchant_id`, status, request.ID).Scan(&merchantID)
	if err != nil {
		v1Error(w, r, 404, "NOT_FOUND", "商业公寓不存在")
		return
	}
	if status == "published" {
		_, err = tx.Exec(r.Context(), `INSERT INTO rental_listings(merchant_id,listing_type,title,description,campus_id,poi_id,address,latitude,longitude,contact_name,contact_value,status,published_at,expires_at) SELECT a.merchant_id,'commercial_apartment',a.name,a.description,COALESCE(p.campus_id,'main'),a.unified_poi_id,a.address,a.latitude,a.longitude,m.contact_name,COALESCE(NULLIF(a.contact_phone,''),m.contact_phone),'published',NOW(),NOW()+INTERVAL '90 days' FROM merchant_apartments a JOIN merchants m ON m.id=a.merchant_id LEFT JOIN pois p ON p.id=a.unified_poi_id WHERE a.id=$1 AND NOT EXISTS(SELECT 1 FROM rental_listings l WHERE l.merchant_id=a.merchant_id AND l.listing_type='commercial_apartment')`, request.ID)
	} else {
		rentalStatus := map[string]string{"rejected": "rejected", "suspended": "removed"}[status]
		_, err = tx.Exec(r.Context(), `UPDATE rental_listings SET status=$1,removed_at=CASE WHEN $1='removed' THEN NOW() ELSE removed_at END,updated_at=NOW() WHERE merchant_id=$2 AND listing_type='commercial_apartment'`, rentalStatus, merchantID)
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		v1Error(w, r, 500, "SAVE_FAILED", "审核失败")
		return
	}
	v1Data(w, r, 200, map[string]any{"id": request.ID, "status": status})
}

func (s *Server) handleAdminReviewMerchantResource(w http.ResponseWriter, r *http.Request, table, displayName string) {
	var request struct {
		ID       int64  `json:"id"`
		Decision string `json:"decision"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	status := map[string]string{"approved": "published", "rejected": "rejected", "suspended": "suspended"}[request.Decision]
	if status == "" {
		v1Error(w, r, 400, "VALIDATION_ERROR", "审核决定无效")
		return
	}
	query := `UPDATE ` + table + ` SET status=$1,updated_at=NOW() WHERE id=$2`
	tag, err := s.pool.Exec(r.Context(), query, status, request.ID)
	if err != nil || tag.RowsAffected() == 0 {
		v1Error(w, r, 404, "NOT_FOUND", displayName+"不存在")
		return
	}
	v1Data(w, r, 200, map[string]any{"id": request.ID, "status": status})
}
