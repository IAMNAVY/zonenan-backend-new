-- 给分:课程档案 / 匿名成绩 / 关联 / 授权(同意状态机) / 评教。
-- schema 对齐旧库(便于旧数据迁移),授权表改为持续同意模型。

CREATE TABLE IF NOT EXISTS grade_course_profiles (
  id              BIGSERIAL PRIMARY KEY,
  semester        TEXT NOT NULL,
  course_name     TEXT NOT NULL,
  teacher_name    TEXT NOT NULL,
  regular_ratio   TEXT NOT NULL DEFAULT '',
  final_ratio     TEXT NOT NULL DEFAULT '',
  ratio_signature TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (semester, course_name, teacher_name, ratio_signature)
);

CREATE TABLE IF NOT EXISTS grade_user_scores (
  id            BIGSERIAL PRIMARY KEY,
  user_hash     TEXT NOT NULL,   -- sha256(学号|GRADE_PEPPER),服务端算
  course_key    TEXT NOT NULL,   -- sha256(semester::course::teacher::ratioSig)
  regular_score NUMERIC(6,2),
  final_score   NUMERIC(6,2),
  total_score   NUMERIC(6,2),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (user_hash, course_key)
);
CREATE INDEX IF NOT EXISTS idx_grade_user_scores_hash ON grade_user_scores(user_hash);

CREATE TABLE IF NOT EXISTS grade_score_links (
  profile_id BIGINT NOT NULL REFERENCES grade_course_profiles(id) ON DELETE CASCADE,
  score_id   BIGINT NOT NULL REFERENCES grade_user_scores(id) ON DELETE CASCADE,
  PRIMARY KEY (profile_id, score_id)
);

-- 授权=持续同意状态机(非过期倒计时)。学号 hash 从 cas identity 取,不在此重复存。
CREATE TABLE IF NOT EXISTS grade_authorizations (
  user_id            BIGINT PRIMARY KEY REFERENCES zonenan_users(id) ON DELETE CASCADE,
  consent_status     TEXT NOT NULL DEFAULT 'active',  -- active | revoked
  first_consented_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  consent_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_sync_at       TIMESTAMPTZ,
  last_full_sync_at  TIMESTAMPTZ,
  device_fingerprint TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS grade_evaluations (
  id                 BIGSERIAL PRIMARY KEY,
  profile_id         BIGINT NOT NULL REFERENCES grade_course_profiles(id) ON DELETE CASCADE,
  user_id            BIGINT REFERENCES zonenan_users(id) ON DELETE SET NULL,
  rating             NUMERIC(2,1) NOT NULL DEFAULT 5.0,
  comment            TEXT NOT NULL DEFAULT '',
  is_anonymous       BOOLEAN NOT NULL DEFAULT FALSE,
  is_hidden          BOOLEAN NOT NULL DEFAULT FALSE,
  is_deleted_by_user BOOLEAN NOT NULL DEFAULT FALSE,
  edit_count         INT NOT NULL DEFAULT 0,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_grade_evals_profile ON grade_evaluations(profile_id, is_hidden);
CREATE UNIQUE INDEX IF NOT EXISTS uk_grade_eval_profile_user ON grade_evaluations(profile_id, user_id);

