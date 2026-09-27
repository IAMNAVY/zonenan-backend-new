package store

import (
	"context"
	"time"
)

// Evaluation 是一条评教。
type Evaluation struct {
	ID          int64     `json:"id"`
	Rating      float64   `json:"rating"`
	Comment     string    `json:"comment"`
	IsAnonymous bool      `json:"is_anonymous"`
	IsMine      bool      `json:"is_mine"`
	CreatedAt   time.Time `json:"created_at"`
}

// CourseDetail 是详情页数据:同名同师(跨比例签名)聚合分布 + 评分 + 评教分页。
type CourseDetail struct {
	CourseName      string            `json:"course_name"`
	TeacherName     string            `json:"teacher_name"`
	Semester        string            `json:"semester"`
	TotalSamples    int               `json:"total_samples"`
	Distribution    ScoreDistribution `json:"score_distribution"`
	AvgRating       *float64          `json:"avg_rating"`
	RatingCount     int               `json:"rating_count"`
	HasMyScore      bool              `json:"has_my_score"`
	Evaluations     []Evaluation      `json:"evaluations"`
	EvaluationTotal int               `json:"evaluation_total"`
}

// Detail 复刻旧 getGradeCourseDetail:以 courseId 为种子取同名同师的所有 profile,聚合分布+评教。
func (s *GradeStore) Detail(ctx context.Context, courseID int64, semester string, userID int64, userHash string, page, pageSize int) (*CourseDetail, error) {
	var courseName, teacherName string
	if err := s.pool.QueryRow(ctx,
		`SELECT course_name, teacher_name FROM grade_course_profiles WHERE id=$1`, courseID,
	).Scan(&courseName, &teacherName); err != nil {
		return nil, err
	}

	// 同名同师(可跨学期/比例)的 profile 集合;若指定 semester 则限定该学期。
	rows, err := s.pool.Query(ctx,
		`SELECT id FROM grade_course_profiles
		  WHERE course_name=$1 AND teacher_name=$2 AND ($3='' OR semester=$3)`,
		courseName, teacherName, semester)
	if err != nil {
		return nil, err
	}
	var profileIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		profileIDs = append(profileIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	d := &CourseDetail{CourseName: courseName, TeacherName: teacherName, Semester: semester, Evaluations: []Evaluation{}}
	if len(profileIDs) == 0 {
		return d, nil
	}

	dd := &d.Distribution
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(s.total_score),
		   COUNT(CASE WHEN s.total_score>=0  AND s.total_score<60  THEN 1 END),
		   COUNT(CASE WHEN s.total_score>=60 AND s.total_score<70  THEN 1 END),
		   COUNT(CASE WHEN s.total_score>=70 AND s.total_score<80  THEN 1 END),
		   COUNT(CASE WHEN s.total_score>=80 AND s.total_score<90  THEN 1 END),
		   COUNT(CASE WHEN s.total_score>=90 AND s.total_score<=100 THEN 1 END)
		 FROM grade_score_links l JOIN grade_user_scores s ON s.id=l.score_id
		 WHERE l.profile_id = ANY($1) AND s.total_score IS NOT NULL`,
		profileIDs,
	).Scan(&d.TotalSamples, &dd.B0059, &dd.B6069, &dd.B7079, &dd.B8089, &dd.B90100); err != nil {
		return nil, err
	}

	_ = s.pool.QueryRow(ctx,
		`SELECT ROUND(AVG(rating)::numeric,1), COUNT(*) FROM grade_evaluations
		  WHERE profile_id = ANY($1) AND is_hidden=FALSE AND is_deleted_by_user=FALSE`,
		profileIDs,
	).Scan(&d.AvgRating, &d.RatingCount)

	if userHash != "" {
		var cnt int
		_ = s.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM grade_score_links l
			  JOIN grade_user_scores s ON s.id=l.score_id
			 WHERE l.profile_id = ANY($1) AND s.user_hash=$2`,
			profileIDs, userHash).Scan(&cnt)
		d.HasMyScore = cnt > 0
	}

	if err := s.loadEvaluations(ctx, d, profileIDs, userID, page, pageSize); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *GradeStore) loadEvaluations(ctx context.Context, d *CourseDetail, profileIDs []int64, userID int64, page, pageSize int) error {
	if pageSize <= 0 {
		pageSize = 10
	}
	if page <= 0 {
		page = 1
	}
	_ = s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM grade_evaluations
		  WHERE profile_id = ANY($1) AND is_hidden=FALSE AND is_deleted_by_user=FALSE`,
		profileIDs).Scan(&d.EvaluationTotal)

	rows, err := s.pool.Query(ctx,
		`SELECT id, rating, comment, is_anonymous, (user_id=$4) AS is_mine, created_at
		   FROM grade_evaluations
		  WHERE profile_id = ANY($1) AND is_hidden=FALSE AND is_deleted_by_user=FALSE
		  ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		profileIDs, pageSize, (page-1)*pageSize, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e Evaluation
		if err := rows.Scan(&e.ID, &e.Rating, &e.Comment, &e.IsAnonymous, &e.IsMine, &e.CreatedAt); err != nil {
			return err
		}
		d.Evaluations = append(d.Evaluations, e)
	}
	return rows.Err()
}

func maskStudentID(sid string) string {
	runes := []rune(sid)
	if len(runes) <= 2 {
		return sid
	}
	masked := make([]rune, len(runes))
	masked[0] = runes[0]
	masked[len(runes)-1] = runes[len(runes)-1]
	for i := 1; i < len(runes)-1; i++ {
		masked[i] = '*'
	}
	return string(masked)
}
