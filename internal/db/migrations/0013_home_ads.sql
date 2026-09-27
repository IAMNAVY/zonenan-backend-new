-- 广告位/运营位:「我的」页 账户设置/应用设置/关于 按钮下方,单独一块,可多条。
-- 每条:左侧 icon + 主副标题,右侧折线向右箭头。点击按 open_mode 打开。
--   open_mode: in_app(应用内 WebView) | browser(系统浏览器) | scheme(自定义协议如 weixin://)
CREATE TABLE IF NOT EXISTS home_ads (
  id         BIGSERIAL PRIMARY KEY,
  icon_url   TEXT NOT NULL DEFAULT '',      -- 左侧图标(外链)
  title      TEXT NOT NULL DEFAULT '',      -- 主标题
  subtitle   TEXT NOT NULL DEFAULT '',      -- 副标题
  link       TEXT NOT NULL DEFAULT '',      -- 目标链接/scheme
  open_mode  TEXT NOT NULL DEFAULT 'in_app',-- in_app | browser | scheme
  sort       INT NOT NULL DEFAULT 0,        -- 排序(大在前)
  active     BOOLEAN NOT NULL DEFAULT TRUE,
  starts_at  TIMESTAMPTZ,
  ends_at    TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_home_ads_active ON home_ads(active);

