package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// evaluateRisk 在关键埋点后跑一次轻量风控规则,命中即记 risk_flag;
// 累计分达阈值则自动封号/封设备。白名单/admin 账号跳过。
//
// 规则(可按需扩展):
//   - 高频:同设备近 1 分钟给分 search 次数过多 → 疑似脚本刷接口。
//   - 多号同设备:同设备近 1 小时关联的不同账号数过多 → 疑似批量注册白嫖。
func (s *Server) evaluateRisk(r *http.Request, userID *int64, deviceFP, category, action string) {
	ctx := r.Context()

	// 白名单/admin 免风控。
	if userID != nil && s.users.IsPrivileged(ctx, *userID) {
		return
	}
	if deviceFP == "" {
		return
	}

	var hits int

	// 规则1:高频 search(脚本刷接口)。
	if category == "grade" && action == "search" {
		n, _ := s.telemetry.CountRecentEventsByDevice(ctx, deviceFP, "grade", "search", time.Minute)
		threshold := s.setting.GetInt(ctx, "risk_search_per_min", 30)
		if threshold > 0 && n >= threshold {
			_ = s.telemetry.AddRiskFlag(ctx, userID, deviceFP, "high_freq_search",
				40, fmt.Sprintf("1分钟内 search %d 次(阈值 %d)", n, threshold))
			hits += 40
		}
	}

	// 规则2:多账号同设备(批量注册白嫖信号)。
	nu, _ := s.telemetry.CountDistinctUsersByDevice(ctx, deviceFP, time.Hour)
	multiThreshold := s.setting.GetInt(ctx, "risk_accounts_per_device", 5)
	if multiThreshold > 0 && nu >= multiThreshold {
		_ = s.telemetry.AddRiskFlag(ctx, userID, deviceFP, "multi_account_device",
			50, fmt.Sprintf("1小时内同设备 %d 个账号(阈值 %d)", nu, multiThreshold))
		hits += 50
	}

	if hits == 0 {
		return
	}
	s.maybeAutoBan(ctx, userID, deviceFP)
}

// maybeAutoBan 累计未处置风险分达阈值时自动封设备(及账号)。阈值 0 = 不自动封。
func (s *Server) maybeAutoBan(ctx context.Context, userID *int64, deviceFP string) {
	autoScore := s.setting.GetInt(ctx, "risk_auto_ban_score", 100)
	if autoScore <= 0 {
		return
	}
	if userID == nil {
		return
	}
	total, err := s.telemetry.UserRiskScore(ctx, *userID, 24*time.Hour)
	if err != nil || total < autoScore {
		return
	}
	// 再次确认非特权账号后封禁。
	if s.users.IsPrivileged(ctx, *userID) {
		return
	}
	_ = s.users.SetBanned(ctx, *userID, true)
	if deviceFP != "" {
		_ = s.telemetry.BlockDevice(ctx, deviceFP, fmt.Sprintf("auto: risk score %d", total))
	}
}
