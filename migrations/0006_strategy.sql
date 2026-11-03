-- 用户分析策略：single（单周期）或 triple_screen（三重滤网）。
-- 空值表示使用单周期。
ALTER TABLE users ADD COLUMN preferred_strategy TEXT;
