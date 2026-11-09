-- Browser-confirmed versions are independent from the mutable MCP workspace.
CREATE TABLE product_confirmations (
 product_id TEXT PRIMARY KEY REFERENCES products(id),
 version INTEGER NOT NULL,
 document_json TEXT NOT NULL CHECK(json_valid(document_json)),
 deleted INTEGER NOT NULL DEFAULT 0 CHECK(deleted IN (0,1)),
 state TEXT NOT NULL CHECK(state IN ('pending','updating','succeeded','failed')),
 confirmed_at TEXT NOT NULL
) STRICT;
CREATE TABLE site_update_batches (
 candidate_id TEXT PRIMARY KEY REFERENCES candidates(id),
 versions_json TEXT NOT NULL CHECK(json_valid(versions_json)),
 operation_id TEXT UNIQUE,
 state TEXT NOT NULL CHECK(state IN ('building','activating','succeeded','failed','superseded'))
) STRICT;
CREATE TABLE product_write_requests (
 actor_id TEXT NOT NULL,
 operation TEXT NOT NULL,
 request_key TEXT NOT NULL,
 request_sha256 TEXT NOT NULL,
 result_json TEXT NOT NULL CHECK(json_valid(result_json)),
 created_at TEXT NOT NULL,
 PRIMARY KEY(actor_id,operation,request_key)
) STRICT;
