-- 新增运行时配置项(存在则不覆盖)。
INSERT INTO app_settings(key, value) VALUES
  ('app_apk_sha256', ''),                     -- 最新 APK 的 SHA256(客户端下载后校验)
  ('grade_free_query_daily_ip_limit', '2'),   -- 每 IP 每日免费查询上限(M2:校园网出口IP高度收敛,收紧到2防换设备指纹放大;已上线库需在 /panel 手动改)
  ('risk_auto_ban_score', '100'),             -- 风险分达到即自动封禁(0=不自动封)
  ('email_code_ttl_minutes', '5'),            -- 验证码有效期(分钟)
  ('email_code_resend_seconds', '300')        -- 验证码重发冷却(秒,默认5分钟)
ON CONFLICT (key) DO NOTHING;

