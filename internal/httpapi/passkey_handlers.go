package httpapi

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"zonenan-backend/internal/store"
)

// WebAuthn Relying Party 配置(由环境/配置驱动)。
const rpName = "ZoneNaN"

// passkeyChallenge 在内存中暂存注册/认证 challenge(5 分钟过期)。
type passkeyChallenge struct {
	challenge []byte
	userID    int64
	expiresAt time.Time
	rpID      string
	origin    string
	isWeb     bool
}

var (
	challengesMu sync.Mutex
	challenges   = map[string]*passkeyChallenge{}
)

func (s *Server) passkeyProfile(r *http.Request) (string, string, bool) {
	origin := strings.TrimRight(r.Header.Get("Origin"), "/")
	for _, allowed := range s.cfg.WebAllowedOrigins {
		if origin == allowed {
			return s.cfg.PasskeyRPID, origin, true
		}
	}
	return s.cfg.PasskeyRPID, "", false
}

func validateWebClientData(encoded []byte, ch *passkeyChallenge, kind string) bool {
	if !ch.isWeb {
		return true
	}
	var data struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Origin    string `json:"origin"`
	}
	if json.Unmarshal(encoded, &data) != nil {
		return false
	}
	return data.Type == kind && data.Challenge == b64Encode(ch.challenge) && strings.TrimRight(data.Origin, "/") == ch.origin
}

func storeChallenge(key string, ch *passkeyChallenge) {
	challengesMu.Lock()
	challenges[key] = ch
	challengesMu.Unlock()
}

func popChallenge(key string) *passkeyChallenge {
	challengesMu.Lock()
	ch := challenges[key]
	delete(challenges, key)
	challengesMu.Unlock()
	if ch != nil && time.Now().After(ch.expiresAt) {
		return nil
	}
	return ch
}

func genChallenge() ([]byte, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return b, err
}

func b64Encode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func b64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// handlePasskeyRegisterBegin:生成注册 challenge,返回 PublicKeyCredentialCreationOptions。
func (s *Server) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	rpID, origin, isWeb := s.passkeyProfile(r)
	challenge, err := genChallenge()
	if err != nil {
		Fail(w, http.StatusInternalServerError, "生成 challenge 失败")
		return
	}
	key := b64Encode(challenge)
	storeChallenge(key, &passkeyChallenge{
		challenge: challenge,
		userID:    uid,
		expiresAt: time.Now().Add(5 * time.Minute),
		rpID:      rpID, origin: origin, isWeb: isWeb,
	})

	user, _ := s.users.GetByID(r.Context(), uid)
	nickname := "ZoneNaN User"
	if user != nil && user.Nickname != "" {
		nickname = user.Nickname
	}

	existing, _ := s.passkeys.ListByUser(r.Context(), uid)
	excludeList := make([]map[string]interface{}, 0, len(existing))
	for _, c := range existing {
		excludeList = append(excludeList, map[string]interface{}{
			"type": "public-key",
			"id":   b64Encode(c.CredentialID),
		})
	}

	OK(w, map[string]interface{}{
		"challenge": b64Encode(challenge),
		"rp":        map[string]string{"id": rpID, "name": rpName},
		"user": map[string]interface{}{
			"id":          b64Encode(big.NewInt(uid).Bytes()),
			"name":        nickname,
			"displayName": nickname,
		},
		"pubKeyCredParams": []map[string]interface{}{
			{"type": "public-key", "alg": -7},
		},
		"timeout":            60000,
		"attestation":        "none",
		"excludeCredentials": excludeList,
		"authenticatorSelection": map[string]interface{}{
			"authenticatorAttachment": "platform",
			"residentKey":             "preferred",
			"userVerification":        "preferred",
		},
	})
}

type passkeyRegFinishReq struct {
	Challenge      string `json:"challenge"`
	CredentialID   string `json:"credential_id"`
	AttestationObj string `json:"attestation_object"`
	ClientDataJSON string `json:"client_data_json"`
	PublicKeyDER   string `json:"public_key_der"`
	Name           string `json:"name"`
}

