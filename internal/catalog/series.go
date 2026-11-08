package catalog

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

// Серия издания хранится как комплект вида series (схема 0001): место в комплекте —
// номер в серии, связь места с изданием — slot_edition. Отдельной таблицы не нужно,
// а у серии сразу есть страница «что из неё у меня есть».

// writeSeries ставит изданию серию e.Series с номером e.SeriesNo внутри транзакции;
// прежняя серия снимается. Пустое название — серии нет.
func writeSeries(tx *sql.Tx, id int64, e *Edition) error {
	// снять прежнюю привязку к серии; опустевшие места серии убрать
	if _, err := tx.Exec(`DELETE FROM slot_edition WHERE edition_id = ? AND slot_id IN (
		SELECT s.id FROM collection_slot s JOIN collection c ON c.id = s.collection_id WHERE c.kind = 'series')`,
		id); err != nil {
		return err
	}
	if err := dropEmptySeries(tx); err != nil {
		return err
	}
	title := strings.Join(strings.Fields(e.Series), " ")
	if title == "" {
		return nil
	}
	var cid int64
	err := tx.QueryRow(`SELECT id FROM collection WHERE kind = 'series' AND title = ?`, title).Scan(&cid)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`INSERT INTO collection (kind, title) VALUES ('series', ?) RETURNING id`, title).Scan(&cid)
	}
	if err != nil {
		return err
	}

	// Номер: число — номер места (у переизданий он общий, место одно); не число
	// («12а», «доп.») — подпись. Без номера — своё безымянное место.
	no := strings.TrimSpace(e.SeriesNo)
	var slot int64
	if n, err := strconv.Atoi(no); err == nil && n > 0 {
		err = tx.QueryRow(`INSERT INTO collection_slot (collection_id, volume, book) VALUES (?, ?, 0)
			ON CONFLICT DO UPDATE SET volume = excluded.volume RETURNING id`, cid, n).Scan(&slot)
		if err != nil {
			return err
		}
	} else if no != "" {
		err = tx.QueryRow(`INSERT INTO collection_slot (collection_id, volume, book, label) VALUES (?, NULL, 0, ?)
			ON CONFLICT DO UPDATE SET label = excluded.label RETURNING id`, cid, no).Scan(&slot)
		if err != nil {
			return err
		}
	} else if err := tx.QueryRow(`INSERT INTO collection_slot (collection_id, volume, book, label) VALUES (?, NULL, 0, '')
		RETURNING id`, cid).Scan(&slot); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO slot_edition (slot_id, edition_id) VALUES (?, ?)`, slot, id)
	return err
}

// dropEmptySeries убирает опустевшие места серий и серии без единой книги — после того,
// как книгу сняли с серии или удалили.
func dropEmptySeries(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM collection_slot WHERE id NOT IN (SELECT slot_id FROM slot_edition)
		AND collection_id IN (SELECT id FROM collection WHERE kind = 'series')`); err != nil {
		return err
	}
	// серия без единой книги — опечатка, оставшаяся в подсказках; убрать
	_, err := tx.Exec(`DELETE FROM collection WHERE kind = 'series'
		AND id NOT IN (SELECT collection_id FROM collection_slot)`)
	return err
}

// readSeries дочитывает в e серию и номер.
func (s *Store) readSeries(e *Edition) error {
	var vol sql.NullInt64
	var label string
	err := s.db.QueryRow(`SELECT c.id, c.title, s.volume, s.label
		FROM slot_edition se JOIN collection_slot s ON s.id = se.slot_id JOIN collection c ON c.id = s.collection_id
		WHERE se.edition_id = ? AND c.kind = 'series' ORDER BY c.id LIMIT 1`, e.ID).
		Scan(&e.SeriesID, &e.Series, &vol, &label)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	e.SeriesNo = label
	if vol.Valid {
		e.SeriesNo = strconv.FormatInt(vol.Int64, 10)
	}
	return nil
}

// SeriesNames — названия серий для подсказок в форме.
func (s *Store) SeriesNames() ([]string, error) {
	rows, err := s.db.Query(`SELECT title FROM collection WHERE kind = 'series' ORDER BY fold(title)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SeriesItem — издание серии с номером.
type SeriesItem struct {
	No string // номер или подпись; пусто — без номера
	Row
}

// Series — серия и что из неё есть, по номерам.
func (s *Store) Series(id int64) (title string, items []SeriesItem, err error) {
	err = s.db.QueryRow(`SELECT title FROM collection WHERE id = ? AND kind = 'series'`, id).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, err
	}
	rows, err := s.db.Query(`SELECT COALESCE(CAST(s.volume AS TEXT), s.label), e.id, e.title, e.publisher,
			COALESCE(e.year, 0), e.volume, e.cover_path,
			COALESCE((SELECT group_concat(p.name, ', ' ORDER BY p.position) FROM edition_person p
			          WHERE p.edition_id = e.id AND p.role = 'author'), ''),
			(SELECT COUNT(*) FROM book_copy c WHERE c.edition_id = e.id)
		FROM collection_slot s JOIN slot_edition se ON se.slot_id = s.id JOIN edition e ON e.id = se.edition_id
		WHERE s.collection_id = ?
		ORDER BY s.volume IS NULL, s.volume, s.label, fold(e.title)`, id)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it SeriesItem
		if err := rows.Scan(&it.No, &it.ID, &it.Title, &it.Publisher, &it.Year, &it.Volume, &it.Cover,
			&it.Authors, &it.Copies); err != nil {
			return "", nil, err
		}
		items = append(items, it)
	}
	return title, items, rows.Err()
}
