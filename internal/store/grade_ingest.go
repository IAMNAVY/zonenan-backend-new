package store

import (
	"context"

	"zonenan-backend/internal/auth"
)

// GradeRecord 是客户端抓取并上传的一条成绩(已 join 老师)。
type GradeRecord struct {
	Semester     string   `json:"semester"`
	CourseName   string   `json:"course_name"`
	TeacherName  string   `json:"teacher_name"`
	RegularRatio string   `json:"regular_ratio"`
	FinalRatio   string   `json:"final_ratio"`
	RegularScore *float64 `json:"regular_score"`
	FinalScore   *float64 `json:"final_score"`
	TotalScore   *float64 `json:"total_score"`
}

// SaveFetched 在事务内 upsert 档案/成绩/关联,并更新同步时间戳。
// userHash 由调用方用 pepper 算好(服务端)。full=true 表示本次是全学期同步。
func (s *GradeStore) SaveFetched(ctx context.Context, userID int64, userHash string, records []GradeRecord, full bool) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	synced := 0
	for _, rec := range records {
		ratioSig := auth.RatioSignature(rec.RegularRatio, rec.FinalRatio)
		courseKey := auth.CourseKey(rec.Semester, rec.CourseName, rec.TeacherName)

		var profileID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO grade_course_profiles(semester, course_name, teacher_name, regular_ratio, final_ratio, ratio_signature, updated_at)
			 VALUES ($1,$2,$3,$4,$5,$6,NOW())
			 ON CONFLICT (semester, course_name, teacher_name, ratio_signature)
			 DO UPDATE SET regular_ratio=EXCLUDED.regular_ratio, final_ratio=EXCLUDED.final_ratio, updated_at=NOW()
			 RETURNING id`,
			rec.Semester, rec.CourseName, rec.TeacherName, rec.RegularRatio, rec.FinalRatio, ratioSig,
		).Scan(&profileID); err != nil {
			return 0, err
		}

		var scoreID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO grade_user_scores(user_hash, course_key, regular_score, final_score, total_score, updated_at)
			 VALUES ($1,$2,$3,$4,$5,NOW())
			 ON CONFLICT (user_hash, course_key)
			 DO UPDATE SET regular_score=EXCLUDED.regular_score, final_score=EXCLUDED.final_score,
			               total_score=EXCLUDED.total_score, updated_at=NOW()
			 RETURNING id`,
			userHash, courseKey, rec.RegularScore, rec.FinalScore, rec.TotalScore,
		).Scan(&scoreID); err != nil {
			return 0, err
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO grade_score_links(profile_id, score_id) VALUES ($1,$2)
			 ON CONFLICT (profile_id, score_id) DO NOTHING`,
			profileID, scoreID); err != nil {
			return 0, err
		}
		synced++
	}

	// 更新同步时间戳(授权行必存,因为 sync 前必先 consent)。
	// S2:仅在确有记录落库时才更新 last_sync_at,使其真正等价于"至少贡献过 1 条"。
	// 这样 CanQuery(consent=active AND last_sync_at≠nil)不会被空/零结果同步骗过。
	if synced > 0 {
		if full {
			if _, err := tx.Exec(ctx,
				`UPDATE grade_authorizations SET last_sync_at=NOW(), last_full_sync_at=NOW() WHERE user_id=$1`,
				userID); err != nil {
				return 0, err
			}
		} else {
			if _, err := tx.Exec(ctx,
				`UPDATE grade_authorizations SET last_sync_at=NOW() WHERE user_id=$1`,
				userID); err != nil {
				return 0, err
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return synced, nil
}
