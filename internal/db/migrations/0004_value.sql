-- Ценность — для себя (решено 22.10.2026): не страховая точность, а порядок —
-- что самое ценное, чем дорожить, сколько стоит библиотека.

-- Состояние по шкале: 5 — отличное, 4 — очень хорошее, 3 — хорошее,
-- 2 — удовлетворительное, 1 — плохое; NULL — не оценено. Свободный текст condition
-- остаётся для подробностей («корешок потёрт»).
ALTER TABLE book_copy ADD COLUMN grade INTEGER CHECK (grade BETWEEN 1 AND 5);

-- «Дорожу»: ценность не денежная — памятная или собирательская. Макулатурный том
-- за 100 ₽ с историей «добыли за 20 кг газет» дороже своей цены.
ALTER TABLE book_copy ADD COLUMN cherished INTEGER NOT NULL DEFAULT 0 CHECK (cherished IN (0, 1));

-- Оценка в рублях с датой и основанием. Цены меняются, поэтому оценки не
-- переписываются, а копятся: действующая — последняя по дате.
CREATE TABLE valuation (
    id         INTEGER PRIMARY KEY,
    copy_id    INTEGER NOT NULL REFERENCES book_copy (id) ON DELETE CASCADE,
    amount     INTEGER NOT NULL CHECK (amount >= 0),  -- рубли
    valued_on  TEXT NOT NULL,                         -- ГГГГ-ММ-ДД
    basis      TEXT NOT NULL DEFAULT '',              -- «по трём предложениям на alib.ru», «сам купил за»
    source_url TEXT NOT NULL DEFAULT ''
);
CREATE INDEX valuation_copy ON valuation (copy_id, valued_on);
