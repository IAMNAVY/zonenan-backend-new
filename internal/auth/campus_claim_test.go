package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestCampusClaimCannotAuthenticateAccount(t *testing.T) {
	m := NewTokenManager("synthetic-test-key", time.Hour)
	token, err := m.IssueCampusClaim(42, "test-device")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := m.ParseCampusClaim(token)
	if err != nil || claim.UserID != 42 || claim.Device != "test-device" {
		t.Fatalf("claim: %v", err)
	}
	if _, err := m.Parse(token); err == nil {
		t.Fatal("restricted claim authenticated as account")
	}
	// Model the old server's parser, which does not know about new claims.
	if _, err := jwt.Parse(token, func(t *jwt.Token) (interface{}, error) { return []byte("synthetic-test-key"), nil }); err == nil {
		t.Fatal("legacy parser accepted restricted signature")
	}
	full, _ := m.Issue(42)
	if _, err := m.ParseCampusClaim(full); err == nil {
		t.Fatal("full token accepted as claim")
	}
	other := NewTokenManager("other-synthetic-key", time.Hour)
	if _, err := other.ParseCampusClaim(token); err == nil {
		t.Fatal("wrong key accepted")
	}
	expired, _ := NewTokenManager("synthetic-test-key", -time.Minute).IssueCampusClaim(42, "test-device")
	if _, err := m.ParseCampusClaim(expired); err == nil {
		t.Fatal("expired claim accepted")
	}
}
