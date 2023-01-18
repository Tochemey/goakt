--- offsets relation holds the offsets of events consumed by a projection
CREATE TABLE IF NOT EXISTS offsets
(
    projection_name  VARCHAR(255) NOT NULL,
    persistence_id   VARCHAR(255) NOT NULL,
    current_offset   BIGINT       NOT NULL,
    last_updated     BIGINT       NOT NULL,
    PRIMARY KEY (projection_name, persistence_id)
);

CREATE INDEX IF NOT EXISTS idx_offsets_projection_name ON offsets (projection_name);
