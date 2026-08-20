package repository

const CurrentSchemaVersion = 6

const schemaV6 = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,
    raw_uri TEXT NOT NULL UNIQUE,
    canonical_identity TEXT NOT NULL,
    endpoint_fingerprint TEXT NOT NULL,
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    disabled BOOLEAN NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS node_sources (
    node_id TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_id TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (node_id, source_type, source_id),
    FOREIGN KEY(node_id) REFERENCES nodes(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_node_sources_owner ON node_sources(source_type, source_id);

CREATE TABLE IF NOT EXISTS node_health (
    node_id TEXT PRIMARY KEY,
    success_count INTEGER NOT NULL DEFAULT 0,
    fail_count INTEGER NOT NULL DEFAULT 0,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_test_ms REAL NOT NULL DEFAULT 0,
    last_test_error TEXT NOT NULL DEFAULT '',
    last_success_at INTEGER NOT NULL DEFAULT 0,
    last_fail_at INTEGER NOT NULL DEFAULT 0,
    cooldown_until INTEGER NOT NULL DEFAULT 0,
    last_429_at INTEGER NOT NULL DEFAULT 0,
    rate_limit_count INTEGER NOT NULL DEFAULT 0,
    last_sub_healthy_at INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY(node_id) REFERENCES nodes(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_nodes_identity ON nodes(canonical_identity);

CREATE TABLE IF NOT EXISTS global_proxies (
    id TEXT PRIMARY KEY,
    raw_uri TEXT NOT NULL UNIQUE,
    canonical_identity TEXT NOT NULL,
    endpoint_fingerprint TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT '',
    disabled BOOLEAN NOT NULL DEFAULT 0,
    pinned BOOLEAN NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_global_proxies_identity ON global_proxies(canonical_identity);
CREATE INDEX IF NOT EXISTS idx_global_proxies_endpoint ON global_proxies(endpoint_fingerprint);
CREATE UNIQUE INDEX IF NOT EXISTS uq_global_proxies_identity ON global_proxies(canonical_identity);

CREATE TABLE IF NOT EXISTS global_proxy_sources (
    global_proxy_id TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_id TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (global_proxy_id, source_type, source_id),
    FOREIGN KEY(global_proxy_id) REFERENCES global_proxies(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS global_proxy_health (
    global_proxy_id TEXT PRIMARY KEY,
    cooldown_until INTEGER NOT NULL DEFAULT 0,
    last_test_ok BOOLEAN NOT NULL DEFAULT 0,
    last_test_ms REAL NOT NULL DEFAULT 0,
    last_test_at INTEGER NOT NULL DEFAULT 0,
    last_test_error TEXT NOT NULL DEFAULT '',
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY(global_proxy_id) REFERENCES global_proxies(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS subscription_user_agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    user_agent TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    user_agent TEXT NOT NULL DEFAULT '',
    custom_ua_id TEXT,
    update_interval INTEGER NOT NULL DEFAULT 60,
    adopt_manual BOOLEAN NOT NULL DEFAULT 0,
    last_update_time INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 0,
    generation TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(custom_ua_id) REFERENCES subscription_user_agents(id)
);

CREATE TABLE IF NOT EXISTS tool_states (
    external_call_id TEXT PRIMARY KEY,
    response_id TEXT NOT NULL DEFAULT '',
    conversation_id TEXT NOT NULL DEFAULT '',
    upstream_operation TEXT NOT NULL,
    opaque_blob BLOB NOT NULL,
    expires_at INTEGER NOT NULL,
    consumed_at INTEGER NOT NULL DEFAULT 0,
    transcript_hash TEXT NOT NULL,
    consume_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_tool_states_expiry ON tool_states(expires_at);
CREATE INDEX IF NOT EXISTS idx_tool_states_response ON tool_states(response_id);

CREATE TABLE IF NOT EXISTS responses (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    model TEXT NOT NULL,
    request_json BLOB NOT NULL,
    input_json BLOB NOT NULL,
    output_json BLOB NOT NULL DEFAULT '[]',
    usage_json BLOB NOT NULL DEFAULT '{}',
    error_json BLOB NOT NULL DEFAULT 'null',
    incomplete_json BLOB NOT NULL DEFAULT 'null',
    previous_response_id TEXT NOT NULL DEFAULT '',
    conversation_id TEXT NOT NULL DEFAULT '',
    metadata_json BLOB NOT NULL DEFAULT '{}',
    upstream_operation_id TEXT NOT NULL DEFAULT '',
    store BOOLEAN NOT NULL DEFAULT 1,
    background BOOLEAN NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    completed_at INTEGER NOT NULL DEFAULT 0,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_responses_expiry ON responses(expires_at);
CREATE INDEX IF NOT EXISTS idx_responses_conversation ON responses(conversation_id, created_at);

CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    metadata_json BLOB NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    next_ordinal INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS conversation_items (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    item_json BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(conversation_id, ordinal),
    FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_conversation_items_page ON conversation_items(conversation_id, ordinal);

CREATE TABLE IF NOT EXISTS interactions (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    agent TEXT NOT NULL DEFAULT '',
    request_json BLOB NOT NULL,
    steps_json BLOB NOT NULL DEFAULT '[]',
    usage_json BLOB NOT NULL DEFAULT '{}',
    error_json BLOB NOT NULL DEFAULT 'null',
    previous_interaction_id TEXT NOT NULL DEFAULT '',
    labels_json BLOB NOT NULL DEFAULT '{}',
    store BOOLEAN NOT NULL DEFAULT 1,
    background BOOLEAN NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_interactions_expiry ON interactions(expires_at);

CREATE TABLE IF NOT EXISTS resource_events (
    resource_kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    sequence_number INTEGER NOT NULL,
    event_id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL,
    event_json BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY(resource_kind, resource_id, sequence_number)
);
CREATE INDEX IF NOT EXISTS idx_resource_events_resume ON resource_events(resource_kind, resource_id, event_id);
CREATE TABLE IF NOT EXISTS resource_event_counters (
    resource_kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    next_sequence INTEGER NOT NULL,
    PRIMARY KEY(resource_kind, resource_id)
);

CREATE TABLE IF NOT EXISTS background_jobs (
    id TEXT PRIMARY KEY,
    resource_kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    status TEXT NOT NULL,
    error_json BLOB NOT NULL DEFAULT 'null',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    UNIQUE(resource_kind, resource_id)
);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    endpoint TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    body_hash TEXT NOT NULL,
    resource_kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    PRIMARY KEY(endpoint, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_idempotency_expiry ON idempotency_keys(expires_at);

CREATE TABLE IF NOT EXISTS local_files (
    id TEXT PRIMARY KEY,
    dialect TEXT NOT NULL,
    name TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    purpose TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    storage_path TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'processed',
    metadata_json BLOB NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_local_files_created ON local_files(created_at, id);
CREATE INDEX IF NOT EXISTS idx_local_files_expiry ON local_files(expires_at);

CREATE TABLE IF NOT EXISTS cached_contents (
    id TEXT PRIMARY KEY,
    model TEXT NOT NULL DEFAULT '',
    contents_json BLOB NOT NULL,
    system_instruction_json BLOB NOT NULL DEFAULT 'null',
    tools_json BLOB NOT NULL DEFAULT '[]',
    metadata_json BLOB NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cached_contents_expiry ON cached_contents(expires_at);

CREATE TABLE IF NOT EXISTS batches (
    id TEXT PRIMARY KEY,
    dialect TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    input_file_id TEXT NOT NULL DEFAULT '',
    output_file_id TEXT NOT NULL DEFAULT '',
    error_file_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    request_counts_json BLOB NOT NULL DEFAULT '{}',
    metadata_json BLOB NOT NULL DEFAULT '{}',
    error_json BLOB NOT NULL DEFAULT 'null',
    created_at INTEGER NOT NULL,
    in_progress_at INTEGER NOT NULL DEFAULT 0,
    completed_at INTEGER NOT NULL DEFAULT 0,
    cancelled_at INTEGER NOT NULL DEFAULT 0,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_batches_created ON batches(created_at, id);
CREATE INDEX IF NOT EXISTS idx_batches_expiry ON batches(expires_at);

CREATE TABLE IF NOT EXISTS chat_completions (
    id TEXT PRIMARY KEY,
    model TEXT NOT NULL,
    status TEXT NOT NULL,
    request_json BLOB NOT NULL,
    response_json BLOB NOT NULL,
    messages_json BLOB NOT NULL,
    metadata_json BLOB NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_completions_created ON chat_completions(created_at, id);
CREATE INDEX IF NOT EXISTS idx_chat_completions_expiry ON chat_completions(expires_at);
`
