-- 私聊多会话 + 群组按成员隔离。
-- 旧 sessions 的 chat_id/user_id 唯一约束无法直接删除，只能重建表。

CREATE TABLE sessions_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chat_id INTEGER NOT NULL,
    scope TEXT NOT NULL CHECK (scope IN ('private', 'group')),
    title TEXT,
    current_symbol TEXT,
    current_market TEXT,
    summary TEXT,
    summary_tokens INTEGER NOT NULL DEFAULT 0,
    last_analysis_json TEXT,
    generation INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_active_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO sessions_new (
    id, user_id, chat_id, scope, title, current_symbol, current_market,
    summary, summary_tokens, last_analysis_json, generation, created_at, last_active_at
)
SELECT
    id, user_id, chat_id, 'private',
    COALESCE(NULLIF(current_symbol, ''), '默认会话'),
    current_symbol, current_market, summary, summary_tokens,
    last_analysis_json, generation, CURRENT_TIMESTAMP, last_active_at
FROM sessions;

CREATE TABLE messages_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES sessions_new(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content TEXT NOT NULL,
    tokens INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO messages_new (id, session_id, role, content, tokens, created_at)
SELECT id, session_id, role, content, tokens, created_at FROM messages;

DROP TABLE messages;
DROP TABLE sessions;

ALTER TABLE sessions_new RENAME TO sessions;
ALTER TABLE messages_new RENAME TO messages;

CREATE INDEX idx_messages_session_id ON messages(session_id, id);
CREATE INDEX idx_sessions_user_chat ON sessions(user_id, chat_id, scope);
CREATE UNIQUE INDEX ux_sessions_group ON sessions(chat_id, user_id)
    WHERE scope = 'group';

CREATE TABLE chat_state (
    chat_id INTEGER PRIMARY KEY,
    active_session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO chat_state (chat_id, active_session_id)
SELECT chat_id, id FROM sessions WHERE scope = 'private';
