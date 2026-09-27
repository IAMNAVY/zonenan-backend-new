package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"zonenan-backend/internal/merchantauth"
)

const merchantSessionCookie = "zonenan_merchant_session"
const merchantCSRFCookie = "zonenan_merchant_csrf"

type merchantContextKey struct{}

func merchantPrincipal(r *http.Request) *merchantauth.Principal {
	p, _ := r.Context().Value(merchantContextKey{}).(*merchantauth.Principal)
	return p
}

func (s *Server) mountMerchantV1(r chi.Router) {
	r.Handle("/merchant-media/*", http.StripPrefix("/merchant-media/", http.FileServer(http.Dir(s.cfg.MerchantUploadDir))))
	r.Route("/merchant-api/v1", func(r chi.Router) {
		r.Get("/registration/status", s.handleMerchantRegistrationStatus)
		r.Post("/auth/register", s.handleMerchantRegister)
		r.Post("/auth/login", s.handleMerchantLogin)
		r.Group(func(r chi.Router) {
			r.Use(s.merchantAuth)
			r.Get("/auth/me", s.handleMerchantMe)
			r.Post("/auth/logout", s.handleMerchantLogout)
			r.Post("/account/password", s.handleMerchantPassword)
			r.Get("/claims", s.handleMerchantClaims)
			r.Post("/claims", s.handleMerchantClaims)
			r.Post("/media", s.handleMerchantMedia)
			r.With(requireMerchantTypes("restaurant", "beverage", "printing", "life_service", "partner", "other")).Get("/store", s.handleMerchantStore)
			r.With(requireMerchantTypes("restaurant", "beverage", "printing", "life_service", "partner", "other")).Put("/store", s.handleMerchantStore)
			r.With(requireMerchantTypes("restaurant", "beverage")).Get("/menu/categories", s.handleMerchantCategories)
			r.With(requireMerchantTypes("restaurant", "beverage")).Post("/menu/categories", s.handleMerchantCategories)
			r.With(requireMerchantTypes("restaurant", "beverage")).Delete("/menu/categories/{id}", s.handleMerchantCategories)
			r.With(requireMerchantTypes("restaurant", "beverage")).Get("/menu/items", s.handleMerchantItems)
			r.With(requireMerchantTypes("restaurant", "beverage")).Post("/menu/items", s.handleMerchantItems)
			r.With(requireMerchantTypes("restaurant", "beverage")).Delete("/menu/items/{id}", s.handleMerchantItems)
			r.With(requireMerchantTypes("restaurant", "beverage", "printing", "life_service", "partner", "other")).Get("/promotions", s.handleMerchantPromotions)
			r.With(requireMerchantTypes("restaurant", "beverage", "printing", "life_service", "partner", "other")).Post("/promotions", s.handleMerchantPromotions)
			r.With(requireMerchantTypes("restaurant", "beverage", "printing", "life_service", "partner", "other")).Delete("/promotions/{id}", s.handleMerchantPromotions)
			r.Get("/analytics", s.handleMerchantAnalytics)
			r.With(requireMerchantTypes("individual_landlord", "commercial_apartment")).Get("/rentals", s.handleMerchantRentals)
			r.With(requireMerchantTypes("individual_landlord", "commercial_apartment")).Post("/rentals", s.handleMerchantRentals)
			r.With(requireMerchantTypes("individual_landlord", "commercial_apartment")).Delete("/rentals/{id}", s.handleMerchantRentals)
			r.With(requireMerchantTypes("individual_landlord", "commercial_apartment")).Post("/rentals/{id}/renew", s.handleMerchantRentalRenew)
			r.With(requireMerchantTypes("commercial_apartment")).Get("/apartment", s.handleMerchantApartment)
			r.With(requireMerchantTypes("commercial_apartment")).Put("/apartment", s.handleMerchantApartment)
			r.With(requireMerchantTypes("commercial_apartment")).Get("/apartment/rooms", s.handleMerchantRooms)
			r.With(requireMerchantTypes("commercial_apartment")).Post("/apartment/rooms", s.handleMerchantRooms)
			r.With(requireMerchantTypes("commercial_apartment")).Delete("/apartment/rooms/{id}", s.handleMerchantRooms)
		})
	})
}

