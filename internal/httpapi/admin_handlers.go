package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"zonenan-backend/internal/store"
)

// adminAuth 管理鉴权:请求头 Authorization: Bearer <ADMIN_SECRET>。
// B2:密钥来自 config(无硬编码兜底),常量时间比较防时序侧信道。
func (s *Server) adminAuth(next http.Handler) http.Handler {
	secret := []byte(s.cfg.AdminSecret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := []byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if subtle.ConstantTimeCompare(token, secret) != 1 {
			Fail(w, http.StatusUnauthorized, "管理密钥错误")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Admin handlers (简易管理,需 Bearer token = ADMIN_SECRET) ---

const adminUserPageSize int64 = 25

// 搜索关联身份时用 EXISTS，避免重复计数或丢失用户的其他身份。
const adminUserFilter = `($1 = '' OR u.id = $2 OR u.nickname ILIKE $3
	OR EXISTS(SELECT 1 FROM zonenan_identities si WHERE si.user_id = u.id
		AND (si.provider || ':' || si.display_name) ILIKE $3))`

type adminUserQuery struct {
	page    int64
	search  string
	pattern string
	userID  int64
}

func parseAdminUserQuery(values url.Values) (adminUserQuery, error) {
	q := adminUserQuery{page: 1, search: strings.TrimSpace(values.Get("q"))}
	if raw := values.Get("page"); raw != "" {
		page, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || page < 1 {
			return q, errors.New("page 必须为正整数")
		}
		q.page = page
	}
	if utf8.RuneCountInString(q.search) > 200 {
		return q, errors.New("搜索关键词最多 200 个字符")
	}
	id, err := strconv.ParseInt(q.search, 10, 64)
	if err == nil && id > 0 {
		q.userID = id
	}
	q.pattern = "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q.search) + "%"
	return q, nil
}

func adminUserPage(page, total int64) int64 {
	last := int64(1)
	if total > 0 {
		last = (total-1)/adminUserPageSize + 1
	}
	if page > last {
		return last
	}
	return page
}

// handleAdminUsers 分页列出用户（含完整身份信息），每页固定 25 条。
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	q, err := parseAdminUserQuery(r.URL.Query())
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	var total int64
	if err := s.pool.QueryRow(r.Context(),
		`SELECT count(*) FROM zonenan_users u WHERE `+adminUserFilter,
		q.search, q.userID, q.pattern).Scan(&total); err != nil {
		Fail(w, 500, "查询失败")
		return
	}
	q.page = adminUserPage(q.page, total)
	rows, err := s.pool.Query(r.Context(),
		`WITH page_users AS (
			SELECT u.* FROM zonenan_users u WHERE `+adminUserFilter+`
			ORDER BY u.id LIMIT $4 OFFSET $5
		)
		SELECT u.id, u.nickname, u.avatar_url, u.is_banned, u.created_at,
		       u.role, u.is_whitelisted,
		       EXISTS(SELECT 1 FROM beta_memberships b WHERE b.user_id = u.id) AS is_beta,
		       EXISTS(SELECT 1 FROM membership_grants g WHERE g.user_id=u.id AND g.membership_type_key='premium' AND g.revoked_at IS NULL AND g.starts_at <= NOW() AND g.expires_at > NOW()) AS is_premium,
		       COALESCE(i.identities, ''),
		       v.app_version, v.platform, v.last_seen_at, v.os_version, v.device_brand, v.device_model, u.last_online_at
		  FROM page_users u
		  LEFT JOIN LATERAL (
			SELECT string_agg(i.provider || ':' || i.display_name, ', ' ORDER BY i.id) AS identities
			FROM zonenan_identities i WHERE i.user_id = u.id
		  ) i ON true
		  LEFT JOIN analytics_user_versions v ON v.user_id = u.id
		 ORDER BY u.id`, q.search, q.userID, q.pattern, adminUserPageSize, (q.page-1)*adminUserPageSize)
	if err != nil {
		Fail(w, 500, "查询失败")
		return
	}
	defer rows.Close()
	users := []map[string]interface{}{}
	for rows.Next() {
		var id int64
		var nickname, avatar, identities, role string
		var banned, whitelisted, beta, premium bool
		var created, appVersion, platform, versionSeen, osVersion, deviceBrand, deviceModel, lastOnline interface{}
		if err := rows.Scan(&id, &nickname, &avatar, &banned, &created, &role, &whitelisted, &beta, &premium, &identities, &appVersion, &platform, &versionSeen, &osVersion, &deviceBrand, &deviceModel, &lastOnline); err != nil {
			Fail(w, 500, "查询失败")
			return
		}
		users = append(users, map[string]interface{}{
			"id": id, "nickname": nickname, "avatar_url": avatar,
			"is_banned": banned, "role": role, "is_whitelisted": whitelisted, "is_beta": beta, "is_premium": premium,
			"created_at": created, "identities": identities,
			"app_version": appVersion, "app_platform": platform, "app_last_seen_at": versionSeen,
			"app_os_version": osVersion, "device_brand": deviceBrand, "device_model": deviceModel,
			"last_online_at": lastOnline,
		})
	}
	if err := rows.Err(); err != nil {
		Fail(w, 500, "查询失败")
		return
	}
	OK(w, map[string]interface{}{
		"items": users, "total": total, "page": q.page, "page_size": adminUserPageSize,
	})
}

// handleAdminBanUser 封禁/解封用户。
func (s *Server) handleAdminBanUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"user_id"`
		Ban    bool  `json:"ban"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	_, err := s.pool.Exec(r.Context(),
		`UPDATE zonenan_users SET is_banned=$1 WHERE id=$2`, req.Ban, req.UserID)
	if err != nil {
		Fail(w, 500, "操作失败")
		return
	}
	OK(w, map[string]interface{}{"user_id": req.UserID, "is_banned": req.Ban})
}

// handleAdminGradeStats 给分数据概览。
func (s *Server) handleAdminGradeStats(w http.ResponseWriter, r *http.Request) {
	var profiles, scores, links int
	s.pool.QueryRow(r.Context(), `SELECT count(*) FROM grade_course_profiles`).Scan(&profiles)
	s.pool.QueryRow(r.Context(), `SELECT count(*) FROM grade_user_scores`).Scan(&scores)
	s.pool.QueryRow(r.Context(), `SELECT count(*) FROM grade_score_links`).Scan(&links)
	OK(w, map[string]int{"profiles": profiles, "scores": scores, "links": links})
}

// handleAdminGetSettings 读取 app_settings。
func (s *Server) handleAdminGetSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT key, value, updated_at FROM app_settings ORDER BY key`)
	if err != nil {
		Fail(w, 500, "查询失败")
		return
	}
	defer rows.Close()
	settings := []map[string]interface{}{}
	for rows.Next() {
		var k, v string
		var updated interface{}
		rows.Scan(&k, &v, &updated)
		settings = append(settings, map[string]interface{}{"key": k, "value": v, "updated_at": updated})
	}
	OK(w, settings)
}

// handleAdminSetSetting 修改 app_settings 单项。
func (s *Server) handleAdminSetSetting(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.setting.Set(r.Context(), req.Key, req.Value); err != nil {
		Fail(w, 500, "保存失败")
		return
	}
	OK(w, map[string]string{"key": req.Key, "value": req.Value})
}

// handleAdminGetLegalDocs 列出所有协议文档。
func (s *Server) handleAdminGetLegalDocs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT doc_type, version, title, content, published_at FROM legal_documents ORDER BY doc_type, version DESC`)
	if err != nil {
		Fail(w, 500, "查询失败")
		return
	}
	defer rows.Close()
	docs := []store.LegalDoc{}
	for rows.Next() {
		var d store.LegalDoc
		rows.Scan(&d.DocType, &d.Version, &d.Title, &d.Content, &d.PublishedAt)
		docs = append(docs, d)
	}
	OK(w, docs)
}

