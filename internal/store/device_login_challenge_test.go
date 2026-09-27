package store

import (
	"errors"
	"testing"
	"time"

	"zonenan-backend/internal/auth"
)

func testChallenge(status string, expiresAt time.Time, secret string) *DeviceLoginChallenge {
	digest := auth.OpaqueSecretHash(secret)
	return &DeviceLoginChallenge{
		StudentHash: "student-a",
		SecretHash:  digest[:],
		Status:      status,
		ExpiresAt:   expiresAt,
	}
}

func TestChallengeCredentialAndStateGuards(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	c := testChallenge(ChallengeApproved, now.Add(time.Minute), "target-secret")

	if err := authenticateChallenge(c, "target-secret"); err != nil {
		t.Fatalf("valid secret rejected: %v", err)
	}
	if err := authenticateChallenge(c, "attacker-secret"); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("wrong secret error = %v, want ErrChallengeInvalid", err)
	}
	if err := stateError(c, ChallengeApproved, now); err != nil {
		t.Fatalf("approved challenge rejected for finish: %v", err)
	}

	if !challengeBelongsToStudent(c, "student-a") {
		t.Fatal("matching campus identity was rejected")
	}
	if challengeBelongsToStudent(c, "student-b") {
		t.Fatal("cross-account challenge action was accepted")
	}

	c.Status = ChallengePending
	if err := stateError(c, ChallengeApproved, now); !errors.Is(err, ErrChallengePending) {
		t.Fatalf("pending finish error = %v", err)
	}
	c.Status = ChallengeRejected
	if err := stateError(c, ChallengeApproved, now); !errors.Is(err, ErrChallengeRejected) {
		t.Fatalf("rejected finish error = %v", err)
	}
	c.Status = ChallengeConsumed
	if err := stateError(c, ChallengeApproved, now); !errors.Is(err, ErrChallengeConsumed) {
		t.Fatalf("replay error = %v, want ErrChallengeConsumed", err)
	}
}

func TestChallengeExpiryTakesPrecedence(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	c := testChallenge(ChallengeApproved, now.Add(-time.Second), "secret")
	if err := stateError(c, ChallengeApproved, now); !errors.Is(err, ErrChallengeExpired) {
		t.Fatalf("expired state error = %v, want ErrChallengeExpired", err)
	}
	if got := c.EffectiveStatus(now); got != "expired" {
		t.Fatalf("effective status = %q, want expired", got)
	}
}
