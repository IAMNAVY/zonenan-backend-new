package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shogo82148/androidbinary/apk"
)

func TestParseAPKMetadataRealFlutterFixture(t *testing.T) {
	fixture := os.Getenv("ZONENAN_APK_FIXTURE")
	if fixture == "" {
		fixture = filepath.Clean("../../../csu-app-flutter/build/app/outputs/flutter-apk/app-release.apk")
	}
	if _, err := os.Stat(fixture); err != nil {
		t.Skipf("real APK fixture unavailable: %v", err)
	}
	parsed, err := apk.OpenFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer parsed.Close()
	version, build, err := parseAPKMetadata(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if version != "2.3.0" || build <= 0 {
		t.Fatalf("APK metadata = %s+%d, want version 2.2.2 and positive build", version, build)
	}
	t.Logf("real Flutter APK metadata: version=%s build=%d", version, build)
}

func TestAPKArtifactVerifierFetchesAndHashesAPK(t *testing.T) {
	fixture := filepath.Clean("../../../csu-app-flutter/build/app/outputs/flutter-apk/app-release.apk")
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Skipf("real APK fixture unavailable: %v", err)
	}
	requestURL, _ := url.Parse("https://downloads.example.test/app.apk")
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)),
			ContentLength: int64(len(data)), Request: req,
			Header: make(http.Header),
		}, nil
	})}
	policy := ArtifactURLPolicy{
		AllowedHosts: []string{"downloads.example.test"}, MaxBytes: int64(len(data) + 1),
		Timeout:     5 * time.Second,
		resolveHost: func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil },
	}
	verifier := &APKArtifactVerifier{policy: policy, client: client}
	artifact, err := verifier.Verify(context.Background(), requestURL.String())
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if artifact.VersionName != "2.3.0" || artifact.VersionCode <= 0 || artifact.SizeBytes != int64(len(data)) || artifact.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("unexpected artifact metadata: %#v", artifact)
	}
}

func TestValidateArtifactURLRejectsPrivateTargets(t *testing.T) {
	verifier := NewAPKArtifactVerifier(ArtifactURLPolicy{AllowedHosts: []string{"127.0.0.1"}})
	_, err := verifier.Verify(t.Context(), "https://127.0.0.1/app.apk")
	if err == nil {
		t.Fatal("private artifact target unexpectedly accepted")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
