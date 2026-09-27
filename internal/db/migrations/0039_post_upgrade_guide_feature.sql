-- “本版亮点”客户端入口的后端云控，首次部署默认关闭。
INSERT INTO app_settings(key, value) VALUES
  ('feature_post_upgrade_guide_state', 'disabled')
ON CONFLICT (key) DO NOTHING;

