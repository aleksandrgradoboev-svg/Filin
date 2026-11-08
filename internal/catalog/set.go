package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Собрание сочинений / многотомник — комплект вида set: места «том / книга в томе» и
// издания, которые их закрывают. Считаются книги, а не тома (см. Completeness).
// Издательская серия (series) — другой вид: у неё состав, а не полнота, здесь её нет.

// MaxVolumes — больше томов собрание не заводит: защита от опечатки «100» вместо «10».
const MaxVolumes = 200

// SetRow — собрание в списке: сколько мест и сколько закрыто.
type SetRow struct {
	Collection
	Places, Have int
}

// Sets — все собрания по названию.
func (s *Store) Sets() ([]SetRow, error) {
	rows, err := s.db.Query(`SELECT c.id, c.title, c.publisher, COALESCE(c.year_from, 0), COALESCE(c.year_to, 0),
			COALESCE(c.volumes_announced, 0), c.notes,
			(SELECT COUNT(*) FROM collection_slot s WHERE s.collection_id = c.id),
			(SELECT COUNT(*) FROM collection_slot s WHERE s.collection_id = c.id AND ` + placeHave + `)
		FROM collection c WHERE c.kind = 'set' ORDER BY fold(c.title)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SetRow
	for rows.Next() {
		r := SetRow{Collection: Collection{Kind: "set"}}
		if err := rows.Scan(&r.ID, &r.Title, &r.Publisher, &r.YearFrom, &r.YearTo, &r.VolumesAnnounced, &r.Notes,
			&r.Places, &r.Have); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// placeHave — место закрыто: есть экземпляр хотя бы одного издания, связанного с ним.
// Условие на строку s таблицы collection_slot — то же, что в Completeness.
const placeHave = `EXISTS (SELECT 1 FROM slot_edition se JOIN book_copy bc ON bc.edition_id = se.edition_id
	WHERE se.slot_id = s.id)`

// CreateSet заводит собрание и сразу места т. 1 … т. N, где N — объявлено томов.
func (s *Store) CreateSet(c *Collection) (int64, error) {
	c.Kind = "set"
	c.Title, c.Publisher, c.Notes = strings.TrimSpace(c.Title), strings.TrimSpace(c.Publisher), strings.TrimSpace(c.Notes)
	switch {
	case c.Title == "":
		return 0, InputError("нужно название собрания")
	case c.VolumesAnnounced < 1 || c.VolumesAnnounced > MaxVolumes:
		return 0, InputError(fmt.Sprintf("томов — от 1 до %d", MaxVolumes))
	case c.YearTo != 0 && c.YearFrom != 0 && c.YearTo < c.YearFrom:
		return 0, InputError("год окончания раньше года начала")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO collection (kind, title, publisher, year_from, year_to, volumes_announced, notes)
		VALUES ('set', ?, ?, ?, ?, ?, ?)`,
		c.Title, c.Publisher, nullInt(c.YearFrom), nullInt(c.YearTo), c.VolumesAnnounced, c.Notes)
	if err != nil {
		return 0, err
	}
	if c.ID, err = res.LastInsertId(); err != nil {
		return 0, err
	}
	for v := 1; v <= c.VolumesAnnounced; v++ {
		if _, err := tx.Exec(`INSERT INTO collection_slot (collection_id, volume, book) VALUES (?, ?, 0)`, c.ID, v); err != nil {
			return 0, err
		}
	}
	return c.ID, tx.Commit()
}

