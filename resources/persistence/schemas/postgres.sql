--- event_journal represents the postgres event store schema
--- this schema need to be created in your database before you can use the postgres event store
CREATE TABLE IF NOT EXISTS event_journal
(
    persistence_id  VARCHAR(255)          NOT NULL,
    sequence_number BIGINT                NOT NULL,
    is_deleted      BOOLEAN DEFAULT FALSE NOT NULL,
    event_payload   BYTEA                 NOT NULL,
    event_manifest  VARCHAR(255)          NOT NULL,
    state_payload   BYTEA                 NOT NULL,
    state_manifest  VARCHAR(255)          NOT NULL,
    timestamp       TIMESTAMP             NOT NULL,

    PRIMARY KEY (persistence_id, sequence_number)
);

--- create an index on the is_deleted column
CREATE INDEX IF NOT EXISTS idx_event_journal_deleted ON event_journal (is_deleted);
