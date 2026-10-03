package httpapi

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"zonenan-backend/internal/auth"
	"zonenan-backend/internal/store"
)

const defaultDeviceChallengeTTLMinutes = 10

type deviceChallengeCredentialReq struct {
	ChallengeID string `json:"challenge_id"`
	Secret      string `json:"secret"`
}

type deviceChallengeEmailReq struct {
	ChallengeID string `json:"challenge_id"`
	Secret      string `json:"secret"`
	StudentID   string `json:"student_id"`
	Code        string `json:"code,omitempty"`
}

type deviceChallengeActionReq struct {
	ChallengeID string `json:"challenge_id"`
}

func validateChallengeCredentials(challengeID, secret string) bool {
	return len(challengeID) == 32 && len(secret) == 64
}

func (s *Server) challengeForEmail(w http.ResponseWriter, r *http.Request, req deviceChallengeEmailReq) (*store.DeviceLoginChallenge, string, bool) {
	req.ChallengeID = strings.TrimSpace(req.ChallengeID)
	req.Secret = strings.TrimSpace(req.Secret)
	req.StudentID = strings.TrimSpace(req.StudentID)
	if !validateChallengeCredentials(req.ChallengeID, req.Secret) || req.StudentID == "" {
		Fail(w, http.StatusBadRequest, "设备登录请求参数不完整")
		return nil, "", false
	}
	challenge, err := s.challenges.Authenticate(r.Context(), req.ChallengeID, req.Secret)
	if err != nil {
		s.writeChallengeError(w, err)
		return nil, "", false
	}
	studentHash := auth.StudentHash(req.StudentID, s.cfg.GradePepper)
	if challenge.StudentHash != studentHash {
		// Do not disclose whether the challenge ID or claimed account was wrong.
		Fail(w, http.StatusNotFound, store.ErrChallengeInvalid.Error())
		return nil, "", false
	}
	if status := challenge.EffectiveStatus(time.Now()); status != store.ChallengePending {
		s.writeEffectiveChallengeStatusError(w, status)
		return nil, "", false
	}
	return challenge, campusEmail(req.StudentID), true
}

func (s *Server) sendDeviceChallengeEmail(r *http.Request, email string) (int, error) {
	cooldown := s.setting.GetInt(r.Context(), "email_code_resend_seconds", defaultCodeCooldownSeconds)
	if left := cooldown - s.codes.SecondsSinceLast(r.Context(), email); left > 0 {
		return left, nil
	}
	ttl := time.Duration(s.setting.GetInt(r.Context(), "email_code_ttl_minutes", defaultCodeTTLMinutes)) * time.Minute
	code := auth.RandomCode()
	if err := s.codes.Save(r.Context(), email, code, "device_verify", ttl); err != nil {
		return 0, err
	}
	body := fmt.Sprintf("你的 ZoneNaN 新设备验证码是 %s，%d 分钟内有效。若非本人操作请忽略。", code, int(ttl.Minutes()))
	if err := s.mail.Send(r.Context(), email, "ZoneNaN 新设备验证", body); err != nil {
		log.Printf("send device-verify code to %s failed: %v", maskEmail(email), err)
		return 0, err
	}
	return 0, nil
}

// handleDeviceChallengeEmailStart explicitly starts or resends the legacy-compatible
// campus email method after the target device selects it.
func (s *Server) handleDeviceChallengeEmailStart(w http.ResponseWriter, r *http.Request) {
	var req deviceChallengeEmailReq
	if !decodeJSON(w, r, &req) {
		return
	}
	_, email, ok := s.challengeForEmail(w, r, req)
	if !ok {
		return
	}
	left, err := s.sendDeviceChallengeEmail(r, email)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "验证码发送失败,请稍后重试")
		return
	}
	if left > 0 {
		writeJSON(w, http.StatusTooManyRequests, Envelope{
			OK:      false,
			Message: fmt.Sprintf("请 %d 秒后再试", left),
			Data: map[string]interface{}{
				"email_masked": maskEmail(email),
				"resend_in":    left,
			},
		})
		return
	}
	OK(w, map[string]interface{}{"sent": true, "email_masked": maskEmail(email)})
}

