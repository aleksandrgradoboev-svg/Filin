-- Выдача книг почитать. Выдаётся экземпляр, а не издание: из двух одинаковых книг
-- одна может быть на руках, другая — на полке.
-- Человек — просто строка (решено 30.10.2026): «Петя» и «Пётр Иванович» — разные люди,
-- зато без справочника. Справочник можно добавить позже, не ломая выдачу.

CREATE TABLE loan (
    id          INTEGER PRIMARY KEY,
    copy_id     INTEGER NOT NULL REFERENCES book_copy (id) ON DELETE CASCADE,
    borrower    TEXT NOT NULL CHECK (trim(borrower) <> ''),
    lent_on     TEXT NOT NULL,  -- ГГГГ-ММ-ДД
    due_on      TEXT,           -- срок; NULL — без срока
    returned_on TEXT,           -- NULL — книга на руках
    note        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX loan_copy ON loan (copy_id);
CREATE INDEX loan_borrower ON loan (borrower);

-- Один экземпляр на руках только у одного человека.
CREATE UNIQUE INDEX loan_active ON loan (copy_id) WHERE returned_on IS NULL;
