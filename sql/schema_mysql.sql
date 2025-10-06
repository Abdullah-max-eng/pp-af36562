CREATE TABLE IF NOT EXISTS
    nodes (
        id BIGINT PRIMARY KEY AUTO_INCREMENT,
        label VARCHAR(64) NOT NULL,
        name VARCHAR(255),
        props JSON NULL
    );

CREATE TABLE IF NOT EXISTS
    edges (
        id BIGINT PRIMARY KEY AUTO_INCREMENT,
        src_id BIGINT NOT NULL,
        dst_id BIGINT NOT NULL,
        typ VARCHAR(64) NOT NULL,
        props JSON NULL,
        FOREIGN KEY (src_id) REFERENCES nodes (id),
        FOREIGN KEY (dst_id) REFERENCES nodes (id),
        INDEX (src_id),
        INDEX (dst_id),
        INDEX (typ)
    );