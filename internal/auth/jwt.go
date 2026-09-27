package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims 是 Zonenan 会话令牌载荷。只放 user_id;学号 hash 不进 token(客户端不该持有)。
type Claims struct {
	UserID    int64  `json:"uid"`
	SessionID string `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

// TokenManager 签发/校验 Zonenan 会话 JWT。
type TokenManager struct {
	key    []byte
	expire time.Duration
}

func NewTokenManager(signingKey string, expire time.Duration) *TokenManager {
	return &TokenManager{key: []byte(signingKey), expire: expire}
}

// Issue 为指定用户签发会话令牌。
func (m *TokenManager) Issue(userID int64) (string, error) {
	return m.IssueSession(userID, "")
}

func (m *TokenManager) IssueSession(userID int64, sessionID string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.expire)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(m.key)
}

// Parse 校验令牌签名与有效期,返回 user_id。
func (m *TokenManager) Parse(tokenStr string) (int64, error) {
	uid, _, err := m.ParseSession(tokenStr)
	return uid, err
}

func (m *TokenManager) ParseSession(tokenStr string) (int64, string, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.key, nil
	})
	if err != nil {
		return 0, "", err
	}
	if !tok.Valid || claims.UserID <= 0 {
		return 0, "", fmt.Errorf("invalid token")
	}
	return claims.UserID, claims.SessionID, nil
}
