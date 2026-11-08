-- Темы издания — о чём книга, а не что это за книга (жанр). Из РГБ — тематические рубрики
-- (поле 650): цепочка от общего к частному, уровни через « -- », как в записи РГБ:
-- «История -- СССР -- Период гражданской войны (1918-1920) -- Воспоминания».
-- Цепочка хранится целиком: по любому её уровню видны все книги на эту тему.
CREATE TABLE edition_subject (
    edition_id INTEGER NOT NULL REFERENCES edition (id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    path       TEXT NOT NULL CHECK (path <> ''),
    PRIMARY KEY (edition_id, position)
);
