package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"zonenan-backend/internal/store"
)

type syncReq struct {
	Records []store.GradeRecord `json:"records"`
	Full    bool                `json:"full"`
}

// handleGradeSync:客户端上传抓取到的成绩。必须已绑校园且 consent=active。
func (s *Server) handleGradeSync(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireCampus(w, r)
	if !ok {
		return
	}
	st, err := s.grades.GetAuthStatus(r.Context(), userIDFrom(r))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	if st.ConsentStatus != "active" {
		Fail(w, http.StatusForbidden, "请先同意给分数据协议")
		return
	}
	var req syncReq
	if !decodeJSON(w, r, &req) {
		return
	}
	// S2:拒绝空上传。否则 {"records":[]} 会把 last_sync_at 置非空,
	// 令 CanQuery 永久为真 → 零贡献白嫖全校给分,架空"贡献才能查"门槛。
	if len(req.Records) == 0 {
		Fail(w, http.StatusBadRequest, "没有可上传的成绩")
		return
	}
	if len(req.Records) > 500 {
		Fail(w, http.StatusBadRequest, "单次上传成绩过多")
		return
	}
	synced, err := s.grades.SaveFetched(r.Context(), userIDFrom(r), hash, req.Records, req.Full)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "保存失败")
		return
	}
	OK(w, map[string]int{"synced": synced})
}

// queryTier 表示当前用户查给分的资格档位。
type queryTier int

const (
	tierDenied    queryTier = iota // 无资格(免费额度已耗尽)
	tierUnlimited                  // 无限(已贡献/宽限期内/白名单/admin)
	tierFree                       // 免费体验(未绑校园或无贡献,按课名去重限量)
)

// unlimitedGradePath 返回无需消耗体验次数的查询路径；空串表示进入体验档。
// 由 auth-status 与实际查询守卫共用，避免两边对 normal/grace/privileged 的
// 判断再次漂移。now 由调用方传入，便于覆盖边界测试。
func unlimitedGradePath(st *store.AuthStatus, privileged bool, graceDays int, now time.Time) string {
	if privileged {
		return "privileged"
	}
	if st.CanQuery {
		return "normal"
	}
	if st.ConsentStatus == "active" && st.LastSyncAt != nil &&
		now.Sub(*st.LastSyncAt) <= time.Duration(graceDays)*24*time.Hour {
		return "grace"
	}
	return ""
}

// evalQueryTier 判定当前用户的查给分档位(不产生副作用,不扣费)。
//
// 三条路径:
//  1. 正常:已贡献 → 无限。
//  2. 贡献宽限:已绑校园+consent=active+上次贡献在 N 天内 → 无限。
//  3. 白名单/admin → 无限(自测/运营账号不受额度限制)。
//  4. 其余 → 免费体验档,由 consumeFreeQuery 按课名去重限量。
func (s *Server) evalQueryTier(w http.ResponseWriter, r *http.Request) (queryTier, bool) {
	uid := userIDFrom(r)

	// 白名单/admin:无限,免额度。
	if uid > 0 && s.users.IsPrivileged(r.Context(), uid) {
		return tierUnlimited, true
	}

	st, err := s.grades.GetAuthStatus(r.Context(), uid)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return tierDenied, false
	}

	graceDays := s.setting.GetInt(r.Context(), "grade_contrib_grace_days", 30)
	if unlimitedGradePath(st, false, graceDays, time.Now()) != "" {
		return tierUnlimited, true
	}

	return tierFree, true
}

// checkCanQuery:仅校验能否浏览给分(suggest/semesters/search 列表),不扣费。
// 免费体验档也放行浏览(真正的额度在 consumeFreeQuery 里按「选中的课程名」扣)。
func (s *Server) checkCanQuery(w http.ResponseWriter, r *http.Request) bool {
	tier, ok := s.evalQueryTier(w, r)
	if !ok {
		return false
	}
	if tier == tierUnlimited || tier == tierFree {
		return true
	}
	Fail(w, http.StatusForbidden, "请先绑定校园身份、同意协议并成功抓取一次成绩后再查询")
	return false
}

