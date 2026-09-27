package store

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"unicode/utf8"
)

type ReleaseWhatsNew struct {
	Title   string                `json:"title"`
	Summary string                `json:"summary,omitempty"`
	Items   []ReleaseWhatsNewItem `json:"items"`
}

type ReleaseWhatsNewItem struct {
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	Platforms   []string `json:"platforms,omitempty"`
	ImageURL    string   `json:"image_url,omitempty"`
	ActionID    string   `json:"action_id,omitempty"`
	ActionLabel string   `json:"action_label,omitempty"`
}

func ValidateReleaseWhatsNew(guide *ReleaseWhatsNew) error {
	if guide == nil {
		return nil
	}
	validText := func(s string, max int) bool {
		return strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= max
	}
	if !validText(guide.Title, 80) || utf8.RuneCountInString(guide.Summary) > 240 ||
		len(guide.Items) == 0 || len(guide.Items) > 3 {
		return errors.New("本版亮点标题或条目无效（最多 3 条）")
	}
	for _, item := range guide.Items {
		if !validText(item.Title, 80) || !validText(item.Body, 600) || len(item.Platforms) > 3 {
			return errors.New("本版亮点条目标题、正文或平台无效")
		}
		seen := map[string]bool{}
		for _, platform := range item.Platforms {
			if !releasePlatforms[platform] || seen[platform] {
				return errors.New("本版亮点平台无效")
			}
			seen[platform] = true
		}
		if item.ImageURL != "" {
			u, err := url.Parse(item.ImageURL)
			if err != nil || u == nil {
				return errors.New("亮点图片必须使用 HTTPS 地址")
			}
			host := strings.ToLower(u.Hostname())
			ip := net.ParseIP(host)
			if err != nil || u.Scheme != "https" || host == "" || u.User != nil || len(item.ImageURL) > 2048 ||
				host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
				(ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())) {
				return errors.New("亮点图片必须使用 HTTPS 地址")
			}
		}
		if item.ActionID != "" && item.ActionID != "open_reminder_settings" {
			return errors.New("本版亮点按钮目标无效")
		}
		if item.ActionID == "" && item.ActionLabel != "" ||
			item.ActionID != "" && (!validText(item.ActionLabel, 40) || len(item.Platforms) != 1 || item.Platforms[0] != "android") {
			return errors.New("本版亮点按钮配置无效")
		}
	}
	encoded, err := json.Marshal(guide)
	if err != nil || len(encoded) > 12*1024 {
		return errors.New("本版亮点内容过大")
	}
	return nil
}

// UpdateWhatsNew changes only editorial content; it never revalidates or modifies an APK.
func (s *AppReleaseStore) UpdateWhatsNew(ctx context.Context, id int64, guide *ReleaseWhatsNew) error {
	if err := ValidateReleaseWhatsNew(guide); err != nil {
		return err
	}
	var value any
	if guide != nil {
		encoded, err := json.Marshal(guide)
		if err != nil {
			return err
		}
		value = string(encoded)
	}
	result, err := s.pool.Exec(ctx, `UPDATE app_releases SET whats_new=$2::jsonb, updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, id, value)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
