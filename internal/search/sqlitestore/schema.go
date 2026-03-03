// Package sqlitestore implements the search.Store interface using SQLite with FTS5.
package sqlitestore

// DDL contains all CREATE TABLE, CREATE INDEX, CREATE VIRTUAL TABLE, and trigger
// statements needed to initialise or migrate the search database.
//
// All statements are idempotent (IF NOT EXISTS / CREATE OR REPLACE TRIGGER).
const DDL = `
CREATE TABLE IF NOT EXISTS documents (
    id            TEXT PRIMARY KEY,
    type          TEXT NOT NULL,
    path          TEXT NOT NULL,
    page_id       TEXT NOT NULL DEFAULT '',
    title         TEXT NOT NULL DEFAULT '',
    space_key     TEXT NOT NULL DEFAULT '',
    labels        TEXT NOT NULL DEFAULT '[]',
    content       TEXT NOT NULL DEFAULT '',
    heading_path  TEXT NOT NULL DEFAULT '[]',
    heading_text  TEXT NOT NULL DEFAULT '',
    heading_level INTEGER NOT NULL DEFAULT 0,
    language      TEXT NOT NULL DEFAULT '',
    line          INTEGER NOT NULL DEFAULT 0,
    mod_time      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_documents_path      ON documents(path);
CREATE INDEX IF NOT EXISTS idx_documents_type      ON documents(type);
CREATE INDEX IF NOT EXISTS idx_documents_space_key ON documents(space_key);

CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
    title,
    content,
    heading_text,
    content=documents,
    content_rowid=rowid,
    tokenize='porter unicode61'
);

CREATE TRIGGER IF NOT EXISTS documents_ai
AFTER INSERT ON documents BEGIN
    INSERT INTO documents_fts(rowid, title, content, heading_text)
    VALUES (new.rowid, new.title, new.content, new.heading_text);
END;

CREATE TRIGGER IF NOT EXISTS documents_ad
AFTER DELETE ON documents BEGIN
    INSERT INTO documents_fts(documents_fts, rowid, title, content, heading_text)
    VALUES ('delete', old.rowid, old.title, old.content, old.heading_text);
END;

CREATE TRIGGER IF NOT EXISTS documents_au
AFTER UPDATE ON documents BEGIN
    INSERT INTO documents_fts(documents_fts, rowid, title, content, heading_text)
    VALUES ('delete', old.rowid, old.title, old.content, old.heading_text);
    INSERT INTO documents_fts(rowid, title, content, heading_text)
    VALUES (new.rowid, new.title, new.content, new.heading_text);
END;

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
`

const metaKeyLastIndexedAt = "last_indexed_at"