// handlePasskeyRegisterFinish:验证注册响应,保存公钥凭证。
func (s *Server) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	uid := userIDFrom(r)
	var req passkeyRegFinishReq
	if !decodeJSON(w, r, &req) {
		return
	}

	ch := popChallenge(req.Challenge)
	if ch == nil || ch.userID != uid {
		Fail(w, http.StatusBadRequest, "challenge 无效或已过期")
		return
	}
	clientData, err := b64Decode(req.ClientDataJSON)
	if err != nil || !validateWebClientData(clientData, ch, "webauthn.create") {
		Fail(w, http.StatusBadRequest, "WebAuthn 来源校验失败")
		return
	}

	credID, err := b64Decode(req.CredentialID)
	if err != nil || len(credID) == 0 {
		Fail(w, http.StatusBadRequest, "credential_id 无效")
		return
	}

	pubKeyBytes, err := b64Decode(req.PublicKeyDER)
	if err != nil || len(pubKeyBytes) == 0 {
		Fail(w, http.StatusBadRequest, "public_key 无效")
		return
	}

	name := req.Name
	if name == "" {
		name = "Passkey"
	}

	cred := &store.PasskeyCredential{
		UserID:       uid,
		CredentialID: credID,
		PublicKey:    pubKeyBytes,
		SignCount:    0,
		Name:         name,
		// pgx 会把 nil 切片编码成 SQL NULL,撞上 NOT NULL 约束导致 500;
		// 空值显式给非 nil 的空切片。
		AAGUID:     []byte{},
		Transports: []string{},
	}
	if err := s.passkeys.Create(r.Context(), cred); err != nil {
		Fail(w, http.StatusInternalServerError, "保存凭证失败")
		return
	}
	OK(w, map[string]bool{"registered": true})
}

// handlePasskeyLoginBegin:生成认证 challenge。无需登录(allowCredentials 为空=discoverable)。
func (s *Server) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	rpID, origin, isWeb := s.passkeyProfile(r)
	challenge, err := genChallenge()
	if err != nil {
		Fail(w, http.StatusInternalServerError, "生成 challenge 失败")
		return
	}
	key := b64Encode(challenge)
	storeChallenge(key, &passkeyChallenge{
		challenge: challenge,
		userID:    0,
		expiresAt: time.Now().Add(5 * time.Minute),
		rpID:      rpID, origin: origin, isWeb: isWeb,
	})

	OK(w, map[string]interface{}{
		"challenge":        b64Encode(challenge),
		"rpId":             rpID,
		"timeout":          60000,
		"userVerification": "preferred",
	})
}

type passkeyLoginFinishReq struct {
	Challenge         string `json:"challenge"`
	CredentialID      string `json:"credential_id"`
	AuthenticatorData string `json:"authenticator_data"`
	ClientDataJSON    string `json:"client_data_json"`
	Signature         string `json:"signature"`
	WebSession        bool   `json:"web_session,omitempty"`
	RememberMe        bool   `json:"remember_me,omitempty"`
	DeviceName        string `json:"device_name,omitempty"`
}

