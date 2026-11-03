-- 用户每笔最大亏损（USDT）。空值表示未设置，不提供仓位建议。
ALTER TABLE users ADD COLUMN risk_per_trade REAL;
