-- +goose Up
-- +goose NO TRANSACTION

-- 新增 kind='agent'（PRD C34）。
--
-- 00001 里的 CREATE TYPE 已经带上了 'agent'，这条是给**已经建过库**的部署用的：
-- goose 按版本号记账，改过的 00001 不会重跑，只改它等于「新库有、老库没有」。
-- 两边都做，且用 IF NOT EXISTS，因此无论从哪条路径来结果都一致。
--
-- NO TRANSACTION 是必需的：ALTER TYPE ... ADD VALUE 在事务里执行时，
-- PostgreSQL 不允许同一事务内再使用这个新值，goose 默认把每个迁移包进事务，
-- 会让后续引用该值的语句失败。
ALTER TYPE bundle_kind ADD VALUE IF NOT EXISTS 'agent';

-- +goose Down
-- +goose NO TRANSACTION

-- PostgreSQL 不支持从枚举里删值。回滚只能重建整个类型，
-- 那要求先把所有引用列改掉，风险远大于收益——留一个未被使用的枚举值是无害的。
SELECT 1;
