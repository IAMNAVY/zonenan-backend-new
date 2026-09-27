package store

import (
	"context"
	"errors"
)

// ErrEditLimit 表示评教编辑次数超限(复刻旧库每人每档 ≤3 次)。
var ErrEditLimit = errors.New("编辑次数已达上限")

const maxEvalEdits = 3

// Evaluate upsert 当前用户对某 profile 的评教。首次创建;再次为编辑(edit_count+1,≤3)。
// 单条原子 UPSERT:靠 (profile_id,user_id) 唯一约束,DO UPDATE 带 edit_count<3 条件,
// 消除"读-判-写"竞态与并发首插竞态(L1)。
func (s *GradeStore) Evaluate(ctx context.Context, profileID, userID int64, rating float64, comment string, anonymous bool) error {
	ct, err := s.pool.Exec(ctx,
		`INSERT INTO grade_evaluations(profile_id, user_id, rating, comment, is_anonymous)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (profile_id, user_id) DO UPDATE
		    SET rating=EXCLUDED.rating, comment=EXCLUDED.comment,
		        is_anonymous=EXCLUDED.is_anonymous, is_deleted_by_user=FALSE,
		        edit_count=grade_evaluations.edit_count+1, updated_at=NOW()
		  WHERE grade_evaluations.edit_count < $6`,
		profileID, userID, rating, comment, anonymous, maxEvalEdits)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		// 冲突命中已存在行但 edit_count>=3,WHERE 挡下 → 未更新。
		return ErrEditLimit
	}
	return nil
}

// DeleteEvaluation 软删除当前用户自己的评教。
func (s *GradeStore) DeleteEvaluation(ctx context.Context, evaluationID, userID int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE grade_evaluations SET is_deleted_by_user=TRUE, updated_at=NOW()
		  WHERE id=$1 AND user_id=$2`,
		evaluationID, userID)
	return err
}

// UpdateEvaluation 编辑当前用户自己的评教内容。
func (s *GradeStore) UpdateEvaluation(ctx context.Context, evaluationID, userID int64, comment string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE grade_evaluations SET comment=$3, updated_at=NOW()
		  WHERE id=$1 AND user_id=$2 AND NOT is_deleted_by_user`,
		evaluationID, userID, comment)
	return err
}

// AdminEvaluation 是 admin 审核视图下的一条评教(含课程与用户信息)。
type AdminEvaluation struct {
	ID          int64   `json:"id"`
	ProfileID   int64   `json:"profile_id"`
	UserID      *int64  `json:"user_id,omitempty"`
	Rating      float64 `json:"rating"`
	Comment     string  `json:"comment"`
	IsAnonymous bool    `json:"is_anonymous"`
	IsHidden    bool    `json:"is_hidden"`
	CourseName  string  `json:"course_name"`
	TeacherName string  `json:"teacher_name"`
	CreatedAt   string  `json:"created_at"`
}

// ListEvaluationsForAdmin 分页列出评教(最新在前),仅含有评论内容的。
// onlyVisible=false 时含已隐藏项(审核用)。
func (s *GradeStore) ListEvaluationsForAdmin(ctx context.Context, page, pageSize int) ([]AdminEvaluation, error) {
	if pageSize <= 0 {
		pageSize = 30
	}
	rows, err := s.pool.Query(ctx,
		`SELECT e.id, e.profile_id, e.user_id, e.rating, e.comment, e.is_anonymous,
		        e.is_hidden, p.course_name, p.teacher_name, e.created_at::text
		   FROM grade_evaluations e
		   JOIN grade_course_profiles p ON p.id = e.profile_id
		  WHERE e.is_deleted_by_user = FALSE AND e.comment <> ''
		  ORDER BY e.created_at DESC
		  LIMIT $1 OFFSET $2`, pageSize, page*pageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminEvaluation{}
	for rows.Next() {
		var e AdminEvaluation
		if err := rows.Scan(&e.ID, &e.ProfileID, &e.UserID, &e.Rating, &e.Comment,
			&e.IsAnonymous, &e.IsHidden, &e.CourseName, &e.TeacherName, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// SetEvaluationHidden 管理员下架/恢复某条评教。
func (s *GradeStore) SetEvaluationHidden(ctx context.Context, evaluationID int64, hidden bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE grade_evaluations SET is_hidden=$2, updated_at=NOW() WHERE id=$1`,
		evaluationID, hidden)
	return err
}
