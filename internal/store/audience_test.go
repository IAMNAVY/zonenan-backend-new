package store

import "testing"

func TestAudienceViewerCanSeeBetaUsesBetaAudience(t *testing.T) {
	if !(AudienceViewer{IsBeta: true}).CanSee("beta") {
		t.Fatal("beta member cannot see beta audience")
	}
	if !(AudienceViewer{IsAdmin: true}).CanSee("beta") {
		t.Fatal("admin cannot see beta audience")
	}
	if (AudienceViewer{IsPremium: true}).CanSee("beta") {
		t.Fatal("premium entitlement unexpectedly grants beta audience")
	}
}
