-- 公告:三种形态 + 重要程度。
--   kind:  modal(弹窗,类更新,可设强制阅读秒数,读完记版本不再弹)
--          status_card(状态页卡片公告)
--          mine_section(「我的」页 账户/应用/关于 与广告位之间的纯文本区块,正文可含超链接)
--   level: info | important | critical(重要程度,前端据此配色/排序)
CREATE TABLE IF NOT EXISTS announcements (
  id                BIGSERIAL PRIMARY KEY,
  kind              TEXT NOT NULL DEFAULT 'status_card',   -- modal | status_card | mine_section
  level             TEXT NOT NULL DEFAULT 'info',          -- info | important | critical
  title             TEXT NOT NULL DEFAULT '',
  body              TEXT NOT NULL DEFAULT '',               -- 正文(mine_section 可含超链接 markdown)
  force_read_seconds INT NOT NULL DEFAULT 0,                -- modal 专用:强制阅读秒数(0=不强制)
  dismissible       BOOLEAN NOT NULL DEFAULT TRUE,          -- modal 是否可关闭
  sort              INT NOT NULL DEFAULT 0,                 -- 展示排序(大在前)
  active            BOOLEAN NOT NULL DEFAULT TRUE,
  starts_at         TIMESTAMPTZ,                            -- 生效起(NULL=立即)
  ends_at           TIMESTAMPTZ,                            -- 生效止(NULL=永久)
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_announcements_active ON announcements(active, kind);