func (s *Server) handleDeviceChallengeEmailVerify(w http.ResponseWriter, r *http.Request) {
	var req deviceChallengeEmailReq
	if !decodeJSON(w, r, &req) {
		return
	}
	_, email, ok := s.challengeForEmail(w, r, req)
	if !ok {
		return
	}
	if err := s.codes.Verify(r.Context(), email, strings.TrimSpace(req.Code), "device_verify"); err != nil {
		Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	studentHash := auth.StudentHash(strings.TrimSpace(req.StudentID), s.cfg.GradePepper)
	if err := s.challenges.ApproveWithSecret(r.Context(), strings.TrimSpace(req.ChallengeID), strings.TrimSpace(req.Secret), studentHash); err != nil {
		s.writeChallengeError(w, err)
		return
	}
	OK(w, map[string]string{"status": store.ChallengeApproved})
}

func (s *Server) handleDeviceChallengeStatus(w http.ResponseWriter, r *http.Request) {
	var req deviceChallengeCredentialReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ChallengeID = strings.TrimSpace(req.ChallengeID)
	req.Secret = strings.TrimSpace(req.Secret)
	if !validateChallengeCredentials(req.ChallengeID, req.Secret) {
		Fail(w, http.StatusBadRequest, "设备登录请求参数不完整")
		return
	}
	challenge, err := s.challenges.Authenticate(r.Context(), req.ChallengeID, req.Secret)
	if err != nil {
		s.writeChallengeError(w, err)
		return
	}
	OK(w, map[string]interface{}{
		"challenge_id": challenge.ID,
		"status":       challenge.EffectiveStatus(time.Now()),
		"expires_at":   challenge.ExpiresAt,
	})
}

func (s *Server) handleDeviceChallengeFinish(w http.ResponseWriter, r *http.Request) {
	var req deviceChallengeCredentialReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ChallengeID = strings.TrimSpace(req.ChallengeID)
	req.Secret = strings.TrimSpace(req.Secret)
	if !validateChallengeCredentials(req.ChallengeID, req.Secret) {
		Fail(w, http.StatusBadRequest, "设备登录请求参数不完整")
		return
	}

	challenge, err := s.challenges.Authenticate(r.Context(), req.ChallengeID, req.Secret)
	if err != nil {
		s.writeChallengeError(w, err)
		return
	}
	if s.telemetry.IsDeviceBlocked(r.Context(), challenge.TargetDeviceFingerprint) {
		Fail(w, http.StatusForbidden, "该设备已被限制")
		return
	}
	user, err := s.users.GetByStudentHash(r.Context(), challenge.StudentHash)
	if err != nil {
		Fail(w, http.StatusUnauthorized, "校园账号不存在")
		return
	}
	if user.IsBanned {
		Fail(w, http.StatusForbidden, "账号已被封禁")
		return
	}
	// Signing is side-effect free, so prepare the token before the atomic consume;
	// a signing failure must not strand an otherwise usable approved challenge.
	token, err := s.tokens.Issue(user.ID)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "令牌签发失败")
		return
	}
	if _, err := s.challenges.ConsumeAndTrust(r.Context(), req.ChallengeID, req.Secret); err != nil {
		s.writeChallengeError(w, err)
		return
	}
	s.issueForDevice(w, r, user, challenge.StudentHash, challenge.TargetDeviceFingerprint, false, false, token)
}

func (s *Server) handleListPendingDeviceChallenges(w http.ResponseWriter, r *http.Request) {
	studentHash, ok := s.requireTrustedApprover(w, r)
	if !ok {
		return
	}
	list, err := s.challenges.ListPending(r.Context(), studentHash)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	OK(w, list)
}

func (s *Server) handleApproveDeviceChallenge(w http.ResponseWriter, r *http.Request) {
	studentHash, ok := s.requireTrustedApprover(w, r)
	if !ok {
		return
	}
	var req deviceChallengeActionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.challenges.Approve(r.Context(), strings.TrimSpace(req.ChallengeID), studentHash); err != nil {
		s.writeChallengeError(w, err)
		return
	}
	// Deliberately no token: only the target device can finish with its secret.
	OK(w, map[string]string{"status": store.ChallengeApproved})
}

func (s *Server) handleRejectDeviceChallenge(w http.ResponseWriter, r *http.Request) {
	studentHash, ok := s.requireTrustedApprover(w, r)
	if !ok {
		return
	}
	var req deviceChallengeActionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.challenges.Reject(r.Context(), strings.TrimSpace(req.ChallengeID), studentHash); err != nil {
		s.writeChallengeError(w, err)
		return
	}
	OK(w, map[string]string{"status": store.ChallengeRejected})
}

func trustedApproverDeviceAllowed(deviceFP string, trusted bool) bool {
	return strings.TrimSpace(deviceFP) != "" && trusted
}

// requireTrustedApprover binds the JWT's campus identity and current device
// fingerprint to the same trusted-device row before any list/approve/reject action.
func (s *Server) requireTrustedApprover(w http.ResponseWriter, r *http.Request) (string, bool) {
	studentHash, ok := s.requireCampus(w, r)
	if !ok {
		return "", false
	}
	deviceFP := strings.TrimSpace(r.Header.Get("X-Device-Fingerprint"))
	if deviceFP == "" {
		Fail(w, http.StatusForbidden, "当前设备不是可信设备")
		return "", false
	}
	trusted, err := s.devices.IsTrusted(r.Context(), studentHash, deviceFP)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "可信设备校验失败")
		return "", false
	}
	if !trustedApproverDeviceAllowed(deviceFP, trusted) {
		Fail(w, http.StatusForbidden, "当前设备不是可信设备")
		return "", false
	}
	return studentHash, true
}

func (s *Server) writeEffectiveChallengeStatusError(w http.ResponseWriter, status string) {
	switch status {
	case "expired":
		Fail(w, http.StatusGone, store.ErrChallengeExpired.Error())
	case store.ChallengeRejected:
		Fail(w, http.StatusForbidden, store.ErrChallengeRejected.Error())
	case store.ChallengeConsumed:
		Fail(w, http.StatusConflict, store.ErrChallengeConsumed.Error())
	default:
		Fail(w, http.StatusConflict, store.ErrChallengeState.Error())
	}
}

func (s *Server) writeChallengeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrChallengeInvalid):
		Fail(w, http.StatusNotFound, store.ErrChallengeInvalid.Error())
	case errors.Is(err, store.ErrChallengeExpired):
		Fail(w, http.StatusGone, err.Error())
	case errors.Is(err, store.ErrChallengeRejected):
		Fail(w, http.StatusForbidden, err.Error())
	case errors.Is(err, store.ErrChallengePending),
		errors.Is(err, store.ErrChallengeConsumed),
		errors.Is(err, store.ErrChallengeState):
		Fail(w, http.StatusConflict, err.Error())
	default:
		Fail(w, http.StatusInternalServerError, "设备登录请求处理失败")
	}
}
