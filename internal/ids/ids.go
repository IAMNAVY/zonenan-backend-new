// Package ids 用 IDS(wisedu 统一身份)令牌向 ca.csu.edu.cn 回验真实学号。
//
// S1 修复核心:cas-login 不能信任客户端上报的 student_id(可伪造→抢注他人学号)。
// 服务端用客户端换来的 ids_token 调一次 /mobile/userProfile,由 IDS 服务器告诉我们
// 这个 token 真正对应哪个学号。攻击者拿不到他人的 ids_token(需本人真登录一次),
// 因此无法再冒名。
//
// 仅旧客户端 /auth/cas-login 使用学校回验。每次登录都需验证凭证，
// 设备指纹不作为身份凭证。新客户端登记/验证校园邮箱不调用学校接口。
package ids

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrUnverifiable 表示无法确定该 token 对应的学号(网络失败、被拒、token 无效、
// 或 IDS 返回结构异常)。调用方据此拒绝新绑定(fail-closed),而非退回信任客户端。
var ErrUnverifiable = errors.New("ids: 无法回验学号")

// Verifier 持有 IDS 回验所需配置。
type Verifier struct {
	authServer    string
	appID         string
	appSecret     string
	casServiceURL string
	client        *http.Client
}

const defaultCASServiceURL = "http://ca.csu.edu.cn/personalInfo/personCenter/index.html"

// NewVerifier 构造回验器。保留旧构造函数供旧调用方使用。
func NewVerifier(authServer, appID, appSecret string) *Verifier {
	return NewVerifierWithCASService(authServer, appID, appSecret, defaultCASServiceURL)
}

// NewVerifierWithCASService 构造同时支持 IDS token 与 CAS service ticket 的回验器。
func NewVerifierWithCASService(authServer, appID, appSecret, casServiceURL string) *Verifier {
	return &Verifier{
		authServer:    strings.TrimRight(authServer, "/"),
		appID:         appID,
		appSecret:     appSecret,
		casServiceURL: strings.TrimSpace(casServiceURL),
		// 给学校接口留超时,避免慢响应拖垮请求;失败即 fail-closed。
		client: &http.Client{Timeout: 8 * time.Second},
	}
}

// Enabled 报告是否配置了回验(appId/secret 齐全)。未配置时调用方应保守处理。
func (v *Verifier) Enabled() bool {
	return v != nil && v.appID != "" && v.appSecret != "" && v.authServer != ""
}

// VerifyStudentID 用 ids_token 换取真实学号(userProfile.data.uid)。
// 复刻客户端 IdsOAuth.getAccount:POST form {appId, token},解析 data.uid。
func (v *Verifier) VerifyStudentID(ctx context.Context, idsToken string) (string, error) {
	if !v.Enabled() {
		return "", ErrUnverifiable
	}
	idsToken = strings.TrimSpace(idsToken)
	if idsToken == "" {
		return "", ErrUnverifiable
	}

	form := url.Values{}
	form.Set("appId", v.appID)
	form.Set("token", idsToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		v.authServer+"/mobile/userProfile", strings.NewReader(form.Encode()))
	if err != nil {
		return "", ErrUnverifiable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.client.Do(req)
	if err != nil {
		return "", ErrUnverifiable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ErrUnverifiable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", ErrUnverifiable
	}

	// 照搬客户端解析:顶层 data.uid = 学号/工号。
	var parsed struct {
		Data struct {
			UID json.RawMessage `json:"uid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", ErrUnverifiable
	}
	uid := decodeUID(parsed.Data.UID)
	if uid == "" {
		return "", ErrUnverifiable
	}
	return uid, nil
}

// VerifyCASTicket 用 CAS serviceValidate 校验一次性票据并返回真实学号。
// ticket 必须由客户端使用 CASTGC 针对配置的 CASServiceURL 新申请，不能信任
// 客户端同时上报的 student_id。
func (v *Verifier) VerifyCASTicket(ctx context.Context, ticket string) (string, error) {
	if v == nil || v.authServer == "" || strings.TrimSpace(v.casServiceURL) == "" {
		return "", ErrUnverifiable
	}
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return "", ErrUnverifiable
	}

	endpoint, err := url.Parse(v.authServer + "/serviceValidate")
	if err != nil {
		return "", ErrUnverifiable
	}
	query := endpoint.Query()
	query.Set("service", v.casServiceURL)
	query.Set("ticket", ticket)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", ErrUnverifiable
	}
	req.Header.Set("Accept", "application/xml")

	resp, err := v.client.Do(req)
	if err != nil {
		return "", ErrUnverifiable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ErrUnverifiable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", ErrUnverifiable
	}

	var parsed struct {
		AuthenticationSuccess *struct {
			User string `xml:"user"`
		} `xml:"authenticationSuccess"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil || parsed.AuthenticationSuccess == nil {
		return "", ErrUnverifiable
	}
	uid := strings.TrimSpace(parsed.AuthenticationSuccess.User)
	if uid == "" {
		return "", ErrUnverifiable
	}
	return uid, nil
}

// Sign 复刻客户端签名 sign = base64(md5(appId + code + secret)),供换 token 用(备用)。
func (v *Verifier) Sign(code string) string {
	sum := md5.Sum([]byte(v.appID + code + v.appSecret))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// decodeUID 兼容 uid 是字符串或数字两种 JSON 形态。
func decodeUID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return strings.TrimSpace(n.String())
	}
	return strings.TrimSpace(strings.Trim(fmt.Sprintf("%s", raw), `"`))
}
