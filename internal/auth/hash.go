package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var wsRe = regexp.MustCompile(`\s+`)

// normalizeText 复刻旧后端:折叠连续空白为单空格并 trim。
func normalizeText(v string) string {
	return strings.TrimSpace(wsRe.ReplaceAllString(v, " "))
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// StudentHash 复刻旧后端 makeAnonymousJwUser:sha256(lower(trim(学号)) + "|" + pepper)。
// 保证与旧库 user_hash 一致 → 老学生新登即接上历史成绩。
func StudentHash(studentID, pepper string) string {
	return sha256hex(strings.ToLower(strings.TrimSpace(studentID)) + "|" + pepper)
}

// RatioSignature 复刻旧后端 ratioSignature:normalize(平时) + "|" + normalize(期末)。
// 仅用于 grade_course_profiles 的 UNIQUE 约束,不参与 course_key。
func RatioSignature(regularRatio, finalRatio string) string {
	return normalizeText(regularRatio) + "|" + normalizeText(finalRatio)
}

// CourseKey 复刻旧后端 buildCourseKey:sha256(semester|course|teacher),均 normalize。无盐、不含比例。
func CourseKey(semester, courseName, teacherName string) string {
	return sha256hex(normalizeText(semester) + "|" + normalizeText(courseName) + "|" + normalizeText(teacherName))
}

// DeviceFingerprint 复刻旧后端:sha256(deviceInfo) 取前 24 位。
func DeviceFingerprint(deviceInfo string) string {
	if deviceInfo == "" {
		return ""
	}
	return sha256hex(deviceInfo)[:24]
}
