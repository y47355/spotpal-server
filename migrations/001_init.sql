-- 001_init.sql · SQLite 零依赖版初始 schema
-- 约定：TEXT 主键 + ULID；JSONB→TEXT 存 JSON；时间戳 TEXT（ISO 8601 UTC）

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    phone         TEXT UNIQUE NOT NULL,
    nickname      TEXT NOT NULL,
    avatar_url    TEXT,
    city          TEXT,
    credit_score  INTEGER DEFAULT 620,
    status        TEXT DEFAULT 'ACTIVE',
    created_at    TEXT DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS user_interest (
    user_id    TEXT NOT NULL REFERENCES users(id),
    tag        TEXT NOT NULL,
    weight     REAL DEFAULT 1.0,
    updated_at TEXT DEFAULT (datetime('now')),
    PRIMARY KEY (user_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_interest_tag ON user_interest(tag);

CREATE TABLE IF NOT EXISTS user_pref (
    user_id     TEXT PRIMARY KEY REFERENCES users(id),
    cost_mode   TEXT DEFAULT 'FLEXIBLE',
    time_slots  TEXT,
    role_pref   TEXT DEFAULT 'ANY'
);

CREATE TABLE IF NOT EXISTS squads (
    id            TEXT PRIMARY KEY,
    initiator_id  TEXT NOT NULL REFERENCES users(id),
    status        TEXT NOT NULL,
    activity      TEXT NOT NULL,
    time_window   TEXT NOT NULL,
    cost_mode     TEXT,
    expire_at     TEXT,
    created_at    TEXT DEFAULT (datetime('now')),
    updated_at    TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_squads_status ON squads(status, expire_at);

CREATE TABLE IF NOT EXISTS candidate_pool (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id),
    candidate_id    TEXT NOT NULL REFERENCES users(id),
    intent_snapshot TEXT NOT NULL,
    status          TEXT DEFAULT 'WAITING',
    created_at      TEXT DEFAULT (datetime('now')),
    UNIQUE (user_id, candidate_id)
);

CREATE TABLE IF NOT EXISTS negotiations (
    id              TEXT PRIMARY KEY,
    squad_id        TEXT REFERENCES squads(id),
    candidate_id    TEXT NOT NULL,
    round           INTEGER DEFAULT 0,
    status          TEXT DEFAULT 'ACTIVE',
    created_at      TEXT DEFAULT (datetime('now')),
    expire_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS negotiation_rounds (
    id             TEXT PRIMARY KEY,
    negotiation_id TEXT NOT NULL REFERENCES negotiations(id),
    round_no       INTEGER NOT NULL,
    proposal       TEXT NOT NULL,
    feedback       TEXT,
    created_at     TEXT DEFAULT (datetime('now')),
    UNIQUE (negotiation_id, round_no)
);

CREATE TABLE IF NOT EXISTS reviews (
    id         TEXT PRIMARY KEY,
    squad_id    TEXT REFERENCES squads(id),
    reviewer   TEXT NOT NULL,
    reviewee   TEXT NOT NULL,
    rating     INTEGER CHECK (rating BETWEEN 1 AND 5),
    tags        TEXT,
    comment    TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    UNIQUE (squad_id, reviewer)
);

CREATE TABLE IF NOT EXISTS settle_orders (
    id          TEXT PRIMARY KEY,
    squad_id    TEXT REFERENCES squads(id),
    mode        TEXT NOT NULL,
    amount      INTEGER NOT NULL,
    wx_order_id TEXT,
    status      TEXT DEFAULT 'CREATED',
    created_at  TEXT DEFAULT (datetime('now'))
);

-- 轻量化承接表
CREATE TABLE IF NOT EXISTS timeouts (
    id          TEXT PRIMARY KEY,
    due_at      TEXT NOT NULL,
    kind        TEXT NOT NULL,
    payload     TEXT NOT NULL,
    claimed_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_timeouts_due ON timeouts(due_at) WHERE claimed_at IS NULL;

CREATE TABLE IF NOT EXISTS event_outbox (
    id         TEXT PRIMARY KEY,
    topic      TEXT NOT NULL,
    payload    TEXT NOT NULL,
    created_at TEXT DEFAULT (datetime('now')),
    delivered  INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_outbox_undelivered ON event_outbox(delivered, created_at);

CREATE TABLE IF NOT EXISTS ws_outbox (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    seq        INTEGER NOT NULL,
    type       TEXT NOT NULL,
    payload    TEXT NOT NULL,
    created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_ws_outbox_user_seq ON ws_outbox(user_id, seq);

CREATE TABLE IF NOT EXISTS dead_letter (
    id          TEXT PRIMARY KEY,
    topic       TEXT NOT NULL,
    payload     TEXT NOT NULL,
    err         TEXT,
    created_at  TEXT DEFAULT (datetime('now'))
);

-- 位置数据零落库：实时坐标只进内存表（见 internal/module/match/geo.go）
