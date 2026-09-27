package store

import "testing"

func TestNormalizeNewCampusMapContribution(t *testing.T) {
	item, err := normalizeContribution(CampusMapContribution{
		Kind: " new_place ",
		ProposedPlace: &CampusMapPlace{
			CampusID: "xiaoxiang", Name: " 新地点 ", Latitude: 28.15, Longitude: 112.93,
			CoordinateSystem: "cgcs2000", Category: "teaching_research",
			Types: []string{"study_space"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.ProposedPlace == nil || item.ProposedPlace.Name != "新地点" || !item.ProposedPlace.Active {
		t.Fatalf("unexpected normalized proposal: %#v", item.ProposedPlace)
	}
}

func TestNormalizeCorrectionRequiresMessage(t *testing.T) {
	id := int64(3)
	if _, err := normalizeContribution(CampusMapContribution{Kind: "correction", PlaceID: &id}); err == nil {
		t.Fatal("correction without a message was accepted")
	}
}

func TestContributionRejectsLocalCustomCategory(t *testing.T) {
	_, err := normalizeContribution(CampusMapContribution{
		Kind: "new_place",
		ProposedPlace: &CampusMapPlace{
			CampusID: "xiaoxiang", Name: "本地其他地点", Latitude: 28.15, Longitude: 112.93,
			Category: "custom", Types: []string{"custom"},
		},
	})
	if err == nil {
		t.Fatal("local-only custom category was accepted for public review")
	}
}

func TestContributionFingerprintIgnoresFormattingAndTypeOrder(t *testing.T) {
	first, err := normalizeContribution(CampusMapContribution{
		Kind: "new_place",
		ProposedPlace: &CampusMapPlace{
			CampusID: "xiaoxiang", Name: "知新馆", Latitude: 28.15, Longitude: 112.93,
			Category: "teaching_research", Types: []string{"study_space", "teaching_research"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	second := first
	copyPlace := *first.ProposedPlace
	copyPlace.Name = "  知新馆  "
	copyPlace.Types = []string{"teaching_research", "study_space"}
	second.ProposedPlace = &copyPlace
	if contributionFingerprint(first) != contributionFingerprint(second) {
		t.Fatal("equivalent submissions produced different fingerprints")
	}
}
