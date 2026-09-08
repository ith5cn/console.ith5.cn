-- +goose Up
-- +goose NO TRANSACTION

-- viewer 是管理后台的只读演示角色：可以浏览管理数据，但所有写请求都会
-- 在 HTTP 中间件中被拒绝。它与普通 member 分开，避免扩大员工账号权限。
ALTER TYPE user_role ADD VALUE IF NOT EXISTS 'viewer';

-- +goose Down
-- +goose NO TRANSACTION

-- PostgreSQL 不支持安全地从枚举中删除单个值。
SELECT 1;
