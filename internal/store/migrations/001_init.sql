-- Schema baseline v1 (docs/06 §3). Idempotent: safe to re-run on restart.
CREATE TABLE IF NOT EXISTS app_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
    id            TEXT PRIMARY KEY,
    state         TEXT NOT NULL,
    input         TEXT NOT NULL,
    input_kind    TEXT NOT NULL,
    resource_id   TEXT,
    priority      INTEGER NOT NULL DEFAULT 0,
    error_kind    TEXT,
    error_message TEXT,
    bytes_done    INTEGER NOT NULL DEFAULT 0,
    bytes_total   INTEGER NOT NULL DEFAULT 0,
    bytes_uploaded INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    completed_at  INTEGER
);
CREATE INDEX IF NOT EXISTS idx_tasks_state_priority ON tasks(state, priority DESC, created_at);

CREATE TABLE IF NOT EXISTS resources (
    id         TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    engine     TEXT NOT NULL DEFAULT 'go',
    options    TEXT NOT NULL DEFAULT '{}',
    headers    TEXT NOT NULL DEFAULT '{}',
    cred_ref   TEXT
);

CREATE TABLE IF NOT EXISTS files (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    idx         INTEGER NOT NULL,
    rel_path    TEXT NOT NULL,
    size        INTEGER NOT NULL DEFAULT 0,
    selected    INTEGER NOT NULL DEFAULT 1,
    final_path  TEXT,
    UNIQUE(resource_id, idx)
);

CREATE TABLE IF NOT EXISTS mirrors (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    url         TEXT NOT NULL,
    priority    INTEGER NOT NULL DEFAULT 0,
    geo         TEXT,
    weight      REAL NOT NULL DEFAULT 1.0,
    failures    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS protocol_identity (
    resource_id   TEXT PRIMARY KEY REFERENCES resources(id) ON DELETE CASCADE,
    infohash_v1   TEXT,
    btmh_v2       TEXT,
    private_flag  INTEGER NOT NULL DEFAULT 0,
    trackers_json TEXT,
    passkey_ref   TEXT,
    peer_id       TEXT,
    ed2k_hash     TEXT,
    aich_root     TEXT,
    user_hash     TEXT,
    ident_key_ref TEXT,
    credits_ref   TEXT,
    variant_id    TEXT,
    is_adaptive   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS checksums (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    file_idx    INTEGER NOT NULL DEFAULT 0,
    algo        TEXT NOT NULL,
    scope       TEXT NOT NULL,
    chunk_index INTEGER,
    value       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_checksums_lookup ON checksums(resource_id, scope, chunk_index);

CREATE TABLE IF NOT EXISTS chunks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id      TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    file_idx     INTEGER NOT NULL DEFAULT 0,
    idx          INTEGER NOT NULL,
    start        INTEGER NOT NULL,
    length       INTEGER NOT NULL,
    state        TEXT NOT NULL,
    attempt      INTEGER NOT NULL DEFAULT 0,
    mirror_id    INTEGER,
    hash_ok      INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL,
    UNIQUE(task_id, file_idx, idx)
);
CREATE INDEX IF NOT EXISTS idx_chunks_pending ON chunks(task_id, state);

CREATE TABLE IF NOT EXISTS task_options (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    key     TEXT NOT NULL,
    value   TEXT NOT NULL,
    PRIMARY KEY(task_id, key)
);

CREATE TABLE IF NOT EXISTS events (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT,
    type    TEXT NOT NULL,
    payload TEXT NOT NULL,
    ts      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts);

CREATE TABLE IF NOT EXISTS sidecars (
    name         TEXT PRIMARY KEY,
    version      TEXT,
    min_version  TEXT,
    license      TEXT,
    sha256       TEXT,
    source_url   TEXT,
    buildconf    TEXT,
    status       TEXT NOT NULL DEFAULT 'absent',
    capabilities TEXT NOT NULL DEFAULT '[]'
);
