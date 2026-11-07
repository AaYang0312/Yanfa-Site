-- UTC timestamps are RFC3339Nano strings. Auth expiry comparisons are performed
-- by Go time.Time, never lexically on fractional timestamp strings.
CREATE TABLE admins (
 id TEXT PRIMARY KEY CHECK(id = 'admin'),
 username TEXT NOT NULL UNIQUE,
 password_hash TEXT NOT NULL,
 created_at TEXT NOT NULL
) STRICT;
CREATE TABLE sessions (
 secret_hash TEXT PRIMARY KEY CHECK(length(secret_hash) = 64),
 admin_id TEXT NOT NULL REFERENCES admins(id),
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 revoked_at TEXT,
 preview_candidate_id TEXT REFERENCES candidates(id),
 preview_expires_at TEXT
) STRICT;
CREATE INDEX sessions_admin ON sessions(admin_id);
CREATE TABLE mcp_tokens (
 id TEXT PRIMARY KEY,
 admin_id TEXT NOT NULL REFERENCES admins(id),
 name TEXT NOT NULL,
 secret_hash TEXT NOT NULL UNIQUE CHECK(length(secret_hash) = 64),
 scopes_json TEXT NOT NULL CHECK(json_valid(scopes_json) AND json_type(scopes_json) = 'array'),
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 revoked_at TEXT
) STRICT;
CREATE INDEX mcp_tokens_admin ON mcp_tokens(admin_id);
CREATE TABLE workspace (
 id INTEGER PRIMARY KEY CHECK(id = 1),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 0 AND 9007199254740991),
 dictionaries_json TEXT NOT NULL CHECK(json_valid(dictionaries_json) AND json_type(dictionaries_json) = 'object'),
 seed_sha256 TEXT CHECK(seed_sha256 IS NULL OR length(seed_sha256) = 64)
) STRICT;
INSERT INTO workspace(id, revision, dictionaries_json) VALUES (1, 0, '{}');
CREATE TABLE products (
 id TEXT PRIMARY KEY,
 slug TEXT NOT NULL UNIQUE,
 model TEXT NOT NULL COLLATE NOCASE,
 version INTEGER NOT NULL CHECK(version BETWEEN 1 AND 9007199254740991),
 document_json TEXT NOT NULL CHECK(json_valid(document_json) AND json_type(document_json) = 'object'),
 deleted_at TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX products_model_unique ON products(model COLLATE NOCASE) WHERE model <> '' AND deleted_at IS NULL;
CREATE TABLE reserved_routes (
 slug TEXT PRIMARY KEY,
 owner_kind TEXT NOT NULL CHECK(owner_kind IN ('product','series')),
 owner_id TEXT NOT NULL,
 ever_published INTEGER NOT NULL DEFAULT 0 CHECK(ever_published IN (0,1)),
 created_at TEXT NOT NULL
) STRICT;
CREATE TRIGGER reserved_routes_no_delete BEFORE DELETE ON reserved_routes BEGIN
 SELECT RAISE(ABORT, 'route ownership is permanent');
END;
CREATE TRIGGER reserved_routes_no_transfer BEFORE UPDATE OF slug, owner_kind, owner_id ON reserved_routes BEGIN
 SELECT RAISE(ABORT, 'route ownership is permanent');
END;
CREATE TRIGGER reserved_routes_no_unpublish BEFORE UPDATE OF ever_published ON reserved_routes
WHEN OLD.ever_published = 1 AND NEW.ever_published <> 1 BEGIN
 SELECT RAISE(ABORT, 'published route history is permanent');
END;
CREATE TABLE media (
 id TEXT PRIMARY KEY,
 sha256 TEXT NOT NULL UNIQUE CHECK(length(sha256) = 64),
 format TEXT NOT NULL CHECK(format IN ('jpeg','png','webp')),
 bytes INTEGER NOT NULL CHECK(bytes BETWEEN 1 AND 10485760),
 width INTEGER NOT NULL CHECK(width BETWEEN 1 AND 12000),
 height INTEGER NOT NULL CHECK(height BETWEEN 1 AND 12000 AND width * height <= 40000000),
 path TEXT NOT NULL UNIQUE,
 uploaded_by TEXT NOT NULL REFERENCES admins(id),
 created_at TEXT NOT NULL
) STRICT;
CREATE INDEX media_created ON media(created_at, id);
CREATE TABLE guide_reviews (
 product_id TEXT NOT NULL REFERENCES products(id),
 field TEXT NOT NULL,
 fact_sha256 TEXT NOT NULL CHECK(length(fact_sha256) = 64),
 source_sha256s_json TEXT NOT NULL CHECK(json_valid(source_sha256s_json) AND json_type(source_sha256s_json) = 'array'),
 reviewed_by TEXT NOT NULL REFERENCES admins(id),
 reviewed_at TEXT NOT NULL,
 PRIMARY KEY(product_id, field)
) STRICT;
CREATE TABLE candidates (
 id TEXT PRIMARY KEY,
 state TEXT NOT NULL CHECK(state IN ('queued','building','ready','failed')),
 schema_version INTEGER NOT NULL CHECK(schema_version > 0),
 workspace_revision INTEGER NOT NULL CHECK(workspace_revision BETWEEN 0 AND 9007199254740991),
 code_version TEXT NOT NULL,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json) AND json_type(snapshot_json) = 'object'),
 snapshot_sha256 TEXT NOT NULL CHECK(length(snapshot_sha256) = 64),
 expected_active_release_id TEXT,
 artifact_path TEXT,
 manifest_sha256 TEXT,
 validation_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(validation_json)),
 diff_json TEXT NOT NULL CHECK(json_valid(diff_json) AND json_type(diff_json) = 'object'),
 created_by TEXT NOT NULL REFERENCES admins(id),
 created_at TEXT NOT NULL,
 started_at TEXT,
 completed_at TEXT,
 error_json TEXT CHECK(error_json IS NULL OR json_valid(error_json))
) STRICT;
CREATE INDEX candidates_queue ON candidates(state, created_at, id);
CREATE TABLE publication_requests (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('publish','rollback')),
 candidate_id TEXT REFERENCES candidates(id),
 release_id TEXT REFERENCES candidates(id),
 snapshot_sha256 TEXT NOT NULL CHECK(length(snapshot_sha256) = 64),
 expected_active_release_id TEXT,
 expected_workspace_revision INTEGER NOT NULL CHECK(expected_workspace_revision BETWEEN 0 AND 9007199254740991),
 state TEXT NOT NULL CHECK(state IN ('awaiting_confirmation','queued','activating','succeeded','stale','expired','cancelled','failed')),
 created_by TEXT NOT NULL REFERENCES admins(id),
 confirmed_by TEXT REFERENCES admins(id),
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 confirmed_at TEXT,
 completed_at TEXT,
 result_release_id TEXT REFERENCES candidates(id),
 error_json TEXT CHECK(error_json IS NULL OR json_valid(error_json)),
 CHECK((kind = 'publish' AND candidate_id IS NOT NULL AND release_id IS NULL) OR
       (kind = 'rollback' AND candidate_id IS NULL AND release_id IS NOT NULL))
) STRICT;
CREATE INDEX publication_queue ON publication_requests(state, created_at, id);
CREATE TABLE idempotency_keys (
 actor_id TEXT NOT NULL,
 operation TEXT NOT NULL,
 key TEXT NOT NULL,
 request_sha256 TEXT NOT NULL CHECK(length(request_sha256) = 64),
 resource_id TEXT NOT NULL,
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 PRIMARY KEY(actor_id, operation, key)
) STRICT;
