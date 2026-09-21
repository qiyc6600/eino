CREATE TABLE IF NOT EXISTS agent_users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    roles JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS agent_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    username TEXT NOT NULL,
    roles JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS agent_sessions_user_idx ON agent_sessions(user_id);
CREATE INDEX IF NOT EXISTS agent_sessions_expiry_idx ON agent_sessions(expires_at);

CREATE TABLE IF NOT EXISTS agent_threads (
    user_id TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    messages JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, thread_id)
);

CREATE TABLE IF NOT EXISTS agent_checkpoints (
    user_id TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    step INTEGER NOT NULL,
    data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(user_id, thread_id, run_id, step)
);

CREATE INDEX IF NOT EXISTS agent_checkpoints_thread_idx
    ON agent_checkpoints(user_id, thread_id, created_at DESC);

CREATE TABLE IF NOT EXISTS agent_memories (
    user_id TEXT NOT NULL,
    memory_key TEXT NOT NULL,
    data JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, memory_key)
);

CREATE TABLE IF NOT EXISTS agent_approvals (
    interrupt_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    status TEXT NOT NULL,
    phase TEXT NOT NULL DEFAULT '',
    approved BOOLEAN,
    reason TEXT NOT NULL DEFAULT '',
    claim_token TEXT NOT NULL DEFAULT '',
    data JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    decided_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS agent_approvals_pending_idx
    ON agent_approvals(user_id, status, created_at);
CREATE INDEX IF NOT EXISTS agent_approvals_thread_idx
    ON agent_approvals(user_id, thread_id, created_at);

CREATE TABLE IF NOT EXISTS agent_orders (
    user_id TEXT NOT NULL,
    order_id TEXT NOT NULL,
    status TEXT NOT NULL,
    amount TEXT NOT NULL,
    description TEXT NOT NULL,
    deleted_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, order_id)
);

CREATE INDEX IF NOT EXISTS agent_orders_active_idx
    ON agent_orders(user_id, order_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS agent_email_records (
    id BIGSERIAL PRIMARY KEY,
    idempotency_key TEXT UNIQUE NOT NULL,
    user_id TEXT NOT NULL,
    recipient TEXT NOT NULL,
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '',
    sent_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS agent_email_records_user_idx
    ON agent_email_records(user_id, sent_at DESC);

CREATE TABLE IF NOT EXISTS agent_tool_effects (
    idempotency_key TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
