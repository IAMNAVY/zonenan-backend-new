package httpapi

import "testing"

func TestValidateChallengeCredentials(t *testing.T) {
	if !validateChallengeCredentials("0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef") {
		t.Fatal("well-formed challenge credentials were rejected")
	}
	if validateChallengeCredentials("short", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef") {
		t.Fatal("short challenge ID was accepted")
	}
	if validateChallengeCredentials("0123456789abcdef0123456789abcdef", "short") {
		t.Fatal("short challenge secret was accepted")
	}
}

func TestTrustedApproverDeviceAllowed(t *testing.T) {
	if !trustedApproverDeviceAllowed("trusted-device", true) {
		t.Fatal("trusted device was rejected")
	}
	if trustedApproverDeviceAllowed("untrusted-device", false) {
		t.Fatal("nontrusted device was accepted")
	}
	if trustedApproverDeviceAllowed("", true) {
		t.Fatal("empty fingerprint was accepted")
	}
}
