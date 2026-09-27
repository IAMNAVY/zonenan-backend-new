package store

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"zonenan-backend/internal/db"
)

const defaultClassroomArtifactMaxBytes int64 = 64 << 20

var classroomTermPattern = regexp.MustCompile(`^\d{4}-\d{4}-[12]$`)

// ClassroomArtifact is a published, verified reference to a term SQLite package.
type ClassroomArtifact struct {
	ID          int64      `json:"id"`
	TermID      string     `json:"term_id"`
	DisplayName string     `json:"display_name"`
	FirstMonday time.Time  `json:"first_monday"`
	TotalWeeks  int        `json:"total_weeks"`
	SourceURL   string     `json:"url"`
	SHA256      string     `json:"sha256"`
	SizeBytes   int64      `json:"size_bytes"`
	VerifiedAt  time.Time  `json:"verified_at"`
	Active      bool       `json:"active"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func ValidateClassroomArtifact(a ClassroomArtifact) error {
	if !classroomTermPattern.MatchString(strings.TrimSpace(a.TermID)) {
		return errors.New("学期格式应为 YYYY-YYYY-1 或 YYYY-YYYY-2")
	}
	if a.FirstMonday.IsZero() || a.FirstMonday.Weekday() != time.Monday {
		return errors.New("第一周日期必须是星期一")
	}
	if a.TotalWeeks < 1 || a.TotalWeeks > 32 {
		return errors.New("总周数必须在 1 到 32 之间")
	}
	if strings.TrimSpace(a.SourceURL) == "" {
		return errors.New("缺少数据包 URL")
	}
	return nil
}

// ClassroomArtifactStore manages the small metadata records; artifact bytes stay in R2.
type ClassroomArtifactStore struct{ pool *db.Pool }

func NewClassroomArtifactStore(pool *db.Pool) *ClassroomArtifactStore {
	return &ClassroomArtifactStore{pool: pool}
}

func (s *ClassroomArtifactStore) Active(ctx context.Context) ([]ClassroomArtifact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, term_id, display_name, first_monday, total_weeks, source_url,
		       sha256, size_bytes, verified_at, active, archived_at, created_at, updated_at
		  FROM classroom_data_artifacts
		 WHERE active = TRUE
		 ORDER BY first_monday ASC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanClassroomArtifacts(rows)
}

func (s *ClassroomArtifactStore) All(ctx context.Context) ([]ClassroomArtifact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, term_id, display_name, first_monday, total_weeks, source_url,
		       sha256, size_bytes, verified_at, active, archived_at, created_at, updated_at
		  FROM classroom_data_artifacts
		 ORDER BY first_monday DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanClassroomArtifacts(rows)
}

func scanClassroomArtifacts(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]ClassroomArtifact, error) {
	out := []ClassroomArtifact{}
	for rows.Next() {
		var item ClassroomArtifact
		if err := rows.Scan(
			&item.ID, &item.TermID, &item.DisplayName, &item.FirstMonday,
			&item.TotalWeeks, &item.SourceURL, &item.SHA256, &item.SizeBytes,
			&item.VerifiedAt, &item.Active, &item.ArchivedAt, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Publish archives the previous active revision for the term and inserts a new verified one.
func (s *ClassroomArtifactStore) Publish(ctx context.Context, artifact ClassroomArtifact) (ClassroomArtifact, error) {
	if err := ValidateClassroomArtifact(artifact); err != nil {
		return ClassroomArtifact{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ClassroomArtifact{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE classroom_data_artifacts
		   SET active=FALSE, archived_at=COALESCE(archived_at, NOW()), updated_at=NOW()
		 WHERE term_id=$1 AND active=TRUE`, artifact.TermID); err != nil {
		return ClassroomArtifact{}, err
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO classroom_data_artifacts(
			term_id, display_name, first_monday, total_weeks, source_url, sha256,
			size_bytes, verified_at, active)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,TRUE)
		RETURNING id, term_id, display_name, first_monday, total_weeks, source_url,
		          sha256, size_bytes, verified_at, active, archived_at, created_at, updated_at`,
		artifact.TermID, artifact.DisplayName, artifact.FirstMonday, artifact.TotalWeeks,
		artifact.SourceURL, artifact.SHA256, artifact.SizeBytes, artifact.VerifiedAt,
	)
	if err := row.Scan(
		&artifact.ID, &artifact.TermID, &artifact.DisplayName, &artifact.FirstMonday,
		&artifact.TotalWeeks, &artifact.SourceURL, &artifact.SHA256, &artifact.SizeBytes,
		&artifact.VerifiedAt, &artifact.Active, &artifact.ArchivedAt, &artifact.CreatedAt, &artifact.UpdatedAt,
	); err != nil {
		return ClassroomArtifact{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ClassroomArtifact{}, err
	}
	return artifact, nil
}

func (s *ClassroomArtifactStore) Archive(ctx context.Context, termID string) error {
	if !classroomTermPattern.MatchString(strings.TrimSpace(termID)) {
		return errors.New("学期格式无效")
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE classroom_data_artifacts
		   SET active=FALSE, archived_at=COALESCE(archived_at, NOW()), updated_at=NOW()
		 WHERE term_id=$1 AND active=TRUE`, termID)
	return err
}