// DeleteSet убирает собрание вместе с местами; книги и экземпляры остаются.
func (s *Store) DeleteSet(id int64) error {
	res, err := s.db.Exec(`DELETE FROM collection WHERE id = ? AND kind = 'set'`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Place — место в собрании и издания, которые его закрывают.
type Place struct {
	ID       int64
	Volume   int // 0 — без номера (дополнительный том с подписью)
	Book     int // 0 — том не делится на книги
	Label    string
	Editions []PlaceEdition
	Have     bool // есть экземпляр хотя бы одного издания
}

// PlaceEdition — издание на месте собрания.
type PlaceEdition struct {
	ID     int64
	Title  string
	Volume string
	Cover  string
	Copies int
}

// Name — как место пишется: «т. 3», «т. 3, кн. 2», подпись для тома без номера.
func (p Place) Name() string { return placeName(p.Volume, p.Book, p.Label) }

func placeName(volume, book int, label string) string {
	var s string
	switch {
	case volume != 0:
		s = fmt.Sprintf("т. %d", volume)
	case label != "":
		s = label
	default:
		s = "без номера"
	}
	if book != 0 {
		s += fmt.Sprintf(", кн. %d", book)
	}
	return s
}

// Set — собрание с местами по порядку томов.
func (s *Store) Set(id int64) (Collection, []Place, error) {
	c := Collection{ID: id, Kind: "set"}
	err := s.db.QueryRow(`SELECT title, publisher, COALESCE(year_from, 0), COALESCE(year_to, 0),
			COALESCE(volumes_announced, 0), notes FROM collection WHERE id = ? AND kind = 'set'`, id).
		Scan(&c.Title, &c.Publisher, &c.YearFrom, &c.YearTo, &c.VolumesAnnounced, &c.Notes)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil, ErrNotFound
	}
	if err != nil {
		return c, nil, err
	}
	rows, err := s.db.Query(`SELECT s.id, COALESCE(s.volume, 0), s.book, s.label,
			COALESCE(e.id, 0), COALESCE(e.title, ''), COALESCE(e.volume, ''), COALESCE(e.cover_path, ''),
			(SELECT COUNT(*) FROM book_copy bc WHERE bc.edition_id = e.id)
		FROM collection_slot s
		LEFT JOIN slot_edition se ON se.slot_id = s.id
		LEFT JOIN edition e ON e.id = se.edition_id
		WHERE s.collection_id = ?
		ORDER BY s.volume IS NULL, s.volume, s.label, s.book, s.id, fold(e.title)`, id) // s.id: строки одного места — подряд
	if err != nil {
		return c, nil, err
	}
	defer rows.Close()
	var places []Place
	for rows.Next() {
		var p Place
		var e PlaceEdition
		if err := rows.Scan(&p.ID, &p.Volume, &p.Book, &p.Label, &e.ID, &e.Title, &e.Volume, &e.Cover, &e.Copies); err != nil {
			return c, nil, err
		}
		if n := len(places); n == 0 || places[n-1].ID != p.ID {
			places = append(places, p)
		}
		last := &places[len(places)-1]
		if e.ID != 0 {
			last.Editions = append(last.Editions, e)
			if e.Copies > 0 {
				last.Have = true
			}
		}
	}
	return c, places, rows.Err()
}

// setOfPlace — собрание, которому принадлежит место; ErrNotFound — места нет или оно не
// в собрании (а, например, в издательской серии).
func (s *Store) setOfPlace(placeID int64) (int64, error) {
	var setID int64
	err := s.db.QueryRow(`SELECT c.id FROM collection_slot s JOIN collection c ON c.id = s.collection_id
		WHERE s.id = ? AND c.kind = 'set'`, placeID).Scan(&setID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return setID, err
}

// SplitVolume делит том на книги: место «т. 3» становится «т. 3, кн. 1» … «кн. N».
// Издания, закрывавшие весь том, переходят на первую книгу — человек поправит.
func (s *Store) SplitVolume(setID int64, volume, books int) error {
	if books < 2 || books > 20 {
		return InputError("книг в томе — от 2 до 20")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var slotID int64
	err = tx.QueryRow(`SELECT s.id FROM collection_slot s JOIN collection c ON c.id = s.collection_id
		WHERE s.collection_id = ? AND c.kind = 'set' AND s.volume = ? AND s.book = 0`, setID, volume).Scan(&slotID)
	if errors.Is(err, sql.ErrNoRows) {
		return InputError(fmt.Sprintf("в собрании нет целого т. %d — он уже разделён или не заведён", volume))
	}
	if err != nil {
		return err
	}
	// место «весь том» становится первой книгой: связи с изданиями остаются на нём
	if _, err := tx.Exec(`UPDATE collection_slot SET book = 1 WHERE id = ?`, slotID); err != nil {
		return err
	}
	for b := 2; b <= books; b++ {
		if _, err := tx.Exec(`INSERT INTO collection_slot (collection_id, volume, book) VALUES (?, ?, ?)`,
			setID, volume, b); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AddPlace добавляет место: следующий по номеру том (label пусто) или дополнительный
// том без номера с подписью («Указатель», «доп. том»).
func (s *Store) AddPlace(setID int64, label string) error {
	if _, _, err := s.Set(setID); err != nil {
		return err
	}
	label = strings.TrimSpace(label)
	if label != "" {
		_, err := s.db.Exec(`INSERT INTO collection_slot (collection_id, volume, book, label) VALUES (?, NULL, 0, ?)`, setID, label)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return InputError("место «" + label + "» уже есть")
		}
		return err
	}
	_, err := s.db.Exec(`INSERT INTO collection_slot (collection_id, volume, book)
		SELECT ?, COALESCE(MAX(volume), 0) + 1, 0 FROM collection_slot WHERE collection_id = ?`, setID, setID)
	return err
}

// DeletePlace убирает место (опечатка, лишний том). Издания остаются в каталоге.
func (s *Store) DeletePlace(placeID int64) (setID int64, err error) {
	if setID, err = s.setOfPlace(placeID); err != nil {
		return 0, err
	}
	_, err = s.db.Exec(`DELETE FROM collection_slot WHERE id = ?`, placeID)
	return setID, err
}

// PlaceRef — место, на которое ставится издание: том и, если том разделён, книга.
type PlaceRef struct{ Volume, Book int }

// ParseVolumes читает номера томов, как их пишут: «3», «6-7» (сдвоенный том — два места),
// «2, 5». Книга внутри тома задаётся отдельно и действует на каждый том.
func ParseVolumes(s string, book int) ([]PlaceRef, error) {
	var out []PlaceRef
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		from, to, ok := strings.Cut(strings.NewReplacer("–", "-", "—", "-").Replace(part), "-")
		a, err1 := atoiStrict(from)
		b, err2 := a, error(nil)
		if ok {
			b, err2 = atoiStrict(to)
		}
		if err1 != nil || err2 != nil || a < 1 || b < a || b-a > 10 {
			return nil, InputError("том — номер или диапазон: «3», «6-7»")
		}
		for v := a; v <= b; v++ {
			out = append(out, PlaceRef{v, book})
		}
	}
	if len(out) == 0 {
		return nil, InputError("укажите номер тома")
	}
	return out, nil
}

// atoiStrict — номер тома: только цифры, без знака («+3», « 3» — не номер).
func atoiStrict(s string) (int, error) {
	if s == "" || strings.TrimLeft(s, "0123456789") != "" {
		return 0, fmt.Errorf("не номер: %q", s)
	}
	return strconv.Atoi(s)
}

// PutInSet ставит издание на места собрания. Места должны существовать: собрание задаёт
// свой состав, а опечатка в номере тома не должна молча заводить новый том.
func (s *Store) PutInSet(editionID, setID int64, refs []PlaceRef) error {
	if _, err := s.Edition(editionID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind string
	err = tx.QueryRow(`SELECT kind FROM collection WHERE id = ?`, setID).Scan(&kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if kind != "set" { // нет такого или это издательская серия
		return ErrNotFound
	}
	for _, ref := range refs {
		var slotID int64
		err := tx.QueryRow(`SELECT id FROM collection_slot WHERE collection_id = ? AND volume = ? AND book = ?`,
			setID, ref.Volume, ref.Book).Scan(&slotID)
		if errors.Is(err, sql.ErrNoRows) {
			if ref.Book == 0 {
				var split int
				tx.QueryRow(`SELECT COUNT(*) FROM collection_slot WHERE collection_id = ? AND volume = ? AND book > 0`,
					setID, ref.Volume).Scan(&split)
				if split > 0 {
					return InputError(fmt.Sprintf("т. %d разделён на книги — укажите книгу", ref.Volume))
				}
			}
			return InputError("в собрании нет места " + placeName(ref.Volume, ref.Book, ""))
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO slot_edition (slot_id, edition_id) VALUES (?, ?)`, slotID, editionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Unplace снимает издание с места; само издание остаётся в каталоге.
func (s *Store) Unplace(placeID, editionID int64) (setID int64, err error) {
	if setID, err = s.setOfPlace(placeID); err != nil {
		return 0, err
	}
	_, err = s.db.Exec(`DELETE FROM slot_edition WHERE slot_id = ? AND edition_id = ?`, placeID, editionID)
	return setID, err
}

// SetWorth — во что оценены экземпляры собрания: сумма действующих оценок по нижней и
// верхней границе. Экземпляр сдвоенного тома (два места) считается один раз.
type SetWorth struct {
	Copies  int // экземпляров изданий собрания
	Valued  int // из них оценено
	Total   int
	TotalTo int
}

func (s *Store) SetWorth(setID int64) (SetWorth, error) {
	var w SetWorth
	err := s.db.QueryRow(`SELECT COUNT(*), COUNT(v.id), COALESCE(SUM(v.amount), 0),
			COALESCE(SUM(COALESCE(v.amount_to, v.amount)), 0)
		FROM book_copy c LEFT JOIN valuation v ON v.id = (`+currentValuation+`)
		WHERE c.edition_id IN (SELECT se.edition_id FROM slot_edition se
			JOIN collection_slot s ON s.id = se.slot_id WHERE s.collection_id = ?)`, setID).
		Scan(&w.Copies, &w.Valued, &w.Total, &w.TotalTo)
	return w, err
}

// EditionPlace — где издание стоит в собраниях: для карточки книги.
type EditionPlace struct {
	SetID    int64
	SetTitle string
	PlaceID  int64
	Place    string
}

func (s *Store) EditionPlaces(editionID int64) ([]EditionPlace, error) {
	rows, err := s.db.Query(`SELECT c.id, c.title, s.id, COALESCE(s.volume, 0), s.book, s.label
		FROM slot_edition se JOIN collection_slot s ON s.id = se.slot_id JOIN collection c ON c.id = s.collection_id
		WHERE se.edition_id = ? AND c.kind = 'set'
		ORDER BY fold(c.title), s.volume, s.book`, editionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EditionPlace
	for rows.Next() {
		var p EditionPlace
		var v, b int
		var label string
		if err := rows.Scan(&p.SetID, &p.SetTitle, &p.PlaceID, &v, &b, &label); err != nil {
			return nil, err
		}
		p.Place = placeName(v, b, label)
		out = append(out, p)
	}
	return out, rows.Err()
}
