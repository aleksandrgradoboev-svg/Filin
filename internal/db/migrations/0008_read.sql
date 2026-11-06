-- «Прочитана» — у экземпляра, просто отметка (решено 06.11.2026): без даты и без «кто».
ALTER TABLE book_copy ADD COLUMN is_read INTEGER NOT NULL DEFAULT 0 CHECK (is_read IN (0, 1));
