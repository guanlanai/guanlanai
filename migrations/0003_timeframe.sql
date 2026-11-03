-- 用户常用周期与会话最近使用周期。
-- 空值表示未设置偏好，分析时回退到 DEFAULT_TIMEFRAME。

ALTER TABLE users ADD COLUMN preferred_timeframe TEXT;
ALTER TABLE sessions ADD COLUMN current_timeframe TEXT;