func requireMerchantTypes(types ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(types))
	for _, merchantType := range types {
		allowed[merchantType] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := merchantPrincipal(r)
			if principal == nil {
				v1Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "请登录商户后台")
				return
			}
			if _, ok := allowed[principal.MerchantType]; !ok {
				v1Error(w, r, http.StatusForbidden, "MERCHANT_TYPE_FORBIDDEN", "当前商户类型无权访问此功能")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) handleMerchantMedia(w http.ResponseWriter, r *http.Request) {
	p := merchantPrincipal(r)
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	file, header, e := r.FormFile("file")
	if e != nil {
		v1Error(w, r, 400, "FILE_REQUIRED", "请选择图片")
		return
	}
	defer file.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	kind := http.DetectContentType(head[:n])
	ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[kind]
	if ext == "" {
		v1Error(w, r, 400, "IMAGE_TYPE_INVALID", "仅支持 JPEG、PNG 和 WebP")
		return
	}
	if _, e = file.Seek(0, 0); e != nil {
		v1Error(w, r, 400, "UPLOAD_FAILED", "读取图片失败")
		return
	}
	raw, _, e := merchantFileToken()
	if e != nil {
		v1Error(w, r, 500, "UPLOAD_FAILED", "生成文件名失败")
		return
	}
	dir := filepath.Join(s.cfg.MerchantUploadDir, strconv.FormatInt(p.MerchantID, 10))
	if e = os.MkdirAll(dir, 0750); e != nil {
		v1Error(w, r, 500, "UPLOAD_FAILED", "创建存储目录失败")
		return
	}
	name := raw + ext
	target := filepath.Join(dir, name)
	out, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if e != nil {
		v1Error(w, r, 500, "UPLOAD_FAILED", "保存图片失败")
		return
	}
	written, e := io.Copy(out, io.LimitReader(file, (5<<20)+1))
	closeErr := out.Close()
	if e != nil || closeErr != nil || written > (5<<20) {
		_ = os.Remove(target)
		v1Error(w, r, 400, "IMAGE_TOO_LARGE", "图片不能超过 5 MB")
		return
	}
	s.merchantAccounts.Audit(r.Context(), p, "media.upload", "merchant_media", name, clientIP(r))
	v1Data(w, r, 201, map[string]any{"url": fmt.Sprintf("/merchant-media/%d/%s", p.MerchantID, name), "name": header.Filename, "size_bytes": written})
}
func merchantFileToken() (string, []byte, error) {
	raw := make([]byte, 18)
	if _, e := rand.Read(raw); e != nil {
		return "", nil, e
	}
	return base64.RawURLEncoding.EncodeToString(raw), raw, nil
}

func (s *Server) merchantAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie(merchantSessionCookie)
		if e != nil {
			v1Error(w, r, 401, "UNAUTHENTICATED", "请登录商户后台")
			return
		}
		p, csrf, e := s.merchantAccounts.Resolve(r.Context(), c.Value)
		if e != nil {
			v1Error(w, r, 401, "UNAUTHENTICATED", "商户会话已失效")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			provided := r.Header.Get("X-CSRF-Token")
			h := sha256.Sum256([]byte(provided))
			if provided == "" || subtle.ConstantTimeCompare(h[:], csrf) != 1 {
				v1Error(w, r, 403, "CSRF_FAILED", "CSRF 校验失败")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(contextWithMerchant(r, p)))
	})
}
func contextWithMerchant(r *http.Request, p *merchantauth.Principal) context.Context {
	return context.WithValue(r.Context(), merchantContextKey{}, p)
}

