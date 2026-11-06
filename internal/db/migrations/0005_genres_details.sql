-- Жанры: у издания их бывает несколько («исторический роман», «приключения»).
-- Справочник свой, пополняется из формы; одинаковость названий сверяет код
-- (без учёта регистра и «ё») — индекс на fold() привязал бы базу к функции приложения.

CREATE TABLE genre (
    id      INTEGER PRIMARY KEY,
    name    TEXT NOT NULL UNIQUE CHECK (trim(name) <> ''),
    livelib INTEGER NOT NULL DEFAULT 0 CHECK (livelib IN (0, 1))  -- из справочника LiveLib: не убирается, даже если книг нет
);

-- Разделы справочника: «Классическая литература» → «Русская классика». Связь многие-ко-многим:
-- у LiveLib один жанр стоит в нескольких разделах («Зарубежная классика»).
CREATE TABLE genre_group (
    parent_id INTEGER NOT NULL REFERENCES genre (id) ON DELETE CASCADE,
    child_id  INTEGER NOT NULL REFERENCES genre (id) ON DELETE CASCADE,
    PRIMARY KEY (parent_id, child_id)
);

CREATE TABLE edition_genre (
    edition_id INTEGER NOT NULL REFERENCES edition (id) ON DELETE CASCADE,
    genre_id   INTEGER NOT NULL REFERENCES genre (id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,  -- порядок, в котором человек их перечислил
    PRIMARY KEY (edition_id, genre_id)
);
CREATE INDEX edition_genre_genre ON edition_genre (genre_id);

-- Выходные данные, как в карточках магазинов и LiveLib: «Тип обложки: твёрдая»,
-- «Формат: 84x108/32 (130x200 мм)», «Страниц: 543». Это свойства издания, а не
-- экземпляра: у экземпляра свой book_copy.binding — если переплели заново.
ALTER TABLE edition ADD COLUMN cover_type TEXT NOT NULL DEFAULT '';
ALTER TABLE edition ADD COLUMN format TEXT NOT NULL DEFAULT '';
ALTER TABLE edition ADD COLUMN pages INTEGER CHECK (pages > 0);

-- Остальное с титула и последней страницы, чтобы карточку не дозаполнять потом.
-- Тираж и цена на книге — ещё и признаки редкости для оценки (этап 8 плана).
ALTER TABLE edition ADD COLUMN orig_title TEXT NOT NULL DEFAULT '';    -- название оригинала
ALTER TABLE edition ADD COLUMN orig_language TEXT NOT NULL DEFAULT ''; -- «пер. с английского»
ALTER TABLE edition ADD COLUMN language TEXT NOT NULL DEFAULT '';      -- язык издания, если не русский
ALTER TABLE edition ADD COLUMN edition_note TEXT NOT NULL DEFAULT '';  -- «2-е изд., испр. и доп.»
ALTER TABLE edition ADD COLUMN circulation INTEGER CHECK (circulation > 0); -- тираж, экз.
ALTER TABLE edition ADD COLUMN print_price TEXT NOT NULL DEFAULT '';   -- цена на книге: «1 р. 20 к.»
ALTER TABLE edition ADD COLUMN printer TEXT NOT NULL DEFAULT '';       -- типография
ALTER TABLE edition ADD COLUMN age_rating TEXT NOT NULL DEFAULT '';    -- «16+»
ALTER TABLE edition ADD COLUMN annotation TEXT NOT NULL DEFAULT '';
ALTER TABLE edition ADD COLUMN contents TEXT NOT NULL DEFAULT '';      -- содержание сборника: по строке на произведение
-- Как в карточке Лабиринта: полиграфия и вид книги.
ALTER TABLE edition ADD COLUMN paper TEXT NOT NULL DEFAULT '';         -- «мелованная», «офсетная», «газетная»
ALTER TABLE edition ADD COLUMN design TEXT NOT NULL DEFAULT '';        -- оформление: «ляссе, тиснение объёмное»
ALTER TABLE edition ADD COLUMN illustrations TEXT NOT NULL DEFAULT ''; -- «цветные», «чёрно-белые», «без иллюстраций»
ALTER TABLE edition ADD COLUMN dimensions TEXT NOT NULL DEFAULT '';    -- размеры, мм: «267x211x17»
ALTER TABLE edition ADD COLUMN weight INTEGER CHECK (weight > 0);      -- вес, г
