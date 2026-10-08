CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE IF NOT EXISTS actors (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
 role text NOT NULL CHECK(role IN ('visitor','agent','admin')),
 name text NOT NULL, email text UNIQUE, password_hash text NOT NULL DEFAULT '',
 enabled boolean NOT NULL DEFAULT true, available boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
 token_hash text PRIMARY KEY, actor_id text NOT NULL REFERENCES actors(id),
 expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions(expires_at);
CREATE TABLE IF NOT EXISTS conversations (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text, visitor_id text NOT NULL REFERENCES actors(id),
 agent_id text REFERENCES actors(id), status text NOT NULL DEFAULT 'ai' CHECK(status IN ('ai','waiting','human','closed')),
 title text NOT NULL DEFAULT '新的咨询', version bigint NOT NULL DEFAULT 1, last_seq bigint NOT NULL DEFAULT 0,
 last_message text NOT NULL DEFAULT '', ai_phase text NOT NULL DEFAULT 'idle', ai_handled_seq bigint NOT NULL DEFAULT 0,
 clarification_count int NOT NULL DEFAULT 0, summary text NOT NULL DEFAULT '', handoff_reason text NOT NULL DEFAULT '',
 feedback text NOT NULL DEFAULT '' CHECK(feedback IN ('','solved','unsolved')), handed_off boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 waiting_at timestamptz, claimed_at timestamptz, first_response_at timestamptz, closed_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_visitor ON conversations(visitor_id) WHERE status <> 'closed';
CREATE INDEX IF NOT EXISTS conversations_queue ON conversations(status,updated_at DESC);
CREATE INDEX IF NOT EXISTS conversations_agent ON conversations(agent_id,updated_at DESC);
CREATE TABLE IF NOT EXISTS attachments (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text, conversation_id text NOT NULL REFERENCES conversations(id),
 uploader_id text NOT NULL REFERENCES actors(id), path text NOT NULL, mime text NOT NULL, name text NOT NULL,
 size bigint NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS messages (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text, conversation_id text NOT NULL REFERENCES conversations(id), seq bigint NOT NULL,
 sender_id text NOT NULL, sender_role text NOT NULL CHECK(sender_role IN ('visitor','agent','admin','ai','system')),
 sender_name text NOT NULL DEFAULT '', client_id text NOT NULL, body text NOT NULL DEFAULT '', image_id text REFERENCES attachments(id),
 citations jsonb NOT NULL DEFAULT '[]', created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(conversation_id,seq), UNIQUE(conversation_id,sender_id,client_id)
);
CREATE TABLE IF NOT EXISTS read_cursors (
 conversation_id text NOT NULL REFERENCES conversations(id), actor_id text NOT NULL REFERENCES actors(id),
 seq bigint NOT NULL DEFAULT 0, PRIMARY KEY(conversation_id,actor_id)
);
CREATE TABLE IF NOT EXISTS handoffs (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text, conversation_id text NOT NULL REFERENCES conversations(id),
 reason text NOT NULL DEFAULT '', summary text NOT NULL DEFAULT '', initiated_by text NOT NULL DEFAULT '',
 agent_id text REFERENCES actors(id), created_at timestamptz NOT NULL DEFAULT now(), claimed_at timestamptz, first_response_at timestamptz
);
CREATE TABLE IF NOT EXISTS ai_jobs (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text, conversation_id text NOT NULL REFERENCES conversations(id),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','running','completed','cancelled','failed')),
 conversation_version bigint NOT NULL DEFAULT 1, input_seq bigint NOT NULL DEFAULT 0,
 lease_owner text NOT NULL DEFAULT '', lease_generation bigint NOT NULL DEFAULT 0, lease_until timestamptz,
 attempts int NOT NULL DEFAULT 0, error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_ai_job ON ai_jobs(conversation_id) WHERE status IN ('pending','running');
CREATE INDEX IF NOT EXISTS ai_job_poll ON ai_jobs(status,lease_until,created_at);
CREATE TABLE IF NOT EXISTS documents (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text, name text NOT NULL, enabled boolean NOT NULL DEFAULT true,
 deleted boolean NOT NULL DEFAULT false, active_version_id text,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS document_versions (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text, document_id text NOT NULL REFERENCES documents(id),
 filename text NOT NULL, path text NOT NULL, mime text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'processing' CHECK(status IN ('processing','ready','failed')),
 error text NOT NULL DEFAULT '', chunk_count int NOT NULL DEFAULT 0,
 embedding_fingerprint text NOT NULL DEFAULT '', content_hash text NOT NULL DEFAULT '',
 lease_owner text NOT NULL DEFAULT '', lease_generation bigint NOT NULL DEFAULT 0, lease_until timestamptz,
 attempts int NOT NULL DEFAULT 0, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS document_version_poll ON document_versions(status,lease_until);
CREATE TABLE IF NOT EXISTS chunks (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text, document_id text NOT NULL REFERENCES documents(id),
 version_id text NOT NULL REFERENCES document_versions(id), page int NOT NULL DEFAULT 0, section text NOT NULL DEFAULT '',
 ordinal int NOT NULL, content text NOT NULL, embedding vector(1024) NOT NULL,
 UNIQUE(version_id,ordinal)
);
CREATE INDEX IF NOT EXISTS chunks_document ON chunks(document_id,version_id);
CREATE TABLE IF NOT EXISTS ai_calls (
 id text PRIMARY KEY DEFAULT gen_random_uuid()::text, conversation_id text REFERENCES conversations(id),
 kind text NOT NULL, model text NOT NULL, status text NOT NULL, latency_ms bigint NOT NULL DEFAULT 0,
 input_tokens bigint NOT NULL DEFAULT 0, output_tokens bigint NOT NULL DEFAULT 0,
 error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ai_calls_recent ON ai_calls(created_at DESC);
