package store

import (
	"errors"
	"testing"
)

func TestValidateHistoricalAppRelease(t *testing.T) {
	base := AppRelease{
		Version: "2.2.1", BuildNumber: 2014, Channel: ReleaseStable,
		Changelog: "补登历史版本",
	}
	if err := ValidateHistoricalAppRelease(base); err != nil {
		t.Fatal(err)
	}
	withArtifact := base
	withArtifact.AndroidURL = "https://example.test/app.apk"
	if err := ValidateHistoricalAppRelease(withArtifact); !errors.Is(err, ErrHistoricalArtifactNotAllowed) {
		t.Fatalf("artifact validation error = %v, want %v", err, ErrHistoricalArtifactNotAllowed)
	}
	for name, release := range map[string]AppRelease{
		"invalid version": {Version: "2.2", BuildNumber: 2014, Channel: ReleaseStable},
		"invalid channel": {Version: "2.2.1", BuildNumber: 2014, Channel: "preview"},
		"invalid build":   {Version: "2.2.1", BuildNumber: 0, Channel: ReleaseStable},
	} {
		if err := ValidateHistoricalAppRelease(release); err == nil {
			t.Errorf("ValidateHistoricalAppRelease(%s) unexpectedly succeeded", name)
		}
	}
}

func TestParseVersion(t *testing.T) {
	major, minor, patch, err := ParseVersion("2.10.3")
	if err != nil || major != 2 || minor != 10 || patch != 3 {
		t.Fatalf("ParseVersion() = %d.%d.%d, %v", major, minor, patch, err)
	}
	for _, version := range []string{"", "v1.2.3", "1.2", "1.2.3.4", "1.a.3"} {
		if _, _, _, err := ParseVersion(version); err == nil {
			t.Errorf("ParseVersion(%q) unexpectedly succeeded", version)
		}
	}
}

func TestValidateAppRelease(t *testing.T) {
	base := AppRelease{Version: "2.3.0", BuildNumber: 15, Channel: ReleaseStable, AndroidURL: "https://example.test/app.apk"}
	if err := ValidateAppRelease(base); err != nil {
		t.Fatal(err)
	}
	for name, release := range map[string]AppRelease{
		"channel": {Version: "2.3.0", BuildNumber: 15, Channel: "preview", AndroidURL: "https://example.test/app.apk"},
		"build":   {Version: "2.3.0", BuildNumber: 0, Channel: ReleaseStable, AndroidURL: "https://example.test/app.apk"},
		"sha":     {Version: "2.3.0", BuildNumber: 15, Channel: ReleaseStable, AndroidURL: "https://example.test/app.apk", AndroidSHA256: "bad"},
		"url":     {Version: "2.3.0", BuildNumber: 15, Channel: ReleaseStable},
	} {
		if err := ValidateAppRelease(release); err == nil {
			t.Errorf("ValidateAppRelease(%s) unexpectedly succeeded", name)
		}
	}
	for _, mode := range []string{UpdatePopup, UpdateSilent} {
		withMode := base
		withMode.UpdateMode = mode
		if err := ValidateAppRelease(withMode); err != nil {
			t.Fatalf("ValidateAppRelease(%q) = %v", mode, err)
		}
	}
	invalidMode := base
	invalidMode.UpdateMode = "banner"
	if err := ValidateAppRelease(invalidMode); err == nil {
		t.Fatal("ValidateAppRelease accepted unknown update mode")
	}
}