func (s *Server) handleMerchantRegistrationStatus(w http.ResponseWriter, r *http.Request) {
	v1Data(w, r, 200, map[string]bool{"enabled": s.merchantAccounts.SelfRegistrationEnabled(r.Context())})
}
func (s *Server) handleMerchantRegister(w http.ResponseWriter, r *http.Request) {
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
	id, e := s.merchantAccounts.Register(r.Context(), q.Email, q.Phone, q.Password, q.DisplayName, q.MerchantName, q.MerchantType)
	if e != nil {
		v1Error(w, r, 400, "REGISTRATION_FAILED", e.Error())
		return
	}
	v1Data(w, r, 201, map[string]any{"id": id, "status": "pending"})
}
func (s *Server) handleMerchantLogin(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Identity string `json:"identity"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	p, e := s.merchantAccounts.Authenticate(r.Context(), q.Identity, q.Password)
	if e != nil {
		v1Error(w, r, 401, "INVALID_CREDENTIALS", "账号、密码或审核状态无效")
		return
	}
	st, ct, ex, e := s.merchantAccounts.CreateSession(r.Context(), p.ID, clientIP(r), r.UserAgent(), s.cfg.MerchantSessionTTL)
	if e != nil {
		v1Error(w, r, 500, "SESSION_FAILED", "创建会话失败")
		return
	}
	secure := s.cfg.Env != "development"
	http.SetCookie(w, &http.Cookie{Name: merchantSessionCookie, Value: st, Path: "/merchant-api/v1", Expires: ex, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: merchantCSRFCookie, Value: ct, Path: "/", Expires: ex, Secure: secure, SameSite: http.SameSiteStrictMode})
	v1Data(w, r, 200, map[string]any{"user": p, "expires_at": ex})
}
func (s *Server) handleMerchantMe(w http.ResponseWriter, r *http.Request) {
	v1Data(w, r, 200, merchantPrincipal(r))
}
func (s *Server) handleMerchantLogout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie(merchantSessionCookie); e == nil {
		_ = s.merchantAccounts.Revoke(r.Context(), c.Value)
	}
	secure := s.cfg.Env != "development"
	for _, c := range []*http.Cookie{{Name: merchantSessionCookie, Path: "/merchant-api/v1", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode}, {Name: merchantCSRFCookie, Path: "/", MaxAge: -1, Secure: secure, SameSite: http.SameSiteStrictMode}} {
		http.SetCookie(w, c)
	}
	v1Data(w, r, 200, map[string]bool{"logged_out": true})
}

func (s *Server) handleMerchantPassword(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	p := merchantPrincipal(r)
	var h string
	if e := s.pool.QueryRow(r.Context(), `SELECT password_hash FROM merchant_users WHERE id=$1`, p.ID).Scan(&h); e != nil || bcrypt.CompareHashAndPassword([]byte(h), []byte(q.Current)) != nil || len(q.Next) < 12 {
		v1Error(w, r, 400, "PASSWORD_INVALID", "原密码错误或新密码少于 12 位")
		return
	}
	nh, _ := bcrypt.GenerateFromPassword([]byte(q.Next), bcrypt.DefaultCost)
	_, e := s.pool.Exec(r.Context(), `UPDATE merchant_users SET password_hash=$1,updated_at=NOW() WHERE id=$2`, string(nh), p.ID)
	if e != nil {
		v1Error(w, r, 500, "SAVE_FAILED", "保存失败")
		return
	}
	s.merchantAccounts.Audit(r.Context(), p, "account.password.update", "merchant_user", strconv.FormatInt(p.ID, 10), clientIP(r))
	v1Data(w, r, 200, map[string]bool{"saved": true})
}

func (s *Server) handleMerchantClaims(w http.ResponseWriter, r *http.Request) {
	p := merchantPrincipal(r)
	if r.Method == "GET" {
		rows, e := s.pool.Query(r.Context(), `SELECT id,merchant_id,claimed_name,evidence,status,review_note,reviewed_at,created_at FROM merchant_claims WHERE merchant_user_id=$1 ORDER BY id DESC`, p.ID)
		if e != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		v, _ := queryMaps(rows)
		v1Data(w, r, 200, v)
		return
	}
	var q struct {
		MerchantID  *int64 `json:"merchant_id"`
		ClaimedName string `json:"claimed_name"`
		Evidence    string `json:"evidence"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	if strings.TrimSpace(q.ClaimedName) == "" || strings.TrimSpace(q.Evidence) == "" {
		v1Error(w, r, 400, "VALIDATION_ERROR", "请填写认领对象和证明材料")
		return
	}
	var id int64
	e := s.pool.QueryRow(r.Context(), `INSERT INTO merchant_claims(merchant_user_id,merchant_id,claimed_name,evidence) VALUES($1,$2,$3,$4) RETURNING id`, p.ID, q.MerchantID, strings.TrimSpace(q.ClaimedName), strings.TrimSpace(q.Evidence)).Scan(&id)
	if e != nil {
		v1Error(w, r, 400, "SAVE_FAILED", "提交失败")
		return
	}
	merchantWriteOK(s, w, r, p, "claim.submit", "merchant_claim", id)
}

