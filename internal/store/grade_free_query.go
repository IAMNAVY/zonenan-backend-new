package store

import (
	"context"
)

// FreeQueryCount 统计用户或设备已用的免费查询次数(按去重后的课程名计)。
func (s *GradeStore) FreeQueryCount(ctx context.Context, userID int64, deviceFP string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT course_name) FROM grade_free_queries
		 WHERE user_id=$1 OR (device_fingerprint=$2 AND device_fingerprint<>'')`,
		userID, deviceFP,
	).Scan(&count)
	return count, err
}

// FreeQueryCountedFor 判断某课程名是否已对该用户/设备计过费(去重:同一门课只计一次)。
func (s *GradeStore) FreeQueryCountedFor(ctx context.Context, userID int64, deviceFP, courseName string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM grade_free_queries
		  WHERE course_name=$3
		    AND (user_id=$1 OR (device_fingerprint=$2 AND device_fingerprint<>'')))`,
		userID, deviceFP, courseName,
	).Scan(&exists)
	return exists, err
}

// RecordFreeQuery 记录一次免费查询(带课程名去重 + 发起 IP)。
func (s *GradeStore) RecordFreeQuery(ctx context.Context, userID int64, deviceFP, courseName, ip string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO grade_free_queries(user_id, device_fingerprint, course_name, ip) VALUES($1,$2,$3,$4)`,
		userID, deviceFP, courseName, ip)
	return err
}

// FreeQueryIPCountToday 统计某 IP 今日的免费查询计费次数(H3:防批量注册白嫖)。
func (s *GradeStore) FreeQueryIPCountToday(ctx context.Context, ip string) (int, error) {
	if ip == "" {
		return 0, nil
	}
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM grade_free_queries
		 WHERE ip=$1 AND queried_at >= date_trunc('day', NOW())`,
		ip,
	).Scan(&count)
	return count, err
}
