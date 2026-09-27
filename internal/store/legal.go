package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"zonenan-backend/internal/db"
)

// LegalStore 读取在线协议文档(用户协议/隐私政策/给分授权说明)。
type LegalStore struct{ pool *db.Pool }

func NewLegalStore(p *db.Pool) *LegalStore { return &LegalStore{pool: p} }

// LegalDoc 是一篇文档的当前生效版本。
type LegalDoc struct {
	DocType     string    `json:"doc_type"`
	Version     int       `json:"version"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	PublishedAt time.Time `json:"published_at"`
}

// GetLatest 取某类型文档的最高版本(当前生效版)。无则 ErrNotFound。
func (s *LegalStore) GetLatest(ctx context.Context, docType string) (*LegalDoc, error) {
	d := &LegalDoc{}
	err := s.pool.QueryRow(ctx,
		`SELECT doc_type, version, title, content, published_at
		   FROM legal_documents WHERE doc_type=$1
		  ORDER BY version DESC LIMIT 1`, docType,
	).Scan(&d.DocType, &d.Version, &d.Title, &d.Content, &d.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// UpdateLatest 原地修改某类型文档的当前(最高版本)标题/正文,不新增版本号。
// 用于纠正错别字等无需留版本痕迹的小改。返回是否有记录被更新。
func (s *LegalStore) UpdateLatest(ctx context.Context, docType, title, content string) (bool, error) {
	ct, err := s.pool.Exec(ctx,
		`UPDATE legal_documents SET title=$2, content=$3
		  WHERE doc_type=$1
		    AND version=(SELECT MAX(version) FROM legal_documents WHERE doc_type=$1)`,
		docType, title, content)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// LatestVersions 返回每类型文档的当前版本号(供 App 检测是否需重新阅读)。
func (s *LegalStore) LatestVersions(ctx context.Context) (map[string]int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT doc_type, MAX(version) FROM legal_documents GROUP BY doc_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var v int
		if err := rows.Scan(&t, &v); err != nil {
			return nil, err
		}
		out[t] = v
	}
	return out, rows.Err()
}
