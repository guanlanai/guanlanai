CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    telegram_user_id INTEGER NOT NULL UNIQUE,
    username TEXT,
    key_ciphertext BLOB,
    key_nonce BLOB,
    key_version INTEGER,
    key_updated_at DATETIME,
    key_status TEXT NOT NULL DEFAULT 'missing' CHECK (key_status IN ('missing', 'valid', 'invalid')),
    api_base_url TEXT NOT NULL DEFAULT 'https://api.openai.com/v1',
    api_base_url_updated_at DATETIME,
    model TEXT,
    mode TEXT NOT NULL DEFAULT 'swing' CHECK (mode IN ('short', 'swing')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'banned')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    chat_id INTEGER NOT NULL UNIQUE,
    current_symbol TEXT,
    current_market TEXT,
    summary TEXT,
    summary_tokens INTEGER NOT NULL DEFAULT 0,
    last_analysis_json TEXT,
    generation INTEGER NOT NULL DEFAULT 0,
    last_active_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content TEXT NOT NULL,
    tokens INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_messages_session_id ON messages(session_id, id);

CREATE TABLE usage_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('analysis', 'chat', 'summarize')),
    model TEXT,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    estimated_cost_usd REAL NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK (status IN ('ok', 'error')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_usage_events_user_created ON usage_events(user_id, created_at);
