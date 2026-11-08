-- Ревью ядра 29.10, проверено 08.11.2026: триггер edition_touch сам делает UPDATE той же строки,
-- и при включённом PRAGMA recursive_triggers каждая правка издания падала «too many levels of
-- trigger recursion». Сейчас режим выключен (умолчание), но держится это на случайности.
-- Теперь триггер не трогает строку, если updated_at уже «сейчас»: вложенный UPDATE ставит то же
-- значение — и условие ложно, рекурсии нет ни при каком режиме.
DROP TRIGGER edition_touch;
CREATE TRIGGER edition_touch AFTER UPDATE ON edition
    WHEN NEW.updated_at = OLD.updated_at AND NEW.updated_at <> strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
BEGIN
    UPDATE edition SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = NEW.id;
END;
