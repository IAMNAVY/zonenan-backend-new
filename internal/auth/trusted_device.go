package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type TrustedDeviceClaim struct {
	UserID  int64  `json:"uid"`
	Device  string `json:"device"`
	TrustID int64  `json:"trust_id"`
	jwt.RegisteredClaims
}

func (m *TokenManager) trustedDeviceKey() []byte {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte("zonenan/trusted-device/v1"))
	return mac.Sum(nil)
}

// Device credentials survive logout but cannot authenticate ordinary API calls.
func (m *TokenManager) IssueTrustedDevice(uid int64, device string, trustID int64) (string, error) {
	now := time.Now()
	c := TrustedDeviceClaim{UserID: uid, Device: device, TrustID: trustID, RegisteredClaims: jwt.RegisteredClaims{
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(365 * 24 * time.Hour)),
	}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(m.trustedDeviceKey())
}

func (m *TokenManager) ParseTrustedDevice(raw string) (*TrustedDeviceClaim, error) {
	c := &TrustedDeviceClaim{}
	t, err := jwt.ParseWithClaims(raw, c, func(*jwt.Token) (interface{}, error) { return m.trustedDeviceKey(), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if !t.Valid || c.UserID <= 0 || c.Device == "" || c.TrustID <= 0 {
		return nil, fmt.Errorf("invalid trusted device credential")
	}
	return c, nil
}
