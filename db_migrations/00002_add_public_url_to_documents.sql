-- +goose Up
-- +goose StatementBegin
ALTER TABLE documents ADD COLUMN IF NOT EXISTS public_url VARCHAR(1024);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE documents DROP COLUMN IF EXISTS public_url;
-- +goose StatementEnd
