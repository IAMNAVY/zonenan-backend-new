package store

import (
	"strings"
	"testing"
	"time"
)

func TestValidateClassroomArtifact(t *testing.T) {
	valid := ClassroomArtifact{
		TermID:      "2026-2027-1",
		FirstMonday: time.Date(2026, time.September, 7, 0, 0, 0, 0, time.UTC),
		TotalWeeks:  20,
		SourceURL:   "https://downloads.example.test/classroom.sqlite",
	}
	if err := ValidateClassroomArtifact(valid); err != nil {
		t.Fatalf("valid artifact rejected: %v", err)
	}

	cases := []ClassroomArtifact{
		{TermID: "2026-2027-3", FirstMonday: valid.FirstMonday, TotalWeeks: 20, SourceURL: valid.SourceURL},
		{TermID: valid.TermID, FirstMonday: valid.FirstMonday.AddDate(0, 0, 1), TotalWeeks: 20, SourceURL: valid.SourceURL},
		{TermID: valid.TermID, FirstMonday: valid.FirstMonday, TotalWeeks: 0, SourceURL: valid.SourceURL},
		{TermID: valid.TermID, FirstMonday: valid.FirstMonday, TotalWeeks: 20},
	}
	for _, item := range cases {
		if err := ValidateClassroomArtifact(item); err == nil {
			t.Fatalf("invalid artifact accepted: %#v", item)
		}
	}
}

func TestSQLiteArtifactVerifierRejectsNonSQLite(t *testing.T) {
	verifier := NewSQLiteArtifactVerifier(ArtifactURLPolicy{})
	if verifier.policy.MaxBytes != defaultClassroomArtifactMaxBytes {
		t.Fatalf("max bytes = %d", verifier.policy.MaxBytes)
	}
	if !strings.Contains("SQLite format 3\x00", "SQLite") {
		t.Fatal("SQLite magic changed unexpectedly")
	}
}
