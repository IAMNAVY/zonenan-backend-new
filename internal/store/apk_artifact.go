package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/shogo82148/androidbinary/apk"
)

const defaultAPKMaxBytes int64 = 512 << 20

var (
	ErrArtifactURLPolicy = errors.New("APK URL 不符合远程资源访问策略")
	ErrArtifactTooLarge  = errors.New("APK 文件超过大小限制")
)

// ArtifactURLPolicy limits the public URL that the backend may fetch. Every
// redirect and every DNS connection is checked against this policy.
type ArtifactURLPolicy struct {
	AllowedHosts []string
	MaxBytes     int64
	Timeout      time.Duration
	resolveHost  func(string) ([]net.IP, error)
}

// APKArtifact is metadata calculated from the bytes fetched from SourceURL.
type APKArtifact struct {
	SourceURL   string    `json:"source_url"`
	VersionName string    `json:"version_name"`
	VersionCode int       `json:"version_code"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	VerifiedAt  time.Time `json:"verified_at"`
}

type APKArtifactVerifier struct {
	policy ArtifactURLPolicy
	client *http.Client
}

func NewAPKArtifactVerifier(policy ArtifactURLPolicy) *APKArtifactVerifier {
	if policy.MaxBytes <= 0 {
		policy.MaxBytes = defaultAPKMaxBytes
	}
	if policy.Timeout <= 0 {
		policy.Timeout = 45 * time.Second
	}
	transport := &http.Transport{
		// Do not inherit a process-wide forward proxy: a proxy could resolve a
		// previously checked hostname differently and defeat the SSRF policy.
		Proxy:                 nil,
		DialContext:           (&safeArtifactDialer{policy: policy}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: policy.Timeout,
		IdleConnTimeout:       30 * time.Second,
	}
	return &APKArtifactVerifier{
		policy: policy,
		client: &http.Client{
			Transport: transport,
			Timeout:   policy.Timeout,
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				return validateArtifactURL(req.URL, policy)
			},
		},
	}
}

func (v *APKArtifactVerifier) Verify(ctx context.Context, rawURL string) (APKArtifact, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return APKArtifact{}, fmt.Errorf("解析 APK URL: %w", err)
	}
	if err := validateArtifactURL(parsed, v.policy); err != nil {
		return APKArtifact{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return APKArtifact{}, fmt.Errorf("创建 APK 请求: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.android.package-archive, application/octet-stream;q=0.9, */*;q=0.1")
	resp, err := v.client.Do(req)
	if err != nil {
		return APKArtifact{}, fmt.Errorf("获取 APK: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return APKArtifact{}, fmt.Errorf("获取 APK 返回 HTTP %d", resp.StatusCode)
	}
	if resp.Request == nil || resp.Request.URL == nil {
		return APKArtifact{}, errors.New("APK 响应缺少最终 URL")
	}
	if err := validateArtifactURL(resp.Request.URL, v.policy); err != nil {
		return APKArtifact{}, err
	}
	if resp.ContentLength > v.policy.MaxBytes {
		return APKArtifact{}, ErrArtifactTooLarge
	}

	tmp, err := os.CreateTemp("", "zonenan-apk-*")
	if err != nil {
		return APKArtifact{}, fmt.Errorf("创建 APK 临时文件: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	defer tmp.Close()

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, v.policy.MaxBytes+1)
	size, err := io.Copy(io.MultiWriter(tmp, hasher), limited)
	if err != nil {
		return APKArtifact{}, fmt.Errorf("读取 APK: %w", err)
	}
	if size > v.policy.MaxBytes {
		return APKArtifact{}, ErrArtifactTooLarge
	}
	if resp.ContentLength >= 0 && resp.ContentLength != size {
		return APKArtifact{}, fmt.Errorf("APK Content-Length 与实际大小不一致: %d != %d", resp.ContentLength, size)
	}
	if err := tmp.Close(); err != nil {
		return APKArtifact{}, fmt.Errorf("关闭 APK 临时文件: %w", err)
	}

	parsedAPK, err := apk.OpenFile(tmpPath)
	if err != nil {
		return APKArtifact{}, fmt.Errorf("解析 APK: %w", err)
	}
	versionName, versionCode, err := parseAPKMetadata(parsedAPK)
	closeErr := parsedAPK.Close()
	if err != nil {
		return APKArtifact{}, err
	}
	if closeErr != nil {
		return APKArtifact{}, fmt.Errorf("关闭 APK 解析器: %w", closeErr)
	}
	if _, _, _, err := ParseVersion(versionName); err != nil {
		return APKArtifact{}, fmt.Errorf("APK versionName 无效: %w", err)
	}
	if int64(versionCode) > int64(^uint(0)>>1) {
		return APKArtifact{}, errors.New("APK versionCode 超出服务端整数范围")
	}
	return APKArtifact{
		SourceURL:   resp.Request.URL.String(),
		VersionName: strings.TrimSpace(versionName),
		VersionCode: int(versionCode),
		SizeBytes:   size,
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
		VerifiedAt:  time.Now().UTC(),
	}, nil
}

func parseAPKMetadata(parsedAPK *apk.Apk) (string, int, error) {
	manifest := parsedAPK.Manifest()
	versionName, err := manifest.VersionName.String()
	if err != nil || strings.TrimSpace(versionName) == "" {
		return "", 0, errors.New("APK 缺少可解析的 versionName")
	}
	versionCode, err := manifest.VersionCode.Int32()
	if err != nil || versionCode <= 0 {
		return "", 0, errors.New("APK 缺少有效的 versionCode")
	}
	return strings.TrimSpace(versionName), int(versionCode), nil
}

func validateArtifactURL(u *url.URL, policy ArtifactURLPolicy) error {
	if u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return ErrArtifactURLPolicy
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return ErrArtifactURLPolicy
	}
	if len(policy.AllowedHosts) > 0 {
		allowed := false
		for _, candidate := range policy.AllowedHosts {
			if strings.EqualFold(host, strings.TrimSuffix(candidate, ".")) {
				allowed = true
				break
			}
		}
		if !allowed {
			return ErrArtifactURLPolicy
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicArtifactIP(ip) {
			return ErrArtifactURLPolicy
		}
		return nil
	}
	lookupHost := policy.resolveHost
	if lookupHost == nil {
		lookupHost = net.LookupIP
	}
	ips, err := lookupHost(host)
	if err != nil || len(ips) == 0 {
		return ErrArtifactURLPolicy
	}
	for _, ip := range ips {
		if !isPublicArtifactIP(ip) {
			return ErrArtifactURLPolicy
		}
	}
	return nil
}

func isPublicArtifactIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsUnspecified() && !ip.IsMulticast()
}

type safeArtifactDialer struct{ policy ArtifactURLPolicy }

func (d *safeArtifactDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrArtifactURLPolicy
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		if err := validateArtifactURL(&url.URL{Scheme: "https", Host: net.JoinHostPort(host, port)}, d.policy); err != nil {
			return nil, err
		}
		lookupHost := d.policy.resolveHost
		if lookupHost == nil {
			lookupHost = net.LookupIP
		}
		ips, err = lookupHost(host)
		if err != nil {
			return nil, ErrArtifactURLPolicy
		}
	}
	for _, ip := range ips {
		if !isPublicArtifactIP(ip) {
			return nil, ErrArtifactURLPolicy
		}
		conn, dialErr := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(
			ctx, network, net.JoinHostPort(ip.String(), port),
		)
		if dialErr == nil {
			return conn, nil
		}
	}
	return nil, ErrArtifactURLPolicy
}
