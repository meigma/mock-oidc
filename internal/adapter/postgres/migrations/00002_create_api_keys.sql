-- +goose Up
-- key_hash holds a lowercase-hex SHA-256 digest of the API key, never the key
-- itself, so a table or backup disclosure exposes no replayable credentials. The
-- apikey store hashes the presented key the same way and looks it up by digest.
CREATE TABLE api_keys (
    key_hash   text   PRIMARY KEY,
    subject    text   NOT NULL,
    roles      text[] NOT NULL DEFAULT '{}'
);

-- +goose Down
DROP TABLE api_keys;
