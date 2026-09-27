-- 预置给分相关可配参数及反馈地址。
INSERT INTO app_settings(key, value) VALUES
  ('grade_free_query_limit', '2'),
  ('grade_contrib_grace_days', '30'),
  ('app_feedback_url', '')
ON CONFLICT (key) DO NOTHING;

