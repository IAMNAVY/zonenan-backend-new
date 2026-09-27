package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const adminCrashPageSize int64 = 25

const adminCrashFilter = `($1 = '' OR c.id = $2 OR COALESCE(c.user_id, 0) = $2
	OR c.app_version ILIKE $3 OR c.platform ILIKE $3 OR c.device_model ILIKE $3
	OR c.error_type ILIKE $3 OR c.message ILIKE $3 OR c.stack ILIKE $3
	OR COALESCE(u.nickname, '') ILIKE $3)
	AND ($4 = '' OR c.error_type = $4)`

type adminCrashQuery struct {
	page      int64
	search    string
	pattern   string
	numeric   int64
	errorType string
}

func parseAdminCrashQuery(values url.Values) (adminCrashQuery, error) {
	q := adminCrashQuery{page: 1, search: strings.TrimSpace(values.Get("q"))}
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
	q.errorType = strings.TrimSpace(values.Get("error_type"))
	if utf8.RuneCountInString(q.errorType) > 100 {
		return q, errors.New("错误分类最多 100 个字符")
	}
	if numeric, err := strconv.ParseInt(q.search, 10, 64); err == nil && numeric > 0 {
		q.numeric = numeric
	}
	q.pattern = "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q.search) + "%"
	return q, nil
}

func adminCrashPage(page, total int64) int64 {
	last := int64(1)
	if total > 0 {
		last = (total-1)/adminCrashPageSize + 1
	}
	if page > last {
		return last
	}
	return page
}

// handleAdminCrashReports 分页查询崩溃上报；关键词可匹配记录/用户 ID、昵称、
// 版本、平台、机型、错误类型、消息和堆栈。
func (s *Server) handleAdminCrashReports(w http.ResponseWriter, r *http.Request) {
	q, err := parseAdminCrashQuery(r.URL.Query())
	if err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}

	var total int64
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*)
		FROM crash_reports c
		LEFT JOIN zonenan_users u ON u.id = c.user_id
		WHERE `+adminCrashFilter,
		q.search, q.numeric, q.pattern, q.errorType,
	).Scan(&total); err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}

	q.page = adminCrashPage(q.page, total)
	rows, err := s.pool.Query(r.Context(), `
		SELECT c.id, c.user_id, COALESCE(u.nickname, ''), c.app_version,
		       c.platform, c.device_model, c.error_type, c.message, c.stack,
		       c.created_at
		FROM crash_reports c
		LEFT JOIN zonenan_users u ON u.id = c.user_id
		WHERE `+adminCrashFilter+`
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT $5 OFFSET $6`,
		q.search, q.numeric, q.pattern, q.errorType, adminCrashPageSize, (q.page-1)*adminCrashPageSize,
	)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	defer rows.Close()

	items := []map[string]interface{}{}
	for rows.Next() {
		var id int64
		var userID *int64
		var nickname, appVersion, platform, deviceModel, errorType, message, stack string
		var createdAt interface{}
		if err := rows.Scan(
			&id, &userID, &nickname, &appVersion, &platform, &deviceModel,
			&errorType, &message, &stack, &createdAt,
		); err != nil {
			Fail(w, http.StatusInternalServerError, "查询失败")
			return
		}
		items = append(items, map[string]interface{}{
			"id": id, "user_id": userID, "nickname": nickname,
			"app_version": appVersion, "platform": platform, "device_model": deviceModel,
			"error_type": errorType, "message": message, "stack": stack,
			"created_at": createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	errorTypes := []string{}
	typeRows, err := s.pool.Query(r.Context(), `
		SELECT DISTINCT error_type FROM crash_reports
		WHERE error_type <> '' ORDER BY error_type`)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	defer typeRows.Close()
	for typeRows.Next() {
		var errorType string
		if err := typeRows.Scan(&errorType); err != nil {
			Fail(w, http.StatusInternalServerError, "查询失败")
			return
		}
		errorTypes = append(errorTypes, errorType)
	}
	if err := typeRows.Err(); err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}

	OK(w, map[string]interface{}{
		"items": items, "total": total, "page": q.page, "page_size": adminCrashPageSize,
		"error_types": errorTypes,
	})
}

// handleAdminDeleteCrashReports 删除单条或清空全部崩溃上报。省略 id 时清空
// 全表，但保留序列当前位置，避免管理操作后重复使用历史记录编号。
func (s *Server) handleAdminDeleteCrashReports(w http.ResponseWriter, r *http.Request) {
	rawID := strings.TrimSpace(r.URL.Query().Get("id"))
	var (
		result interface{ RowsAffected() int64 }
		err    error
	)
	if rawID == "" {
		result, err = s.pool.Exec(r.Context(), `DELETE FROM crash_reports`)
	} else {
		id, parseErr := strconv.ParseInt(rawID, 10, 64)
		if parseErr != nil || id <= 0 {
			Fail(w, http.StatusBadRequest, "id 必须为正整数")
			return
		}
		result, err = s.pool.Exec(r.Context(), `DELETE FROM crash_reports WHERE id=$1`, id)
	}
	if err != nil {
		Fail(w, http.StatusInternalServerError, "删除失败")
		return
	}
	OK(w, map[string]interface{}{"deleted": result.RowsAffected()})
}
