-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS documents (
  id            VARCHAR(64)  NOT NULL,
  team_id       BIGINT       NOT NULL,
  resource_type VARCHAR(64),
  bucket_name   VARCHAR(255),
  object_key    VARCHAR(512),
  mime_type     VARCHAR(128),
  size          BIGINT       NOT NULL DEFAULT 0,
  original_name VARCHAR(255),
  created_by_id BIGINT,
  status        VARCHAR(32)  NOT NULL DEFAULT 'pending',
  created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
  PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS idx_documents_team_id ON documents (team_id);
CREATE INDEX IF NOT EXISTS idx_documents_object_key ON documents (object_key);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS documents;
-- +goose StatementEnd
