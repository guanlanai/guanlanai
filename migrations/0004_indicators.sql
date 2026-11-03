-- 用户常用指标组。空值表示使用默认指标集合。
ALTER TABLE users ADD COLUMN preferred_indicators TEXT;
