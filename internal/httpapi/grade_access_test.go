package httpapi

import (
	"testing"
	"time"

	"zonenan-backend/internal/store"
)

func TestUnlimitedGradePath(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-29 * 24 * time.Hour)
	expired := now.Add(-31 * 24 * time.Hour)

	tests := []struct {
		name       string
		status     *store.AuthStatus
		privileged bool
		want       string
	}{
		{
			name:       "admin without consent remains privileged",
			status:     &store.AuthStatus{ConsentStatus: "none"},
			privileged: true,
			want:       "privileged",
		},
		{
			name:   "active contributor uses normal path",
			status: &store.AuthStatus{ConsentStatus: "active", CanQuery: true},
			want:   "normal",
		},
		{
			name: "recent contribution remains in grace period",
			status: &store.AuthStatus{
				ConsentStatus: "active",
				LastSyncAt:    &recent,
			},
			want: "grace",
		},
		{
			name: "expired contribution returns to free tier",
			status: &store.AuthStatus{
				ConsentStatus: "active",
				LastSyncAt:    &expired,
			},
		},
		{
			name:   "revoked consent returns to free tier",
			status: &store.AuthStatus{ConsentStatus: "revoked", LastSyncAt: &recent},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unlimitedGradePath(tt.status, tt.privileged, 30, now)
			if got != tt.want {
				t.Fatalf("unlimitedGradePath() = %q, want %q", got, tt.want)
			}
		})
	}
}
