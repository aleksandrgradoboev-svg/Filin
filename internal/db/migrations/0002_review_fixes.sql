-- Поправки по ревью ядра.

-- Место без номера тома («дополнительный том») обходило UNIQUE: SQLite считает NULL-ы
-- разными. Такие места различаются подписью. Без подписи — не ограничиваются: у
-- издательской серии члены без номера и подписи обычны.
CREATE UNIQUE INDEX collection_slot_unnumbered
    ON collection_slot (collection_id, label, book) WHERE volume IS NULL AND label <> '';

-- updated_at ставился только при вставке. Триггер обновляет его при любой правке издания,
-- кроме случая, когда правка сама выставила updated_at.
CREATE TRIGGER edition_touch AFTER UPDATE ON edition
    WHEN NEW.updated_at = OLD.updated_at
BEGIN
    UPDATE edition SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = NEW.id;
END;
