package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"zonenan-backend/internal/db"
)

var semverPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)
var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
var ErrBuildNumberNotIncreasing = errors.New("build_number 必须大于历史已发布最大值")
var ErrArtifactMetadataRequired = errors.New("Android APK 必须先通过后端 artifact 校验")
var ErrHistoricalArtifactNotAllowed = errors.New("历史 Release 不允许包含下载 artifact")
var ErrReleaseAlreadyExists = errors.New("相同 version 和 channel 的 Release 已存在")
var ErrBuildNumberAlreadyUsed = errors.New("build_number 已被其他 Release 使用")

const (
	ReleaseStable   = "stable"
	ReleaseBeta     = "beta"
	ReleaseRC       = "rc"
	ReleaseInternal = "internal"
	UpdatePopup     = "popup"
	UpdateSilent    = "silent"
)

var releaseChannels = map[string]bool{
	ReleaseStable: true, ReleaseBeta: true, ReleaseRC: true, ReleaseInternal: true,
}

var releasePlatforms = map[string]bool{
	"android": true, "ios": true, "windows": true,
}

// AppRelease is one published product release. Its channel is server-owned.
type AppRelease struct {
	ID                      int64            `json:"id"`
	Version                 string           `json:"version"`
	VersionMajor            int              `json:"-"`
	VersionMinor            int              `json:"-"`
	VersionPatch            int              `json:"-"`
	BuildNumber             int              `json:"build_number"`
	Channel                 string           `json:"channel"`
	Changelog               string           `json:"changelog"`
	WhatsNew                *ReleaseWhatsNew `json:"whats_new,omitempty"`
	MinSupportedVersion     string           `json:"min_supported,omitempty"`
	MinSupportedBuildNumber int              `json:"min_supported_build_number,omitempty"`
	ForceUpdate             bool             `json:"force"`
	UpdateMode              string           `json:"update_mode"`
	AndroidURL              string           `json:"android_url,omitempty"`
	AndroidSHA256           string           `json:"android_sha256,omitempty"`
	WindowsURL              string           `json:"windows_url,omitempty"`
	WindowsSHA256           string           `json:"windows_sha256,omitempty"`
	IOSURL                  string           `json:"ios_url,omitempty"`
	AndroidVersionName      string           `json:"android_version_name,omitempty"`
	AndroidVersionCode      int              `json:"android_version_code,omitempty"`
	AndroidSizeBytes        int64            `json:"android_size_bytes,omitempty"`
	AndroidVerifiedAt       *time.Time       `json:"android_verified_at,omitempty"`
	Active                  bool             `json:"active"`
	ArchivedAt              *time.Time       `json:"archived_at,omitempty"`
	DeletedAt               *time.Time       `json:"deleted_at,omitempty"`
	CreatedAt               time.Time        `json:"created_at"`
	UpdatedAt               time.Time        `json:"updated_at"`
}

type AppReleaseStore struct{ pool *db.Pool }

func NewAppReleaseStore(p *db.Pool) *AppReleaseStore { return &AppReleaseStore{pool: p} }

// Validate checks the fields that must never be accepted from the admin UI.
func ValidateAppRelease(r AppRelease) error {
	parts := semverPattern.FindStringSubmatch(strings.TrimSpace(r.Version))
	if parts == nil {
		return errors.New("version 必须是 x.y.z")
	}
	if !releaseChannels[r.Channel] {
		return errors.New("channel 无效")
	}
	if r.BuildNumber <= 0 {
		return errors.New("build_number 必须为正整数")
	}
	if r.MinSupportedVersion != "" && semverPattern.FindStringSubmatch(r.MinSupportedVersion) == nil {
		return errors.New("min_supported 必须是 x.y.z")
	}
	for _, v := range []string{r.AndroidSHA256, r.WindowsSHA256} {
		if v != "" && !sha256Pattern.MatchString(v) {
			return errors.New("SHA-256 格式无效")
		}
	}
	if r.MinSupportedBuildNumber < 0 {
		return errors.New("min_supported_build_number 不能为负数")
	}
	if r.UpdateMode != "" && r.UpdateMode != UpdatePopup && r.UpdateMode != UpdateSilent {
		return errors.New("update_mode 无效")
	}
	if r.AndroidURL == "" && r.WindowsURL == "" && r.IOSURL == "" {
		return errors.New("至少需要一个平台下载地址")
	}
	return nil
}

