package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestTrustedDeviceCredentialIsolation(t *testing.T) {
	m := NewTokenManager("synthetic-key", time.Hour)
	raw, err := m.IssueTrustedDevice(42, "test-device", 7)
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.ParseTrustedDevice(raw)
	if err != nil || c.UserID != 42 || c.Device != "test-device" || c.TrustID != 7 {
		t.Fatalf("claim: %v", err)
	}
	if _, err := m.Parse(raw); err == nil {
		t.Fatal("device credential authenticated ordinary API")
	}
	if _, err := m.ParseCampusClaim(raw); err == nil {
		t.Fatal("device credential authenticated restricted session")
	}
	full, _ := m.Issue(42)
	if _, err := m.ParseTrustedDevice(full); err == nil {
		t.Fatal("account JWT accepted as device credential")
	}
	if _, err := NewTokenManager("other-key", time.Hour).ParseTrustedDevice(raw); err == nil {
		t.Fatal("wrong signing key accepted")
	}
	c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	expired, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(m.trustedDeviceKey())
	if _, err := m.ParseTrustedDevice(expired); err == nil {
		t.Fatal("expired credential accepted")
	}
}
