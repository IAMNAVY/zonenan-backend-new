package store

import "testing"

func TestIncidentConfidenceUsesWeightedEvidence(t *testing.T) {
	tests := []struct {
		name                       string
		reports, confirms, rejects float64
		confirmCount               int
		want                       string
	}{
		{name: "new report remains pending", reports: 1, want: "pending"},
		{name: "independent confirmations", reports: 1, confirms: 3, confirmCount: 3, want: "confirmed"},
		{name: "strong contradiction", reports: .5, rejects: 5, confirmCount: 0, want: "low"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, got := incidentConfidence(test.reports, test.confirms, test.rejects, test.confirmCount)
			if got != test.want {
				t.Fatalf("confidence level = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidIncidentTypeRejectsUnknownValues(t *testing.T) {
	for _, kind := range incidentTypes {
		if !validIncidentType(kind) {
			t.Fatalf("known incident type %q rejected", kind)
		}
	}
	if validIncidentType("custom") {
		t.Fatal("unknown incident type accepted")
	}
}