func ValidateHistoricalAppRelease(r AppRelease) error {
	parts := semverPattern.FindStringSubmatch(strings.TrimSpace(r.Version))
	if parts == nil {
		return errors.New("version 必须是 x.y.z")
	}
	if !releaseChannels[r.Channel] {
		return errors.New("channel 无效")
	}
	if r.BuildNumber <= 0 {
		return errors.New("build_number 必须为正整数")
	}
	if r.MinSupportedVersion != "" && semverPattern.FindStringSubmatch(r.MinSupportedVersion) == nil {
		return errors.New("min_supported 必须是 x.y.z")
	}
	if r.MinSupportedBuildNumber < 0 {
		return errors.New("min_supported_build_number 不能为负数")
	}
	if r.AndroidURL != "" || r.AndroidSHA256 != "" || r.WindowsURL != "" ||
		r.WindowsSHA256 != "" || r.IOSURL != "" || r.AndroidVersionName != "" ||
		r.AndroidVersionCode != 0 || r.AndroidSizeBytes != 0 || r.AndroidVerifiedAt != nil {
		return ErrHistoricalArtifactNotAllowed
	}
	return nil
}

func ParseVersion(version string) (int, int, int, error) {
	parts := semverPattern.FindStringSubmatch(strings.TrimSpace(version))
	if parts == nil {
		return 0, 0, 0, errors.New("version 必须是 x.y.z")
	}
	major, _ := strconv.Atoi(parts[1])
	minor, _ := strconv.Atoi(parts[2])
	patch, _ := strconv.Atoi(parts[3])
	return major, minor, patch, nil
}

