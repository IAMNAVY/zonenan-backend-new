package store

import (
	"context"
	"fmt"
)

// ScoreDistribution 是 5 档总分分布。
type ScoreDistribution struct {
	B0059  int `json:"0-59"`
	B6069  int `json:"60-69"`
	B7079  int `json:"70-79"`
	B8089  int `json:"80-89"`
	B90100 int `json:"90-100"`
}

// CourseAverage 是搜索结果一行:同名同师课程的聚合。
type CourseAverage struct {
	ID                int64             `json:"id"`
	CourseName        string            `json:"course_name"`
	TeacherName       string            `json:"teacher_name"`
	RegularRatio      string            `json:"regular_ratio"`
	FinalRatio        string            `json:"final_ratio"`
	Semester          string            `json:"semester"`
	RegularAvg        *float64          `json:"regular_avg"`
	FinalAvg          *float64          `json:"final_avg"`
	TotalAvg          *float64          `json:"total_avg"`
	Median            *float64          `json:"median"`
	TotalSamples      int               `json:"total_samples"`
	ScoreDistribution ScoreDistribution `json:"score_distribution"`
}

// SearchParams 搜索参数。
type SearchParams struct {
	Keyword  string
	Teacher  string
	Semester string
	Sort     string // "samples"(default), "avg", "median"
	Page     int
	PageSize int
}

func (p *SearchParams) orderClause() string {
	switch p.Sort {
	case "avg":
		return "total_avg DESC NULLS LAST, total_samples DESC"
	case "median":
		return "median DESC NULLS LAST, total_samples DESC"
	default:
		return "total_samples DESC, total_avg DESC NULLS LAST"
	}
}

// Search 按课名/老师/学期搜索,同名同师聚合(跨学期),5档分布+中位数+排序+分页。
func (s *GradeStore) Search(ctx context.Context, p SearchParams) ([]CourseAverage, error) {
	if p.PageSize <= 0 {
		p.PageSize = 10
	}
	offset := p.Page * p.PageSize
	query := fmt.Sprintf(`
		SELECT MIN(pr.id) AS id, pr.course_name, pr.teacher_name,
		  MODE() WITHIN GROUP (ORDER BY pr.regular_ratio) AS regular_ratio,
		  MODE() WITHIN GROUP (ORDER BY pr.final_ratio) AS final_ratio,
		  CASE WHEN $1='' THEN '' ELSE $1 END AS semester,
		  ROUND(AVG(s.regular_score)::numeric, 2) AS regular_avg,
		  ROUND(AVG(s.final_score)::numeric, 2) AS final_avg,
		  ROUND(AVG(s.total_score)::numeric, 2) AS total_avg,
		  PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY s.total_score) AS median,
		  COUNT(s.total_score) AS total_samples,
		  COUNT(CASE WHEN s.total_score>=0  AND s.total_score<60  THEN 1 END) AS b0,
		  COUNT(CASE WHEN s.total_score>=60 AND s.total_score<70  THEN 1 END) AS b1,
		  COUNT(CASE WHEN s.total_score>=70 AND s.total_score<80  THEN 1 END) AS b2,
		  COUNT(CASE WHEN s.total_score>=80 AND s.total_score<90  THEN 1 END) AS b3,
		  COUNT(CASE WHEN s.total_score>=90 AND s.total_score<=100 THEN 1 END) AS b4
		FROM grade_course_profiles pr
		JOIN grade_score_links l ON l.profile_id = pr.id
		JOIN grade_user_scores s ON s.id = l.score_id
		WHERE ($1='' OR pr.semester=$1)
		  AND ($2='' OR pr.course_name ILIKE '%%'||$2||'%%')
		  AND ($3='' OR pr.teacher_name ILIKE '%%'||$3||'%%')
		  AND s.total_score IS NOT NULL
		GROUP BY pr.course_name, pr.teacher_name
		ORDER BY %s
		LIMIT $4 OFFSET $5`, p.orderClause())

	rows, err := s.pool.Query(ctx, query, p.Semester, p.Keyword, p.Teacher, p.PageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CourseAverage{}
	for rows.Next() {
		var c CourseAverage
		d := &c.ScoreDistribution
		if err := rows.Scan(&c.ID, &c.CourseName, &c.TeacherName,
			&c.RegularRatio, &c.FinalRatio, &c.Semester,
			&c.RegularAvg, &c.FinalAvg, &c.TotalAvg, &c.Median,
			&c.TotalSamples,
			&d.B0059, &d.B6069, &d.B7079, &d.B8089, &d.B90100); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Semesters 返回有数据的学期列表(降序)。
func (s *GradeStore) Semesters(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT semester FROM grade_course_profiles WHERE semester<>'' ORDER BY semester DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var sem string
		if err := rows.Scan(&sem); err != nil {
			return nil, err
		}
		out = append(out, sem)
	}
	return out, rows.Err()
}

// Suggest 按关键词返回课程名候选(去重)。
func (s *GradeStore) Suggest(ctx context.Context, keyword string, limit int) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT course_name FROM grade_course_profiles
		  WHERE $1<>'' AND course_name ILIKE '%'||$1||'%'
		  ORDER BY course_name LIMIT $2`,
		keyword, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
