-- Страницы людей: автор, переводчик, художник — все его книги и небольшая справка.
-- Человек узнаётся по имени, как оно записано в карточках (edition_person.name):
-- «Лондон Джек» и «Джек Лондон» — разные записи, пока имя не поправлено в карточках.

CREATE TABLE person (
    name  TEXT PRIMARY KEY CHECK (trim(name) <> ''),
    life  TEXT NOT NULL DEFAULT '',  -- «1876–1916»
    about TEXT NOT NULL DEFAULT '',  -- пара строк о человеке
    link  TEXT NOT NULL DEFAULT ''   -- ссылка: Википедия, FantLab
);