// handleAdminUpdateLegalDoc 新增/更新协议文档(插入新版本)。
func (s *Server) handleAdminUpdateLegalDoc(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DocType string `json:"doc_type"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// 取当前最大版本 +1
	var maxVer int
	s.pool.QueryRow(r.Context(),
		`SELECT COALESCE(MAX(version),0) FROM legal_documents WHERE doc_type=$1`, req.DocType).Scan(&maxVer)
	newVer := maxVer + 1
	_, err := s.pool.Exec(r.Context(),
		`INSERT INTO legal_documents(doc_type, version, title, content) VALUES($1,$2,$3,$4)`,
		req.DocType, newVer, req.Title, req.Content)
	if err != nil {
		Fail(w, 500, "保存失败")
		return
	}
	OK(w, map[string]interface{}{"doc_type": req.DocType, "version": newVer})
}

// handleAdminEditLegalDoc 原地修改某类型协议的当前版本(不新增版本号)。
func (s *Server) handleAdminEditLegalDoc(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DocType string `json:"doc_type"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DocType == "" {
		Fail(w, 400, "缺少 doc_type")
		return
	}
	updated, err := s.legal.UpdateLatest(r.Context(), req.DocType, req.Title, req.Content)
	if err != nil {
		Fail(w, 500, "保存失败")
		return
	}
	if !updated {
		Fail(w, 404, "该协议尚无版本,请先发布")
		return
	}
	// 修改后清缓存,App 下次拉到新内容。
	OK(w, map[string]string{"doc_type": req.DocType})
}

// handleAdminDeleteUser 删除用户。
func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id == 0 {
		Fail(w, 400, "缺少 id")
		return
	}
	_, err := s.pool.Exec(r.Context(), `DELETE FROM zonenan_users WHERE id=$1`, id)
	if err != nil {
		Fail(w, 500, "删除失败")
		return
	}
	OK(w, map[string]int64{"deleted": id})
}
