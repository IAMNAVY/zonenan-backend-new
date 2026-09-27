package store

import "context"

// FavoriteAdd 添加收藏。
func (s *GradeStore) FavoriteAdd(ctx context.Context, userID, profileID int64) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO grade_favorites(user_id, profile_id) VALUES($1,$2) ON CONFLICT DO NOTHING`,
		userID, profileID)
	return err
}

// FavoriteRemove 取消收藏。
func (s *GradeStore) FavoriteRemove(ctx context.Context, userID, profileID int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM grade_favorites WHERE user_id=$1 AND profile_id=$2`,
		userID, profileID)
	return err
}

// FavoriteCourses 我的收藏列表。
func (s *GradeStore) FavoriteCourses(ctx context.Context, userID int64) ([]CourseAverage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.course_name, p.teacher_name, p.regular_ratio, p.final_ratio, p.semester
		FROM grade_favorites f
		JOIN grade_course_profiles p ON p.id = f.profile_id
		WHERE f.user_id = $1
		ORDER BY f.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CourseAverage{}
	for rows.Next() {
		var c CourseAverage
		rows.Scan(&c.ID, &c.CourseName, &c.TeacherName, &c.RegularRatio, &c.FinalRatio, &c.Semester)
		out = append(out, c)
	}
	return out, rows.Err()
}

// IsFavorited 批量查询是否已收藏。
func (s *GradeStore) IsFavorited(ctx context.Context, userID int64, profileIDs []int64) (map[int64]bool, error) {
	if len(profileIDs) == 0 {
		return map[int64]bool{}, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT profile_id FROM grade_favorites WHERE user_id=$1 AND profile_id=ANY($2)`,
		userID, profileIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[int64]bool, len(profileIDs))
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		m[id] = true
	}
	return m, rows.Err()
}
