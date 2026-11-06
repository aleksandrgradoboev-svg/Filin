-- Ядро каталога: издания, экземпляры, полки, комплекты.
-- Издание — что напечатано; экземпляр — что стоит на полке. Две «Мастера и Маргариты»
-- одного тиража — одно издание и два экземпляра.

-- Полка: комната → шкаф → полка.
CREATE TABLE shelf (
    id       INTEGER PRIMARY KEY,
    room     TEXT NOT NULL,
    bookcase TEXT NOT NULL,
    shelf    TEXT NOT NULL,
    note     TEXT NOT NULL DEFAULT '',
    UNIQUE (room, bookcase, shelf)
);

CREATE TABLE edition (
    id          INTEGER PRIMARY KEY,
    title       TEXT NOT NULL,
    publisher   TEXT NOT NULL DEFAULT '',
    city        TEXT NOT NULL DEFAULT '',
    year        INTEGER,
    volume      TEXT NOT NULL DEFAULT '',  -- как напечатано: «т. 3», «Том 6-7»; счёт томов — через collection_slot
    bbk         TEXT NOT NULL DEFAULT '',  -- ББК, УДК, авторский знак — для тематики и полок, не для опознания
    udk         TEXT NOT NULL DEFAULT '',
    author_sign TEXT NOT NULL DEFAULT '',
    cover_path  TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'manual',  -- откуда данные: manual, livelib, fantlab, alib
    source_url  TEXT NOT NULL DEFAULT '',
    livelib_id  TEXT UNIQUE,
    notes       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Авторы и прочие люди издания: «под ред. Рыбакова», «пер. Любимова», «ил. Доре».
CREATE TABLE edition_person (
    edition_id INTEGER NOT NULL REFERENCES edition (id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    name       TEXT NOT NULL,
    role       TEXT NOT NULL DEFAULT 'author'
               CHECK (role IN ('author', 'editor', 'translator', 'illustrator', 'compiler')),
    PRIMARY KEY (edition_id, position)
);

-- ISBN — поле, а не ключ: у издания их бывает несколько (соиздатели), а российские
-- издательства один ISBN используют повторно. Поэтому индекс не уникальный.
CREATE TABLE edition_isbn (
    edition_id INTEGER NOT NULL REFERENCES edition (id) ON DELETE CASCADE,
    isbn       TEXT NOT NULL,
    valid      INTEGER NOT NULL CHECK (valid IN (0, 1)),  -- 0: контрольная сумма не сходится
    PRIMARY KEY (edition_id, isbn)
);
CREATE INDEX edition_isbn_isbn ON edition_isbn (isbn);

-- Комплект: собрание сочинений / многотомник (set) или издательская серия (series).
-- У set считается комплектность, у series — только состав.
CREATE TABLE collection (
    id                INTEGER PRIMARY KEY,
    kind              TEXT NOT NULL CHECK (kind IN ('set', 'series')),
    title             TEXT NOT NULL,
    publisher         TEXT NOT NULL DEFAULT '',
    year_from         INTEGER,
    year_to           INTEGER,
    volumes_announced INTEGER,  -- объявлено; вышедшее — это заведённые места collection_slot
    notes             TEXT NOT NULL DEFAULT ''
);

-- Место в комплекте: том и книга внутри тома. «Кембриджская история древнего мира» —
-- 14 томов в 19 книгах, то есть 19 мест: считать надо книги, а не тома.
CREATE TABLE collection_slot (
    id            INTEGER PRIMARY KEY,
    collection_id INTEGER NOT NULL REFERENCES collection (id) ON DELETE CASCADE,
    volume        INTEGER,
    book          INTEGER NOT NULL DEFAULT 0,  -- 0: том не делится на книги
    label         TEXT NOT NULL DEFAULT '',
    UNIQUE (collection_id, volume, book)
);

-- Какое издание закрывает место. Сдвоенный «Том 6-7» закрывает два места.
CREATE TABLE slot_edition (
    slot_id    INTEGER NOT NULL REFERENCES collection_slot (id) ON DELETE CASCADE,
    edition_id INTEGER NOT NULL REFERENCES edition (id) ON DELETE CASCADE,
    PRIMARY KEY (slot_id, edition_id)
);
CREATE INDEX slot_edition_edition ON slot_edition (edition_id);

-- Экземпляр — конкретная книга на полке.
CREATE TABLE book_copy (
    id           INTEGER PRIMARY KEY,
    edition_id   INTEGER NOT NULL REFERENCES edition (id) ON DELETE RESTRICT,
    shelf_id     INTEGER REFERENCES shelf (id) ON DELETE SET NULL,
    condition    TEXT NOT NULL DEFAULT '',  -- по шкале букинистов; шкала ещё не выбрана — свободный текст
    binding      TEXT NOT NULL DEFAULT '',
    bookplate    TEXT NOT NULL DEFAULT '',  -- экслибрис
    inscriptions TEXT NOT NULL DEFAULT '',  -- надписи, автографы
    history      TEXT NOT NULL DEFAULT '',  -- история экземпляра: «добыли за 20 кг газет»
    title_photo  TEXT NOT NULL DEFAULT '',
    no_lend      INTEGER NOT NULL DEFAULT 0 CHECK (no_lend IN (0, 1)),
    notes        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE INDEX book_copy_edition ON book_copy (edition_id);
CREATE INDEX book_copy_shelf ON book_copy (shelf_id);