func queryMaps(rows pgx.Rows) ([]map[string]any, error) {
	defer rows.Close()
	out := []map[string]any{}
	names := rows.FieldDescriptions()
	for rows.Next() {
		vals, e := rows.Values()
		if e != nil {
			return nil, e
		}
		m := map[string]any{}
		for i, v := range vals {
			m[names[i].Name] = v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func idParam(r *http.Request) int64 {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id
}
func merchantWriteOK(s *Server, w http.ResponseWriter, r *http.Request, p *merchantauth.Principal, action, resource string, id int64) {
	s.merchantAccounts.Audit(r.Context(), p, action, resource, strconv.FormatInt(id, 10), clientIP(r))
	v1Data(w, r, 200, map[string]any{"id": id, "saved": true})
}

func (s *Server) handleMerchantStore(w http.ResponseWriter, r *http.Request) {
	p := merchantPrincipal(r)
	if r.Method == "GET" {
		rows, e := s.pool.Query(r.Context(), `SELECT * FROM merchant_stores WHERE merchant_id=$1`, p.MerchantID)
		if e != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		v, e := queryMaps(rows)
		if e != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		if len(v) == 0 {
			v1Data(w, r, 200, nil)
		} else {
			v1Data(w, r, 200, v[0])
		}
		return
	}
	var q struct {
		Name          string   `json:"name"`
		Category      string   `json:"category"`
		Address       string   `json:"address"`
		ContactPhone  string   `json:"contact_phone"`
		ContactWechat string   `json:"contact_wechat"`
		Description   string   `json:"description"`
		LogoURL       string   `json:"logo_url"`
		Tags          []string `json:"tags"`
		CoverURLs     []string `json:"cover_urls"`
		BusinessHours any      `json:"business_hours"`
		AveragePrice  *int     `json:"average_price"`
		POIID         *int64   `json:"poi_id"`
		Latitude      *float64 `json:"latitude"`
		Longitude     *float64 `json:"longitude"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	hours, _ := json.Marshal(q.BusinessHours)
	_, e := s.pool.Exec(r.Context(), `INSERT INTO merchant_stores(merchant_id,name,category,tags,address,poi_id,unified_poi_id,latitude,longitude,contact_phone,contact_wechat,business_hours,average_price,description,logo_url,cover_urls,status) VALUES($1,$2,$3,$4,$5,$6,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'pending') ON CONFLICT(merchant_id) DO UPDATE SET name=EXCLUDED.name,category=EXCLUDED.category,tags=EXCLUDED.tags,address=EXCLUDED.address,poi_id=EXCLUDED.poi_id,unified_poi_id=EXCLUDED.unified_poi_id,latitude=EXCLUDED.latitude,longitude=EXCLUDED.longitude,contact_phone=EXCLUDED.contact_phone,contact_wechat=EXCLUDED.contact_wechat,business_hours=EXCLUDED.business_hours,average_price=EXCLUDED.average_price,description=EXCLUDED.description,logo_url=EXCLUDED.logo_url,cover_urls=EXCLUDED.cover_urls,status='pending',updated_at=NOW()`, p.MerchantID, q.Name, q.Category, q.Tags, q.Address, q.POIID, q.Latitude, q.Longitude, q.ContactPhone, q.ContactWechat, hours, q.AveragePrice, q.Description, q.LogoURL, q.CoverURLs)
	if e != nil {
		v1Error(w, r, 400, "SAVE_FAILED", "门店参数无效")
		return
	}
	merchantWriteOK(s, w, r, p, "store.update", "merchant_store", p.MerchantID)
}

func (s *Server) handleMerchantCategories(w http.ResponseWriter, r *http.Request) {
	p := merchantPrincipal(r)
	if r.Method == "GET" {
		rows, e := s.pool.Query(r.Context(), `SELECT id,name,sort,active,created_at,updated_at FROM merchant_menu_categories WHERE merchant_id=$1 ORDER BY sort DESC,id`, p.MerchantID)
		if e != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		v, _ := queryMaps(rows)
		v1Data(w, r, 200, v)
		return
	}
	if r.Method == "DELETE" {
		id := idParam(r)
		tag, e := s.pool.Exec(r.Context(), `DELETE FROM merchant_menu_categories WHERE id=$1 AND merchant_id=$2`, id, p.MerchantID)
		if e != nil || tag.RowsAffected() == 0 {
			v1Error(w, r, 404, "NOT_FOUND", "分类不存在")
			return
		}
		merchantWriteOK(s, w, r, p, "menu.category.delete", "menu_category", id)
		return
	}
	var q struct {
		ID     int64
		Name   string
		Sort   int
		Active bool
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	var id int64
	e := s.pool.QueryRow(r.Context(), `INSERT INTO merchant_menu_categories(merchant_id,name,sort,active) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,sort=EXCLUDED.sort,active=EXCLUDED.active,updated_at=NOW() WHERE merchant_menu_categories.merchant_id=$1 RETURNING id`, p.MerchantID, q.Name, q.Sort, q.Active).Scan(&id)
	if q.ID > 0 {
		e = s.pool.QueryRow(r.Context(), `UPDATE merchant_menu_categories SET name=$1,sort=$2,active=$3,updated_at=NOW() WHERE id=$4 AND merchant_id=$5 RETURNING id`, q.Name, q.Sort, q.Active, q.ID, p.MerchantID).Scan(&id)
	}
	if e != nil {
		v1Error(w, r, 400, "SAVE_FAILED", "保存失败")
		return
	}
	merchantWriteOK(s, w, r, p, "menu.category.save", "menu_category", id)
}

func (s *Server) handleMerchantItems(w http.ResponseWriter, r *http.Request) {
	p := merchantPrincipal(r)
	if r.Method == "GET" {
		rows, e := s.pool.Query(r.Context(), `SELECT i.*,c.name category_name FROM merchant_menu_items i LEFT JOIN merchant_menu_categories c ON c.id=i.category_id WHERE i.merchant_id=$1 ORDER BY i.sort DESC,i.id`, p.MerchantID)
		if e != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		v, _ := queryMaps(rows)
		v1Data(w, r, 200, v)
		return
	}
	if r.Method == "DELETE" {
		id := idParam(r)
		tag, e := s.pool.Exec(r.Context(), `DELETE FROM merchant_menu_items WHERE id=$1 AND merchant_id=$2`, id, p.MerchantID)
		if e != nil || tag.RowsAffected() == 0 {
			v1Error(w, r, 404, "NOT_FOUND", "菜品不存在")
			return
		}
		merchantWriteOK(s, w, r, p, "menu.item.delete", "menu_item", id)
		return
	}
	var q struct {
		ID          int64  `json:"id"`
		CategoryID  int64  `json:"category_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		ImageURL    string `json:"image_url"`
		PriceCents  int    `json:"price_cents"`
		Sort        int    `json:"sort"`
		Recommended bool   `json:"recommended"`
		IsNew       bool   `json:"is_new"`
		Available   bool   `json:"available"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	if q.CategoryID > 0 {
		var n int
		if s.pool.QueryRow(r.Context(), `SELECT count(*) FROM merchant_menu_categories WHERE id=$1 AND merchant_id=$2`, q.CategoryID, p.MerchantID).Scan(&n) != nil || n == 0 {
			v1Error(w, r, 400, "IDOR_BLOCKED", "分类不属于当前商户")
			return
		}
	}
	var id int64
	var e error
	if q.ID > 0 {
		e = s.pool.QueryRow(r.Context(), `UPDATE merchant_menu_items SET category_id=NULLIF($1,0),name=$2,description=$3,price_cents=$4,image_url=$5,recommended=$6,is_new=$7,available=$8,sort=$9,updated_at=NOW() WHERE id=$10 AND merchant_id=$11 RETURNING id`, q.CategoryID, q.Name, q.Description, q.PriceCents, q.ImageURL, q.Recommended, q.IsNew, q.Available, q.Sort, q.ID, p.MerchantID).Scan(&id)
	} else {
		e = s.pool.QueryRow(r.Context(), `INSERT INTO merchant_menu_items(merchant_id,category_id,name,description,price_cents,image_url,recommended,is_new,available,sort) VALUES($1,NULLIF($2,0),$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, p.MerchantID, q.CategoryID, q.Name, q.Description, q.PriceCents, q.ImageURL, q.Recommended, q.IsNew, q.Available, q.Sort).Scan(&id)
	}
	if e != nil {
		v1Error(w, r, 400, "SAVE_FAILED", "保存失败")
		return
	}
	merchantWriteOK(s, w, r, p, "menu.item.save", "menu_item", id)
}

func (s *Server) handleMerchantPromotions(w http.ResponseWriter, r *http.Request) {
	p := merchantPrincipal(r)
	if r.Method == "GET" {
		rows, e := s.pool.Query(r.Context(), `SELECT * FROM merchant_promotions WHERE merchant_id=$1 ORDER BY id DESC`, p.MerchantID)
		if e != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		v, _ := queryMaps(rows)
		v1Data(w, r, 200, v)
		return
	}
	if r.Method == "DELETE" {
		id := idParam(r)
		tag, e := s.pool.Exec(r.Context(), `DELETE FROM merchant_promotions WHERE id=$1 AND merchant_id=$2 AND status IN('draft','rejected')`, id, p.MerchantID)
		if e != nil || tag.RowsAffected() == 0 {
			v1Error(w, r, 409, "DELETE_DENIED", "仅草稿或被拒活动可删除")
			return
		}
		merchantWriteOK(s, w, r, p, "promotion.delete", "promotion", id)
		return
	}
	var q struct {
		ID            int64      `json:"id"`
		PromotionType string     `json:"promotion_type"`
		Title         string     `json:"title"`
		Description   string     `json:"description"`
		StartsAt      *time.Time `json:"starts_at"`
		EndsAt        *time.Time `json:"ends_at"`
		Submit        bool       `json:"submit"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	status := "draft"
	if q.Submit {
		status = "pending"
	}
	var id int64
	var e error
	if q.ID > 0 {
		e = s.pool.QueryRow(r.Context(), `UPDATE merchant_promotions SET promotion_type=$1,title=$2,description=$3,starts_at=$4,ends_at=$5,status=$6,review_note='',updated_at=NOW() WHERE id=$7 AND merchant_id=$8 AND status IN('draft','rejected') RETURNING id`, q.PromotionType, q.Title, q.Description, q.StartsAt, q.EndsAt, status, q.ID, p.MerchantID).Scan(&id)
	} else {
		e = s.pool.QueryRow(r.Context(), `INSERT INTO merchant_promotions(merchant_id,promotion_type,title,description,starts_at,ends_at,status) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, p.MerchantID, q.PromotionType, q.Title, q.Description, q.StartsAt, q.EndsAt, status).Scan(&id)
	}
	if e != nil {
		v1Error(w, r, 400, "SAVE_FAILED", "活动参数无效或当前状态不可编辑")
		return
	}
	merchantWriteOK(s, w, r, p, "promotion.save", "promotion", id)
}

func (s *Server) handleMerchantAnalytics(w http.ResponseWriter, r *http.Request) {
	p := merchantPrincipal(r)
	rows, e := s.pool.Query(r.Context(), `SELECT date,views,favorites,contacts,map_impressions,search_impressions FROM merchant_metrics_daily WHERE merchant_id=$1 AND date>=CURRENT_DATE-30 ORDER BY date`, p.MerchantID)
	if e != nil {
		v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
		return
	}
	v, _ := queryMaps(rows)
	v1Data(w, r, 200, v)
}
func (s *Server) handleMerchantApartment(w http.ResponseWriter, r *http.Request) {
	p := merchantPrincipal(r)
	if r.Method == "GET" {
		rows, e := s.pool.Query(r.Context(), `SELECT * FROM merchant_apartments WHERE merchant_id=$1`, p.MerchantID)
		if e != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		v, _ := queryMaps(rows)
		if len(v) == 0 {
			v1Data(w, r, 200, nil)
		} else {
			v1Data(w, r, 200, v[0])
		}
		return
	}
	var q struct {
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		Address      string   `json:"address"`
		ContactPhone string   `json:"contact_phone"`
		POIID        *int64   `json:"poi_id"`
		Latitude     *float64 `json:"latitude"`
		Longitude    *float64 `json:"longitude"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	_, e := s.pool.Exec(r.Context(), `INSERT INTO merchant_apartments(merchant_id,name,description,poi_id,unified_poi_id,address,latitude,longitude,contact_phone,status) VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8,'pending') ON CONFLICT(merchant_id) DO UPDATE SET name=EXCLUDED.name,description=EXCLUDED.description,poi_id=EXCLUDED.poi_id,unified_poi_id=EXCLUDED.unified_poi_id,address=EXCLUDED.address,latitude=EXCLUDED.latitude,longitude=EXCLUDED.longitude,contact_phone=EXCLUDED.contact_phone,status='pending',updated_at=NOW()`, p.MerchantID, q.Name, q.Description, q.POIID, q.Address, q.Latitude, q.Longitude, q.ContactPhone)
	if e != nil {
		v1Error(w, r, 400, "SAVE_FAILED", "保存失败")
		return
	}
	merchantWriteOK(s, w, r, p, "apartment.update", "apartment", p.MerchantID)
}
func (s *Server) handleMerchantRooms(w http.ResponseWriter, r *http.Request) {
	p := merchantPrincipal(r)
	if r.Method == "GET" {
		rows, e := s.pool.Query(r.Context(), `SELECT r.* FROM merchant_apartment_rooms r WHERE r.merchant_id=$1 ORDER BY r.id`, p.MerchantID)
		if e != nil {
			v1Error(w, r, 500, "QUERY_FAILED", "查询失败")
			return
		}
		v, _ := queryMaps(rows)
		v1Data(w, r, 200, v)
		return
	}
	if r.Method == "DELETE" {
		id := idParam(r)
		tag, e := s.pool.Exec(r.Context(), `DELETE FROM merchant_apartment_rooms WHERE id=$1 AND merchant_id=$2`, id, p.MerchantID)
		if e != nil || tag.RowsAffected() == 0 {
			v1Error(w, r, 404, "NOT_FOUND", "房型不存在")
			return
		}
		merchantWriteOK(s, w, r, p, "room.delete", "apartment_room", id)
		return
	}
	var q struct {
		ID            int64      `json:"id"`
		Name          string     `json:"name"`
		RentCents     int        `json:"rent_cents"`
		AreaSQM       *float64   `json:"area_sqm"`
		PaymentTerms  string     `json:"payment_terms"`
		AvailableFrom *time.Time `json:"available_from"`
		Status        string     `json:"status"`
		ImageURLs     []string   `json:"image_urls"`
	}
	if !decodeJSON(w, r, &q) {
		return
	}
	var apartmentID int64
	if e := s.pool.QueryRow(r.Context(), `SELECT id FROM merchant_apartments WHERE merchant_id=$1`, p.MerchantID).Scan(&apartmentID); e != nil {
		v1Error(w, r, 409, "APARTMENT_REQUIRED", "请先保存公寓信息")
		return
	}
	var id int64
	var e error
	if q.ID > 0 {
		e = s.pool.QueryRow(r.Context(), `UPDATE merchant_apartment_rooms SET name=$1,rent_cents=$2,area_sqm=$3,payment_terms=$4,available_from=$5,status=$6,image_urls=$7,updated_at=NOW() WHERE id=$8 AND merchant_id=$9 RETURNING id`, q.Name, q.RentCents, q.AreaSQM, q.PaymentTerms, q.AvailableFrom, q.Status, q.ImageURLs, q.ID, p.MerchantID).Scan(&id)
	} else {
		e = s.pool.QueryRow(r.Context(), `INSERT INTO merchant_apartment_rooms(merchant_id,apartment_id,name,rent_cents,area_sqm,payment_terms,available_from,status,image_urls) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, p.MerchantID, apartmentID, q.Name, q.RentCents, q.AreaSQM, q.PaymentTerms, q.AvailableFrom, q.Status, q.ImageURLs).Scan(&id)
	}
	if e != nil {
		v1Error(w, r, 400, "SAVE_FAILED", "保存失败")
		return
	}
	merchantWriteOK(s, w, r, p, "room.save", "apartment_room", id)
}
