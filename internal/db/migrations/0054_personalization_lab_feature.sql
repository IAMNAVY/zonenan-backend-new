-- 个性化实验室入口的后端云控，首次部署默认关闭。
INSERT INTO app_settings(key, value) VALUES
  ('feature_personalization_lab_state', 'disabled')
ON CONFLICT (key) DO NOTHING;
