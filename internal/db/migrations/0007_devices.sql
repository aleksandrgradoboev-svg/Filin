-- Телефоны, которым разрешён доступ к Филину из домашней сети. Компьютер, на котором
-- стоит сервер, ходит без привязки; всё остальное — только с токеном привязанного устройства.
-- Хранится не сам токен, а его хэш: утёкшая база не даёт войти.

CREATE TABLE device (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,  -- sha256 токена, hex
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    last_seen  TEXT
);
