-- 收藏课程表
CREATE TABLE IF NOT EXISTS grade_favorites (
  user_id    BIGINT NOT NULL REFERENCES zonenan_users(id) ON DELETE CASCADE,
  profile_id BIGINT NOT NULL REFERENCES grade_course_profiles(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (user_id, profile_id)
);