func (s *AppReleaseStore) Latest(ctx context.Context, platform string, channels []string) (*AppRelease, error) {
	if !releasePlatforms[platform] || len(channels) == 0 {
		return nil, pgx.ErrNoRows
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, version, version_major, version_minor, version_patch, build_number,
		       channel, changelog, min_supported_version, min_supported_build_number,
		       force_update, update_mode, android_url, android_sha256, windows_url, windows_sha256,
		       ios_url, android_version_name, android_version_code, android_size_bytes,
		       android_verified_at, active, archived_at, deleted_at, created_at, updated_at, whats_new
		  FROM app_releases
		 WHERE deleted_at IS NULL AND active = TRUE AND channel = ANY($1)
		   AND (( $2 = 'android' AND android_url <> '' )
		     OR ( $2 = 'windows' AND windows_url <> '' )
		     OR ( $2 = 'ios' AND ios_url <> '' ))
		 ORDER BY version_major DESC, version_minor DESC, version_patch DESC,
		          build_number DESC, id DESC LIMIT 1`, channels, platform)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	var out AppRelease
	if err := scanAppRelease(rows, &out); err != nil {
		return nil, err
	}
	return &out, rows.Err()
}

// List returns non-deleted releases visible to the supplied channel set.
func (s *AppReleaseStore) List(ctx context.Context, channel string, channels []string) ([]AppRelease, error) {
	if len(channels) == 0 {
		return []AppRelease{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, version, version_major, version_minor, version_patch, build_number,
		       channel, changelog, min_supported_version, min_supported_build_number,
		       force_update, update_mode, android_url, android_sha256, windows_url, windows_sha256,
		       ios_url, android_version_name, android_version_code, android_size_bytes,
		       android_verified_at, active, archived_at, deleted_at, created_at, updated_at, whats_new
		  FROM app_releases
		 WHERE deleted_at IS NULL AND channel = ANY($1) AND ($2 = '' OR channel = $2)
		 ORDER BY version_major DESC, version_minor DESC, version_patch DESC,
		          build_number DESC, id DESC`, channels, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppRelease{}
	for rows.Next() {
		var item AppRelease
		if err := scanAppRelease(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *AppReleaseStore) All(ctx context.Context, includeDeleted bool) ([]AppRelease, error) {
	where := "WHERE deleted_at IS NULL"
	if includeDeleted {
		where = ""
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, version, version_major, version_minor, version_patch, build_number,
		       channel, changelog, min_supported_version, min_supported_build_number,
		       force_update, update_mode, android_url, android_sha256, windows_url, windows_sha256,
		       ios_url, android_version_name, android_version_code, android_size_bytes,
		       android_verified_at, active, archived_at, deleted_at, created_at, updated_at, whats_new
		  FROM app_releases %s
		 ORDER BY version_major DESC, version_minor DESC, version_patch DESC,
		          build_number DESC, id DESC`, where))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppRelease{}
	for rows.Next() {
		var item AppRelease
		if err := scanAppRelease(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanAppRelease(row interface{ Scan(...any) error }, out *AppRelease) error {
	var versionName *string
	var versionCode *int
	var sizeBytes *int64
	var whatsNew []byte
	err := row.Scan(&out.ID, &out.Version, &out.VersionMajor, &out.VersionMinor,
		&out.VersionPatch, &out.BuildNumber, &out.Channel, &out.Changelog,
		&out.MinSupportedVersion, &out.MinSupportedBuildNumber, &out.ForceUpdate,
		&out.UpdateMode, &out.AndroidURL, &out.AndroidSHA256, &out.WindowsURL, &out.WindowsSHA256,
		&out.IOSURL, &versionName, &versionCode, &sizeBytes, &out.AndroidVerifiedAt,
		&out.Active, &out.ArchivedAt, &out.DeletedAt, &out.CreatedAt, &out.UpdatedAt, &whatsNew)
	if err != nil {
		return err
	}
	if len(whatsNew) > 0 {
		var guide ReleaseWhatsNew
		if err := json.Unmarshal(whatsNew, &guide); err != nil {
			return err
		}
		out.WhatsNew = &guide
	}
	if versionName != nil {
		out.AndroidVersionName = *versionName
	}
	if versionCode != nil {
		out.AndroidVersionCode = *versionCode
	}
	if sizeBytes != nil {
		out.AndroidSizeBytes = *sizeBytes
	}
	return nil
}

func (s *AppReleaseStore) Save(ctx context.Context, r AppRelease) (int64, error) {
	if strings.TrimSpace(r.UpdateMode) == "" {
		r.UpdateMode = UpdatePopup
	} else {
		r.UpdateMode = strings.TrimSpace(r.UpdateMode)
	}
	if err := ValidateAppRelease(r); err != nil {
		return 0, err
	}
	if r.AndroidURL != "" && (r.AndroidVersionName == "" || r.AndroidVersionName != r.Version ||
		r.AndroidVersionCode <= 0 || r.AndroidVersionCode != r.BuildNumber ||
		r.AndroidSizeBytes <= 0 || r.AndroidVerifiedAt == nil || !sha256Pattern.MatchString(r.AndroidSHA256)) {
		return 0, ErrArtifactMetadataRequired
	}
	major, minor, patch, err := ParseVersion(r.Version)
	if err != nil {
		return 0, err
	}
	r.Version = strings.TrimSpace(r.Version)
	r.AndroidSHA256 = strings.ToLower(strings.TrimSpace(r.AndroidSHA256))
	r.WindowsSHA256 = strings.ToLower(strings.TrimSpace(r.WindowsSHA256))
	var historicalMax int
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(build_number), 0) FROM app_releases`).Scan(&historicalMax); err != nil {
		return 0, err
	}
	if r.ID == 0 && r.BuildNumber <= historicalMax {
		return 0, ErrBuildNumberNotIncreasing
	}
	if r.ID != 0 && r.BuildNumber < historicalMax {
		return 0, ErrBuildNumberNotIncreasing
	}
	if r.ID == 0 {
		err = s.pool.QueryRow(ctx, `
			INSERT INTO app_releases(
				version, version_major, version_minor, version_patch, build_number, channel,
				changelog, min_supported_version, min_supported_build_number, force_update, update_mode,
				android_url, android_sha256, windows_url, windows_sha256, ios_url,
				android_version_name, android_version_code, android_size_bytes, android_verified_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20) RETURNING id`,
			r.Version, major, minor, patch, r.BuildNumber, r.Channel, r.Changelog,
			r.MinSupportedVersion, r.MinSupportedBuildNumber, r.ForceUpdate, r.UpdateMode,
			r.AndroidURL, r.AndroidSHA256, r.WindowsURL, r.WindowsSHA256, r.IOSURL,
			r.AndroidVersionName, r.AndroidVersionCode, r.AndroidSizeBytes, r.AndroidVerifiedAt).Scan(&r.ID)
	} else {
		result, err := s.pool.Exec(ctx, `
			UPDATE app_releases SET version=$2, version_major=$3, version_minor=$4,
			  version_patch=$5, build_number=$6, channel=$7, changelog=$8,
			  min_supported_version=$9, min_supported_build_number=$10, force_update=$11,
			  update_mode=$12, android_url=$13, android_sha256=$14, windows_url=$15, windows_sha256=$16,
			  ios_url=$17, android_version_name=$18, android_version_code=$19,
			  android_size_bytes=$20, android_verified_at=$21, updated_at=NOW()
			 WHERE id=$1 AND deleted_at IS NULL`, r.ID, r.Version, major, minor, patch,
			r.BuildNumber, r.Channel, r.Changelog, r.MinSupportedVersion,
			r.MinSupportedBuildNumber, r.ForceUpdate, r.UpdateMode, r.AndroidURL, r.AndroidSHA256,
			r.WindowsURL, r.WindowsSHA256, r.IOSURL, r.AndroidVersionName,
			r.AndroidVersionCode, r.AndroidSizeBytes, r.AndroidVerifiedAt)
		if err == nil && result.RowsAffected() == 0 {
			err = ErrNotFound
		}
	}
	if err != nil {
		return 0, err
	}
	// A stable release supersedes preview packages for the same SemVer. Older
	// stable releases remain active history and are selected by version order.
	if r.Channel == ReleaseStable {
		_, err = s.pool.Exec(ctx, `
			UPDATE app_releases SET active=FALSE, archived_at=COALESCE(archived_at,NOW()), updated_at=NOW()
			 WHERE version=$1 AND channel IN ('beta','rc','internal')
			   AND deleted_at IS NULL AND id <> $2`, r.Version, r.ID)
	}
	return r.ID, err
}

// BackfillHistorical inserts a changelog-only record. Unlike Save, it permits
// a build below the current release frontier so omitted older releases can be
// restored without weakening normal release monotonicity.
func (s *AppReleaseStore) BackfillHistorical(ctx context.Context, r AppRelease) (int64, error) {
	if err := ValidateHistoricalAppRelease(r); err != nil {
		return 0, err
	}
	major, minor, patch, err := ParseVersion(r.Version)
	if err != nil {
		return 0, err
	}
	r.Version = strings.TrimSpace(r.Version)

	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM app_releases WHERE version=$1 AND channel=$2)`,
		r.Version, r.Channel,
	).Scan(&exists); err != nil {
		return 0, err
	}
	if exists {
		return 0, ErrReleaseAlreadyExists
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM app_releases WHERE build_number=$1)`,
		r.BuildNumber,
	).Scan(&exists); err != nil {
		return 0, err
	}
	if exists {
		return 0, ErrBuildNumberAlreadyUsed
	}

	var id int64
	err = s.pool.QueryRow(ctx, `
		INSERT INTO app_releases(
			version, version_major, version_minor, version_patch, build_number,
			channel, changelog, min_supported_version, min_supported_build_number,
			force_update, active)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,TRUE)
		RETURNING id`,
		r.Version, major, minor, patch, r.BuildNumber, r.Channel, r.Changelog,
		r.MinSupportedVersion, r.MinSupportedBuildNumber, r.ForceUpdate,
	).Scan(&id)
	return id, err
}

func (s *AppReleaseStore) Archive(ctx context.Context, id int64, archived bool) error {
	if archived {
		_, err := s.pool.Exec(ctx, `UPDATE app_releases SET active=FALSE, archived_at=NOW(), updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, id)
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE app_releases SET active=TRUE, archived_at=NULL, updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, id)
	return err
}

func (s *AppReleaseStore) Restore(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE app_releases SET active=TRUE, archived_at=NULL, deleted_at=NULL, updated_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *AppReleaseStore) Delete(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE app_releases SET active=FALSE, deleted_at=NOW(), updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, id)
	return err
}

// ReleaseBetaMembershipStore is separate from legacy beta_memberships, which
// represents premium entitlement and old content audience semantics.
type ReleaseBetaMembershipStore struct{ pool *db.Pool }

func NewReleaseBetaMembershipStore(p *db.Pool) *ReleaseBetaMembershipStore {
	return &ReleaseBetaMembershipStore{pool: p}
}

func (s *ReleaseBetaMembershipStore) IsMember(ctx context.Context, userID int64) bool {
	if userID <= 0 {
		return false
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM release_beta_memberships WHERE user_id=$1)`, userID).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func (s *ReleaseBetaMembershipStore) SetMember(ctx context.Context, userID int64, member bool) error {
	if member {
		_, err := s.pool.Exec(ctx, `INSERT INTO release_beta_memberships(user_id) VALUES($1) ON CONFLICT(user_id) DO NOTHING`, userID)
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM release_beta_memberships WHERE user_id=$1`, userID)
	return err
}

func (s *ReleaseBetaMembershipStore) List(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT user_id FROM release_beta_memberships ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