// handlePasskeyLoginFinish:验证签名,返回 JWT。
func (s *Server) handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	var req passkeyLoginFinishReq
	if !decodeJSON(w, r, &req) {
		return
	}

	ch := popChallenge(req.Challenge)
	if ch == nil {
		Fail(w, http.StatusBadRequest, "challenge 无效或已过期")
		return
	}

	credID, err := b64Decode(req.CredentialID)
	if err != nil {
		Fail(w, http.StatusBadRequest, "credential_id 无效")
		return
	}

	cred, err := s.passkeys.FindByCredentialID(r.Context(), credID)
	if err != nil {
		Fail(w, http.StatusUnauthorized, "未注册的 passkey")
		return
	}

	authData, err := b64Decode(req.AuthenticatorData)
	if err != nil {
		Fail(w, http.StatusBadRequest, "authenticator_data 无效")
		return
	}
	clientDataJSON, err := b64Decode(req.ClientDataJSON)
	if err != nil {
		Fail(w, http.StatusBadRequest, "client_data_json 无效")
		return
	}
	if !validateWebClientData(clientDataJSON, ch, "webauthn.get") || len(authData) < 32 {
		Fail(w, http.StatusBadRequest, "WebAuthn 来源校验失败")
		return
	}
	rpHash := sha256.Sum256([]byte(ch.rpID))
	if ch.isWeb && subtle.ConstantTimeCompare(authData[:32], rpHash[:]) != 1 {
		Fail(w, http.StatusBadRequest, "WebAuthn RP 校验失败")
		return
	}
	sig, err := b64Decode(req.Signature)
	if err != nil {
		Fail(w, http.StatusBadRequest, "signature 无效")
		return
	}

	clientDataHash := sha256.Sum256(clientDataJSON)
	verifyData := append(authData, clientDataHash[:]...)
	hash := sha256.Sum256(verifyData)

	pubKey, err := x509.ParsePKIXPublicKey(cred.PublicKey)
	if err != nil {
		Fail(w, http.StatusInternalServerError, "公钥解析失败")
		return
	}
	ecKey, ok := pubKey.(*ecdsa.PublicKey)
	if !ok {
		Fail(w, http.StatusInternalServerError, "不支持的密钥类型")
		return
	}

	if !ecdsa.VerifyASN1(ecKey, hash[:], sig) {
		Fail(w, http.StatusUnauthorized, "签名验证失败")
		return
	}

	_ = s.passkeys.UpdateSignCount(r.Context(), credID, cred.SignCount+1)

	user, err := s.users.GetByID(r.Context(), cred.UserID)
	if err != nil {
		Fail(w, http.StatusUnauthorized, "账号不存在")
		return
	}
	if user.IsBanned {
		Fail(w, http.StatusForbidden, "账号已被封禁")
		return
	}
	if req.WebSession {
		s.issueWebFor(w, r, user, req.RememberMe, req.DeviceName)
		return
	}
	s.issueFor(w, user, false)
}

// handlePasskeyList:列出当前用户的 passkey 凭证。
func (s *Server) handlePasskeyList(w http.ResponseWriter, r *http.Request) {
	list, err := s.passkeys.ListByUser(r.Context(), userIDFrom(r))
	if err != nil {
		Fail(w, http.StatusInternalServerError, "查询失败")
		return
	}
	type item struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	out := make([]item, len(list))
	for i, c := range list {
		out[i] = item{ID: c.ID, Name: c.Name}
	}
	OK(w, out)
}

// handlePasskeyDelete:删除 passkey 凭证。
func (s *Server) handlePasskeyDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.passkeys.Delete(r.Context(), req.ID, userIDFrom(r)); err != nil {
		Fail(w, http.StatusInternalServerError, "删除失败")
		return
	}
	OK(w, map[string]bool{"deleted": true})
}

// handleAssetLinks: 返回 Digital Asset Links JSON (Android passkey 验证)。
// SHA256 指纹从 ANDROID_CERT_SHA256 环境变量读取(冒号分隔格式)。
func (s *Server) handleAssetLinks(w http.ResponseWriter, r *http.Request) {
	sha := os.Getenv("ANDROID_CERT_SHA256")
	if sha == "" {
		sha = "00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00:00"
	}
	payload := []map[string]interface{}{
		{
			"relation": []string{
				"delegate_permission/common.handle_all_urls",
				"delegate_permission/common.get_login_creds",
			},
			"target": map[string]interface{}{
				"namespace":                "android_app",
				"package_name":             "cn.skina.zonenan",
				"sha256_cert_fingerprints": []string{sha},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
