-- Verified, publicly downloadable classroom data packages. The binary remains in R2;
-- PostgreSQL only records metadata calculated by the backend from the uploaded URL.
CREATE TABLE IF NOT EXISTS classroom_data_artifacts (
  id           BIGSERIAL PRIMARY KEY,
  term_id      VARCHAR(16) NOT NULL CHECK (term_id ~ '^[0-9]{4}-[0-9]{4}-[12]$'),
  display_name TEXT NOT NULL DEFAULT '',
  first_monday DATE NOT NULL,
  total_weeks  SMALLINT NOT NULL CHECK (total_weeks BETWEEN 1 AND 32),
  source_url   TEXT NOT NULL,
  sha256       CHAR(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  size_bytes   BIGINT NOT NULL CHECK (size_bytes > 16),
  verified_at  TIMESTAMPTZ NOT NULL,
  active       BOOLEAN NOT NULL DEFAULT TRUE,
  archived_at  TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_classroom_data_artifacts_active_term
  ON classroom_data_artifacts(term_id) WHERE active = TRUE;
CREATE INDEX IF NOT EXISTS idx_classroom_data_artifacts_manifest
  ON classroom_data_artifacts(active, first_monday, id DESC);