// SQLiteArtifact describes metadata calculated from a remote SQLite file.
type SQLiteArtifact struct {
	SourceURL  string    `json:"url"`
	SizeBytes  int64     `json:"size_bytes"`
	SHA256     string    `json:"sha256"`
	VerifiedAt time.Time `json:"verified_at"`
}

// SQLiteArtifactVerifier uses the same SSRF-safe public-URL policy as app artifacts.
type SQLiteArtifactVerifier struct {
	policy ArtifactURLPolicy
	client *http.Client
}

func NewSQLiteArtifactVerifier(policy ArtifactURLPolicy) *SQLiteArtifactVerifier {
	if policy.MaxBytes <= 0 || policy.MaxBytes > defaultClassroomArtifactMaxBytes {
		policy.MaxBytes = defaultClassroomArtifactMaxBytes
	}
	if policy.Timeout <= 0 {
		policy.Timeout = 45 * time.Second
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&safeArtifactDialer{policy: policy}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: policy.Timeout,
		IdleConnTimeout:       30 * time.Second,
	}
	return &SQLiteArtifactVerifier{
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

func (v *SQLiteArtifactVerifier) Verify(ctx context.Context, rawURL string) (SQLiteArtifact, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return SQLiteArtifact{}, fmt.Errorf("解析 SQLite URL: %w", err)
	}
	if err := validateArtifactURL(parsed, v.policy); err != nil {
		return SQLiteArtifact{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return SQLiteArtifact{}, fmt.Errorf("创建 SQLite 请求: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.sqlite3, application/octet-stream;q=0.9, */*;q=0.1")
	resp, err := v.client.Do(req)
	if err != nil {
		return SQLiteArtifact{}, fmt.Errorf("获取 SQLite 数据包: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return SQLiteArtifact{}, fmt.Errorf("获取 SQLite 数据包返回 HTTP %d", resp.StatusCode)
	}
	if resp.Request == nil || resp.Request.URL == nil || validateArtifactURL(resp.Request.URL, v.policy) != nil {
		return SQLiteArtifact{}, ErrArtifactURLPolicy
	}
	if resp.ContentLength > v.policy.MaxBytes {
		return SQLiteArtifact{}, ErrArtifactTooLarge
	}

	limited := io.LimitReader(resp.Body, v.policy.MaxBytes+1)
	reader := bufio.NewReader(limited)
	header := make([]byte, 16)
	if _, err := io.ReadFull(reader, header); err != nil {
		return SQLiteArtifact{}, errors.New("SQLite 数据包过短")
	}
	if string(header) != "SQLite format 3\x00" {
		return SQLiteArtifact{}, errors.New("数据包不是有效 SQLite 文件")
	}
	hasher := sha256.New()
	_, _ = hasher.Write(header)
	size, err := io.Copy(hasher, reader)
	if err != nil {
		return SQLiteArtifact{}, fmt.Errorf("读取 SQLite 数据包: %w", err)
	}
	size += int64(len(header))
	if size > v.policy.MaxBytes {
		return SQLiteArtifact{}, ErrArtifactTooLarge
	}
	if resp.ContentLength >= 0 && resp.ContentLength != size {
		return SQLiteArtifact{}, fmt.Errorf("SQLite Content-Length 与实际大小不一致: %d != %d", resp.ContentLength, size)
	}
	return SQLiteArtifact{
		SourceURL:  resp.Request.URL.String(),
		SizeBytes:  size,
		SHA256:     hex.EncodeToString(hasher.Sum(nil)),
		VerifiedAt: time.Now().UTC(),
	}, nil
}
