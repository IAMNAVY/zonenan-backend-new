package store

import "context"

// MyScore 是"我的贡献"一行:我上传过的某门课成绩。
type MyScore struct {
	Semester     string   `json:"semester"`
	CourseName   string   `json:"course_name"`
	TeacherName  string   `json:"teacher_name"`
	RegularScore *float64 `json:"regular_score"`
	FinalScore   *float64 `json:"final_score"`
	TotalScore   *float64 `json:"total_score"`
}

// MyContributions 按 user_hash 拉出该用户上传过的成绩(经 course_key 关联到 profile 取课名/老师)。
func (s *GradeStore) MyContributions(ctx context.Context, userHash string) ([]MyScore, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT p.semester, p.course_name, p.teacher_name, s.regular_score, s.final_score, s.total_score
		   FROM grade_user_scores s
		   JOIN grade_score_links l ON l.score_id = s.id
		   JOIN grade_course_profiles p ON p.id = l.profile_id
		  WHERE s.user_hash = $1
		  ORDER BY p.semester DESC, p.course_name`,
		userHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MyScore{}
	for rows.Next() {
		var m MyScore
		if err := rows.Scan(&m.Semester, &m.CourseName, &m.TeacherName,
			&m.RegularScore, &m.FinalScore, &m.TotalScore); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
