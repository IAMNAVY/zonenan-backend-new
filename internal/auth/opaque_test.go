package auth

import "testing"

func TestRandomOpaqueTokenAndHash(t *testing.T) {
	a, err := RandomOpaqueToken(32)
	if err != nil {
		t.Fatalf("RandomOpaqueToken: %v", err)
	}
	b, err := RandomOpaqueToken(32)
	if err != nil {
		t.Fatalf("RandomOpaqueToken: %v", err)
	}
	if len(a) != 64 || len(b) != 64 {
		t.Fatalf("encoded token lengths = %d, %d; want 64", len(a), len(b))
	}
	if a == b {
		t.Fatal("independent random tokens matched")
	}
	digest := OpaqueSecretHash(a)
	if !OpaqueSecretMatches(a, digest[:]) {
		t.Fatal("matching secret was rejected")
	}
	if OpaqueSecretMatches(b, digest[:]) {
		t.Fatal("different secret was accepted")
	}
	if OpaqueSecretMatches(a, digest[:len(digest)-1]) {
		t.Fatal("wrong-length digest was accepted")
	}
}

func TestRandomOpaqueTokenRejectsInvalidLength(t *testing.T) {
	if _, err := RandomOpaqueToken(0); err == nil {
		t.Fatal("zero-length token did not fail")
	}
}