// consumeFreeQuery:查看某门具体课程时调用。无限档直接放行;免费档按「课程名」
// 去重扣一次额度(同一门课翻页/切排序/再看不重复扣),并叠加每 IP 每日上限(H3)。
func (s *Server) consumeFreeQuery(w http.ResponseWriter, r *http.Request, courseName string) bool {
	tier, ok := s.evalQueryTier(w, r)
	if !ok {
		return false
	}
	if tier == tierUnlimited {
		return true
	}

	freeLimit := s.setting.GetInt(r.Context(), "grade_free_query_limit", 2)
	if freeLimit <= 0 {
		Fail(w, http.StatusForbidden, "请先绑定校园身份、同意协议并成功抓取一次成绩后再查询")
		return false
	}

	uid := userIDFrom(r)
	deviceFP := r.Header.Get("X-Device-Fingerprint")
	courseName = strings.TrimSpace(courseName)

	// 同一门课已计过费 → 免费重复查看,不再扣、不受额度影响。
	if courseName != "" {
		counted, err := s.grades.FreeQueryCountedFor(r.Context(), uid, deviceFP, courseName)
		if err != nil {
			Fail(w, http.StatusInternalServerError, "查询失败")
			return false
		}
		if counted {
			return true
		}
	}

	// 账号+设备维度的总额度。
	used, err := s.grades.FreeQueryCount(r.Context(), uid, deviceFP)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return false
	}
	if used >= freeLimit {
		Fail(w, http.StatusForbidden, "免费查询次数已用完，请绑定校园身份并贡献成绩后解锁")
		return false
	}

	// H3:每 IP 每日上限,防批量注册 + 空指纹绕过账号维度限制。
	ip := clientIP(r)
	ipLimit := s.setting.GetInt(r.Context(), "grade_free_query_daily_ip_limit", 3)
	if ipLimit > 0 {
		ipUsed, err := s.grades.FreeQueryIPCountToday(r.Context(), ip)
		if err != nil {
			Fail(w, http.StatusInternalServerError, "查询失败")
			return false
		}
		if ipUsed >= ipLimit {
			Fail(w, http.StatusForbidden, "今日免费查询已达上限，请绑定校园身份并贡献成绩后解锁")
			return false
		}
	}

	if err := s.grades.RecordFreeQuery(r.Context(), uid, deviceFP, courseName, ip); err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return false
	}
	return true
}

// handleGradeSearch:搜索聚合均分 + 5档直方图 + 中位数,支持排序和分页。
// 计费点:选中某门课名后展示「该课多个老师的给分」即在此按课名去重扣一次免费额度;
// 翻页/切排序会重复打此端点,但同一课名只扣一次。
func (s *Server) handleGradeSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Keyword  string `json:"keyword"`
		Teacher  string `json:"teacher"`
		Semester string `json:"semester"`
		Sort     string `json:"sort"`
		Page     int    `json:"page"`
	}
	if !decodeJSONOptional(w, r, &req) {
		return
	}
	// 按选中的课程名(keyword)去重计费;免费额度耗尽则拒绝。
	if !s.consumeFreeQuery(w, r, req.Keyword) {
		return
	}
	rows, err := s.grades.Search(r.Context(), store.SearchParams{
		Keyword:  req.Keyword,
		Teacher:  req.Teacher,
		Semester: req.Semester,
		Sort:     req.Sort,
		Page:     req.Page,
		PageSize: 10,
	})
	if err != nil {
		Fail(w, http.StatusInternalServerError, "搜索失败")
		return
	}
	OK(w, rows)
}

// handleGradeSemesters:有数据的学期列表。
func (s *Server) handleGradeSemesters(w http.ResponseWriter, r *http.Request) {
	if !s.checkCanQuery(w, r) {
		return
	}
	sems, err := s.grades.Semesters(r.Context())
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, sems)
}

// handleGradeSuggest:课程名候选。
func (s *Server) handleGradeSuggest(w http.ResponseWriter, r *http.Request) {
	if !s.checkCanQuery(w, r) {
		return
	}
	kw := r.URL.Query().Get("keyword")
	list, err := s.grades.Suggest(r.Context(), kw, 10)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, list)
}

// handleGradeDetail:课程详情(分布+评分+评教分页)。
func (s *Server) handleGradeDetail(w http.ResponseWriter, r *http.Request) {
	// detail 不再扣费(计费点已前移到 search 选中课名处),仅校验资格。
	if !s.checkCanQuery(w, r) {
		return
	}
	courseID, _ := strconv.ParseInt(chi.URLParam(r, "courseId"), 10, 64)
	semester := chi.URLParam(r, "semester")
	if semester == "all" {
		semester = ""
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	userHash, _ := s.users.StudentHashOf(r.Context(), userIDFrom(r))
	detail, err := s.grades.Detail(r.Context(), courseID, semester, userIDFrom(r), userHash, page, 10)
	if err != nil {
		Fail(w, http.StatusNotFound, "课程不存在")
		return
	}
	OK(w, detail)
}

// handleGradeMine:我的贡献成绩。
func (s *Server) handleGradeMine(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireCampus(w, r)
	if !ok {
		return
	}
	list, err := s.grades.MyContributions(r.Context(), hash)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, list)
}

