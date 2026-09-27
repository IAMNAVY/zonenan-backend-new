package membership

import (
	"strings"
	"testing"
)

func TestBindingCodeNormalizationSurvivesOffsetRotation(t *testing.T) {
	code, err := GenerateBindingCode(42, 10007)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := NormalizeBindingCode("  " + strings.ReplaceAll(code, "-", " ") + "  ")
	if err != nil {
		t.Fatal(err)
	}
	if canonical != code {
		t.Fatalf("canonical code = %q, want %q", canonical, code)
	}
}

func TestBindingCodeRoundTrip(t *testing.T) {
	code, err := GenerateBindingCode(42, 10007)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseBindingCode(code, 10007)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != 42 || got.PublicID != 10049 || got.Version != BindingAlgorithmV1 {
		t.Fatalf("unexpected binding code: %+v", got)
	}
}

func TestBindingCodeAcceptsHumanSeparators(t *testing.T) {
	code, err := GenerateBindingCode(123, 10007)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseBindingCode("  "+code[:3]+" "+code[4:]+"  ", 10007)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != 123 {
		t.Fatalf("user id = %d", got.UserID)
	}
}

func TestBindingCodeRejectsTampering(t *testing.T) {
	code, err := GenerateBindingCode(42, 10007)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseBindingCode(code[:len(code)-1]+"8", 10007); err == nil {
		t.Fatal("tampered check digit accepted")
	}
}
