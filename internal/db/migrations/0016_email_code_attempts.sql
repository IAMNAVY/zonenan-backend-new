-- H2:验证码失败计数,达上限即作废,防 30 分钟窗口内无限爆破。
ALTER TABLE email_codes ADD COLUMN IF NOT EXISTS attempts INT NOT NULL DEFAULT 0;

