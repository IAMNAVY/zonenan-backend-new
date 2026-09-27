-- 在线协议文档(用户协议/隐私政策/给分授权提示),服务端可更新、带版本。
-- App 拉取最新版展示;版本号变化可用于提示用户重新阅读/同意。
CREATE TABLE IF NOT EXISTS legal_documents (
  doc_type     TEXT NOT NULL,            -- user_agreement | privacy_policy | grade_consent
  version      INTEGER NOT NULL,         -- 单调递增,最大版本为当前生效版本
  title        TEXT NOT NULL DEFAULT '',
  content      TEXT NOT NULL DEFAULT '', -- Markdown 正文
  published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (doc_type, version)
);

CREATE INDEX IF NOT EXISTS idx_legal_docs_type_ver
  ON legal_documents(doc_type, version DESC);

