package store

import "testing"

func TestCampusMapDataVersion(t *testing.T) {
	first := campusMapDataVersion(1)
	if len(first) != 64 {
		t.Fatalf("expected SHA-256 hex string, got %q", first)
	}
	if first != campusMapDataVersion(1) {
		t.Fatal("same revision must produce the same data version")
	}
	if first == campusMapDataVersion(2) {
		t.Fatal("different revisions must produce different data versions")
	}
}

func TestNormalizeCampusMapPlace(t *testing.T) {
	place := NormalizeCampusMapPlace(CampusMapPlace{
		CampusID:         " xiaoxiang ",
		Name:             " 图书馆 ",
		CoordinateSystem: "cgcs2000",
		Type:             " Library ",
		Aliases:          []string{" 新馆 ", "", "新馆"},
	})
	if place.CampusID != "xiaoxiang" || place.Name != "图书馆" {
		t.Fatalf("unexpected normalized place: %#v", place)
	}
	if place.CoordinateSystem != "CGCS2000" || place.Type != "library" {
		t.Fatalf("unexpected normalized coordinate/type: %#v", place)
	}
	if place.Category != "teaching_research" || len(place.Types) != 1 || place.Types[0] != "library" {
		t.Fatalf("legacy type was not migrated in memory: %#v", place)
	}
	if len(place.Aliases) != 1 || place.Aliases[0] != "新馆" {
		t.Fatalf("unexpected aliases: %#v", place.Aliases)
	}
}

func TestValidateCampusMapPlaceMultipleTypes(t *testing.T) {
	place := CampusMapPlace{
		CampusID:         "xiaoxiang",
		Name:             "知新馆",
		Latitude:         28.1531,
		Longitude:        112.9390,
		CoordinateSystem: "CGCS2000",
		Category:         "teaching_research",
		Types:            []string{"study_space", "teaching_research"},
	}
	if err := ValidateCampusMapPlace(place); err != nil {
		t.Fatalf("valid multi-type place rejected: %v", err)
	}
	place.Types = []string{"parking"}
	if err := ValidateCampusMapPlace(place); err == nil {
		t.Fatal("cross-category subtype must be rejected")
	}
}

func TestNormalizeCampusMapPlaceRetiredTypes(t *testing.T) {
	place := NormalizeCampusMapPlace(CampusMapPlace{
		Category: "transportation",
		Types:    []string{"road", "shuttle_stop", "shuttle_route"},
	})
	if len(place.Types) != 2 || place.Types[0] != "transportation" || place.Types[1] != "campus_bus" {
		t.Fatalf("retired types were not normalized: %#v", place.Types)
	}
}

func TestValidateCampusMapEntranceSubtype(t *testing.T) {
	place := CampusMapPlace{
		CampusID: "xiaoxiang", Name: "东门", Latitude: 28.15, Longitude: 112.93,
		CoordinateSystem: "CGCS2000", Category: "transportation", Types: []string{"entrance"},
	}
	if err := ValidateCampusMapPlace(place); err != nil {
		t.Fatalf("valid entrance subtype rejected: %v", err)
	}
	if normalized := NormalizeCampusMapPlace(place); normalized.Type != "gate" || normalized.Types[0] != "entrance" {
		t.Fatalf("entrance compatibility mapping was not preserved: %#v", normalized)
	}
}

func TestValidateCampusMapChargingStationSubtype(t *testing.T) {
	place := CampusMapPlace{
		CampusID: "xiaoxiang", Name: "充电桩", Latitude: 28.15, Longitude: 112.93,
		CoordinateSystem: "CGCS2000", Category: "life_service", Types: []string{"charging_station"},
	}
	if err := ValidateCampusMapPlace(place); err != nil {
		t.Fatalf("valid charging station subtype rejected: %v", err)
	}
	if normalized := NormalizeCampusMapPlace(place); normalized.Type != "service" || normalized.Types[0] != "charging_station" {
		t.Fatalf("charging station compatibility mapping was not preserved: %#v", normalized)
	}
}

func TestValidateCampusMapPrintingServiceSubtype(t *testing.T) {
	place := CampusMapPlace{
		CampusID: "xiaoxiang", Name: "打印店", Latitude: 28.15, Longitude: 112.93,
		CoordinateSystem: "CGCS2000", Category: "life_service", Types: []string{"printing_service"},
	}
	if err := ValidateCampusMapPlace(place); err != nil {
		t.Fatalf("valid printing service subtype rejected: %v", err)
	}
	if normalized := NormalizeCampusMapPlace(place); normalized.Type != "service" || normalized.Types[0] != "printing_service" {
		t.Fatalf("printing service compatibility mapping was not preserved: %#v", normalized)
	}
}

func TestValidateCampusMapPlace(t *testing.T) {
	valid := CampusMapPlace{
		CampusID:         "xiaoxiang",
		Name:             "图书馆",
		Latitude:         28.1531,
		Longitude:        112.9390,
		CoordinateSystem: "CGCS2000",
		Type:             "library",
	}
	if err := ValidateCampusMapPlace(valid); err != nil {
		t.Fatalf("valid place rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*CampusMapPlace)
	}{
		{"invalid campus", func(p *CampusMapPlace) { p.CampusID = "Xiang Ya" }},
		{"blank name", func(p *CampusMapPlace) { p.Name = " " }},
		{"invalid latitude", func(p *CampusMapPlace) { p.Latitude = 91 }},
		{"invalid longitude", func(p *CampusMapPlace) { p.Longitude = 181 }},
		{"invalid coordinate system", func(p *CampusMapPlace) { p.CoordinateSystem = "GCJ02" }},
		{"invalid type", func(p *CampusMapPlace) { p.Type = "custom" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			place := valid
			test.mutate(&place)
			if err := ValidateCampusMapPlace(place); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