// handleGradeFavorite:收藏/取消收藏课程。
func (s *Server) handleGradeFavorite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProfileID int64  `json:"profile_id"`
		Action    string `json:"action"` // "add" or "remove"
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	uid := userIDFrom(r)
	var err error
	if req.Action == "remove" {
		err = s.grades.FavoriteRemove(r.Context(), uid, req.ProfileID)
	} else {
		err = s.grades.FavoriteAdd(r.Context(), uid, req.ProfileID)
	}
	if err != nil {
		Fail(w, http.StatusInternalServerError, "操作失败")
		return
	}
	OK(w, map[string]bool{"ok": true})
}

// handleGradeFavorites:我的收藏列表。
func (s *Server) handleGradeFavorites(w http.ResponseWriter, r *http.Request) {
	list, err := s.grades.FavoriteCourses(r.Context(), userIDFrom(r))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, list)
}

// handleGradeMyEvaluations:我的评论列表。
func (s *Server) handleGradeMyEvaluations(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT e.id, e.rating, e.comment, e.is_anonymous, e.created_at,
		       p.course_name, p.teacher_name
		FROM grade_evaluations e
		JOIN grade_course_profiles p ON p.id = e.profile_id
		WHERE e.user_id = $1 AND e.is_deleted_by_user = FALSE
		ORDER BY e.created_at DESC`, userIDFrom(r))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	defer rows.Close()
	type myEval struct {
		ID          int64   `json:"id"`
		Rating      float64 `json:"rating"`
		Comment     string  `json:"comment"`
		IsAnonymous bool    `json:"is_anonymous"`
		CreatedAt   string  `json:"created_at"`
		CourseName  string  `json:"course_name"`
		TeacherName string  `json:"teacher_name"`
	}
	out := []myEval{}
	for rows.Next() {
		var e myEval
		rows.Scan(&e.ID, &e.Rating, &e.Comment, &e.IsAnonymous, &e.CreatedAt, &e.CourseName, &e.TeacherName)
		out = append(out, e)
	}
	OK(w, out)
}

// handleGradeMyRanking:我的成绩在全站的排名。
func (s *Server) handleGradeMyRanking(w http.ResponseWriter, r *http.Request) {
	hash, ok := s.requireCampus(w, r)
	if !ok {
		return
	}
	type rankItem struct {
		CourseName  string  `json:"course_name"`
		TeacherName string  `json:"teacher_name"`
		MyScore     float64 `json:"my_score"`
		Rank        int     `json:"rank"`
		Total       int     `json:"total"`
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT p.course_name, p.teacher_name, s.total_score,
		  (SELECT COUNT(*) FROM grade_score_links l2
		   JOIN grade_user_scores s2 ON s2.id=l2.score_id
		   WHERE l2.profile_id=l.profile_id AND s2.total_score > s.total_score) + 1 AS rank,
		  (SELECT COUNT(*) FROM grade_score_links l3
		   JOIN grade_user_scores s3 ON s3.id=l3.score_id
		   WHERE l3.profile_id=l.profile_id AND s3.total_score IS NOT NULL) AS total
		FROM grade_user_scores s
		JOIN grade_score_links l ON l.score_id = s.id
		JOIN grade_course_profiles p ON p.id = l.profile_id
		WHERE s.user_hash = $1 AND s.total_score IS NOT NULL
		ORDER BY p.course_name`, hash)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	defer rows.Close()
	out := []rankItem{}
	for rows.Next() {
		var ri rankItem
		rows.Scan(&ri.CourseName, &ri.TeacherName, &ri.MyScore, &ri.Rank, &ri.Total)
		out = append(out, ri)
	}
	OK(w, out)
}

// handleAppDownloadURL:返回最新APK下载地址。
func (s *Server) handleAppDownloadURL(w http.ResponseWriter, r *http.Request) {
	url := s.setting.GetStr(r.Context(), "app_apk_url", "")
	OK(w, map[string]string{"url": url})
}
