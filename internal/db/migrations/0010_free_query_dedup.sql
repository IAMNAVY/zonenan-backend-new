-- 免费查询额度按「课程名」去重:同一(账号/设备, 课程名)只计一次。
-- 修复原 bug:suggest/semesters/search 翻页/切排序都会重复扣费。
-- 改为:仅在 search(选中某门课名后)按课名去重计一次;suggest/semesters/detail 只校验不计费。
ALTER TABLE grade_free_queries ADD COLUMN IF NOT EXISTS course_name TEXT NOT NULL DEFAULT '';
-- H3:记录发起 IP,用于每 IP 每日免费查询上限,防批量注册白嫖。
ALTER TABLE grade_free_queries ADD COLUMN IF NOT EXISTS ip TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_grade_free_queries_ip_day ON grade_free_queries(ip, queried_at);

-- 已扣费记录若无课名(历史数据)保持原样;新记录按 (user_id, course_name) / (device, course_name) 去重。
CREATE INDEX IF NOT EXISTS idx_grade_free_queries_user_course
  ON grade_free_queries(user_id, course_name);
CREATE INDEX IF NOT EXISTS idx_grade_free_queries_device_course
  ON grade_free_queries(device_fingerprint, course_name);

