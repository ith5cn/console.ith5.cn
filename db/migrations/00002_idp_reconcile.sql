-- +goose Up
-- +goose StatementBegin

-- 记录 OIDC 成员最近一次登录时 IdP 给出的用户组，供「对账」在映射表改动后重算角色，
-- 不必等成员下次登录。local 账号两列为 NULL。
ALTER TABLE users
    ADD COLUMN idp_groups    text[],
    ADD COLUMN idp_synced_at timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN idp_groups, DROP COLUMN idp_synced_at;
-- +goose StatementEnd
