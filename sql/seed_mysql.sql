INSERT INTO
    nodes (label, name)
VALUES
    ('Person', 'Alice'),
    ('Person', 'Bob'),
    ('Movie', 'The Matrix');

INSERT INTO
    edges (src_id, dst_id, typ)
VALUES
    (1, 3, 'ACTED_IN'),
    (2, 3, 'ACTED_IN');