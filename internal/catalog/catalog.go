// Package catalog — операции над каталогом: издания, экземпляры, полки, комплекты.
package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/isbn"
)

// Store — каталог поверх открытой базы.
type Store struct{ db *sql.DB }

func New(d *sql.DB) *Store { return &Store{db: d} }

// DB — открытая база: для резервной копии, которой нужен снимок всей базы целиком.
func (s *Store) DB() *sql.DB { return s.db }

// Person — человек издания в порядке титульного листа.
type Person struct {
	Name string
	Role string // author, editor, translator, illustrator, compiler; пусто = author
}

// Edition — что напечатано.
type Edition struct {
	ID         int64
	Title      string
	Publisher  string
	City       string
	Year       int // 0 — год неизвестен
	Volume     string
	BBK        string
	UDK        string
	AuthorSign string
	Source     string // пусто = manual
	SourceURL  string
	LiveLibID  string // пусто — не из LiveLib
	Notes      string
	Cover      string // имя файла обложки в каталоге обложек; пусто — нет. Ставится через SetCover
	Series     string // издательская серия; пусто — вне серии
	SeriesNo   string // номер в серии («12») или подпись («12а»); пусто — без номера
	SeriesID   int64  // заполняет только Edition()
	CoverType  string // тип обложки: «твёрдая», «мягкая», «суперобложка»
	Format     string // как напечатано: «84x108/32 (130x200 мм)»
	Pages      int    // 0 — неизвестно
	Genres     []string
	Details
	People []Person
	ISBNs  []isbn.ISBN
}

// Details — выходные данные с титула и последней страницы: заполняются по желанию,
// в списках не участвуют, поэтому отдельной группой — с одним местом чтения и записи.
type Details struct {
	OrigTitle     string
	OrigLanguage  string // «английский»: переведено с
	Language      string // язык издания, если не русский
	EditionNote   string // «2-е изд., испр. и доп.»
	Circulation   int    // тираж, экз.; 0 — неизвестен
	PrintPrice    string // цена на книге: «1 р. 20 к.»
	Printer       string // типография
	AgeRating     string // «16+»
	Annotation    string
	Contents      string // содержание сборника: по строке на произведение
	Paper         string // «мелованная»
	Design        string // оформление: «ляссе, тиснение»
	Illustrations string // «цветные», «чёрно-белые»
	Dimensions    string // размеры, мм: «267x211x17»
	Weight        int    // вес, г; 0 — неизвестен
}

// detailCols — колонки Details в порядке полей; args и dest — значения для записи и чтения.
const detailCols = `orig_title, orig_language, language, edition_note, circulation, print_price, printer,
	age_rating, annotation, contents, paper, design, illustrations, dimensions, weight`

// detailSet — то же для UPDATE.
const detailSet = `orig_title = ?, orig_language = ?, language = ?, edition_note = ?, circulation = ?,
	print_price = ?, printer = ?, age_rating = ?, annotation = ?, contents = ?, paper = ?, design = ?,
	illustrations = ?, dimensions = ?, weight = ?`

func (d *Details) args() []any {
	return []any{d.OrigTitle, d.OrigLanguage, d.Language, d.EditionNote, nullInt(d.Circulation), d.PrintPrice,
		d.Printer, d.AgeRating, d.Annotation, d.Contents, d.Paper, d.Design, d.Illustrations, d.Dimensions,
		nullInt(d.Weight)}
}

// dest — куда читать; числа, которые могут быть NULL, — во временные circ и weight.
func (d *Details) dest(circ, weight *sql.NullInt64) []any {
	return []any{&d.OrigTitle, &d.OrigLanguage, &d.Language, &d.EditionNote, circ, &d.PrintPrice,
		&d.Printer, &d.AgeRating, &d.Annotation, &d.Contents, &d.Paper, &d.Design, &d.Illustrations,
		&d.Dimensions, weight}
}

// AddEdition сохраняет издание вместе с людьми и ISBN одной транзакцией.
// ISBNs можно заполнить через ParseISBNs; номера с неверной суммой сохраняются с пометкой.
func (s *Store) AddEdition(e *Edition) (int64, error) {
	if strings.TrimSpace(e.Title) == "" {
		return 0, errors.New("у издания нет названия")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	src := e.Source
	if src == "" {
		src = "manual"
	}
	args := append([]any{e.Title, e.Publisher, e.City, nullInt(e.Year), e.Volume, e.BBK, e.UDK, e.AuthorSign,
		src, e.SourceURL, nullStr(e.LiveLibID), e.Notes, e.CoverType, e.Format, nullInt(e.Pages)}, e.Details.args()...)
	res, err := tx.Exec(`INSERT INTO edition
		(title, publisher, city, year, volume, bbk, udk, author_sign, source, source_url, livelib_id, notes,
		 cover_type, format, pages, `+detailCols+`)
		VALUES (`+strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ")+`)`, args...)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := writeChildren(tx, id, e); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	e.ID = id
	return id, nil
}

// writeChildren пишет людей и ISBN издания id внутри транзакции tx.
func writeChildren(tx *sql.Tx, id int64, e *Edition) error {
	for i, p := range e.People {
		role := p.Role
		if role == "" {
			role = "author"
		}
		if _, err := tx.Exec(`INSERT INTO edition_person (edition_id, position, name, role) VALUES (?, ?, ?, ?)`,
			id, i+1, p.Name, role); err != nil {
			return fmt.Errorf("%s: %w", p.Name, err)
		}
	}
	for _, n := range e.ISBNs {
		// один номер, записанный в двух видах (10 и 13 цифр), — одна строка
		if _, err := tx.Exec(`INSERT OR IGNORE INTO edition_isbn (edition_id, isbn, valid) VALUES (?, ?, ?)`,
			id, n.Norm, n.Valid); err != nil {
			return err
		}
	}
	if err := writeGenres(tx, id, e.Genres); err != nil {
		return err
	}
	return writeSeries(tx, id, e)
}

// ParseISBNs разбирает номера как они пришли, в каждой строке их может быть несколько.
// «Отсутствует» — отсутствие номера, а не ошибка; а то, что похоже на номер, но не
// разобралось, возвращается в bad: импорт обязан это показать, а не потерять.
func ParseISBNs(raw ...string) (good []isbn.ISBN, bad []string) {
	for _, r := range raw {
		g, b := isbn.List(r)
		good = append(good, g...)
		bad = append(bad, b...)
	}
	return good, bad
}

// EditionsByISBN — все издания с этим номером. Их может быть несколько: издательства
// используют ISBN повторно, поэтому выбор издания остаётся за человеком.
func (s *Store) EditionsByISBN(raw string) ([]Edition, error) {
	n, err := isbn.Parse(raw)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT e.id, e.title, e.publisher, e.city, COALESCE(e.year, 0), e.volume
		FROM edition e JOIN edition_isbn i ON i.edition_id = e.id
		WHERE i.isbn = ? ORDER BY e.year, e.id`, n.Norm)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edition
	for rows.Next() {
		var e Edition
		if err := rows.Scan(&e.ID, &e.Title, &e.Publisher, &e.City, &e.Year, &e.Volume); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Shelf возвращает id полки, заводя её при первом упоминании.
func (s *Store) Shelf(room, bookcase, shelf string) (int64, error) {
	// Обычно полка уже есть — тогда хватает чтения, без блокировки записи.
	// Не нашлась — вставка; DO NOTHING не вернёт строку, если полку только что завёл
	// другой запрос, поэтому тогда ещё одно чтение.
	const find = `SELECT id FROM shelf WHERE room = ? AND bookcase = ? AND shelf = ?`
	var id int64
	err := s.db.QueryRow(find, room, bookcase, shelf).Scan(&id)
	if !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	err = s.db.QueryRow(`INSERT INTO shelf (room, bookcase, shelf) VALUES (?, ?, ?)
		ON CONFLICT DO NOTHING RETURNING id`, room, bookcase, shelf).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.QueryRow(find, room, bookcase, shelf).Scan(&id)
	}
	return id, err
}

// Copy — экземпляр на полке.
type Copy struct {
	ID           int64
	EditionID    int64
	ShelfID      int64 // 0 — полка не указана
	Condition    string
	Binding      string
	Bookplate    string
	Inscriptions string
	History      string
	NoLend       bool
	Notes        string
	Grade        int  // состояние 1–5 (см. Grades); 0 — не оценено
	Cherished    bool // «дорожу»: ценность памятная или собирательская
	Read         bool // прочитана
}

func (s *Store) AddCopy(c *Copy) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO book_copy
		(edition_id, shelf_id, condition, binding, bookplate, inscriptions, history, no_lend, notes, grade, cherished, is_read)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.EditionID, nullInt64(c.ShelfID), c.Condition, c.Binding, c.Bookplate, c.Inscriptions,
		c.History, c.NoLend, c.Notes, nullInt(c.Grade), c.Cherished, c.Read)
	if err != nil {
		return 0, err
	}
	c.ID, err = res.LastInsertId()
	return c.ID, err
}

// Collection — собрание/многотомник (Kind "set") или издательская серия ("series").
type Collection struct {
	ID               int64
	Kind             string
	Title            string
	Publisher        string
	YearFrom, YearTo int
	VolumesAnnounced int // 0 — неизвестно
	Notes            string
}

func (s *Store) AddCollection(c *Collection) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO collection
		(kind, title, publisher, year_from, year_to, volumes_announced, notes) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.Kind, c.Title, c.Publisher, nullInt(c.YearFrom), nullInt(c.YearTo), nullInt(c.VolumesAnnounced), c.Notes)
	if err != nil {
		return 0, err
	}
	c.ID, err = res.LastInsertId()
	return c.ID, err
}

// AddSlot заводит место в комплекте: том volume, книга book внутри тома (0 — том не делится).
func (s *Store) AddSlot(collectionID int64, volume, book int, label string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO collection_slot (collection_id, volume, book, label) VALUES (?, ?, ?, ?)`,
		collectionID, nullInt(volume), book, label)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LinkSlot отмечает, что издание закрывает место. Сдвоенный том связывается с двумя местами.
func (s *Store) LinkSlot(slotID, editionID int64) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO slot_edition (slot_id, edition_id) VALUES (?, ?)`, slotID, editionID)
	return err
}

// Slot — место комплекта и есть ли на него экземпляр.
type Slot struct {
	Volume int
	Book   int
	Label  string
	Have   bool
}

// Completeness — комплектность: место считается закрытым, если на полке есть экземпляр
// хотя бы одного издания, связанного с ним. Считаются книги, а не тома.
type Completeness struct {
	Slots   []Slot
	Have    int
	Missing []Slot
}

func (s *Store) Completeness(collectionID int64) (Completeness, error) {
	var kind string
	if err := s.db.QueryRow(`SELECT kind FROM collection WHERE id = ?`, collectionID).Scan(&kind); err != nil {
		return Completeness{}, err
	}
	if kind != "set" {
		return Completeness{}, fmt.Errorf("комплект %d — серия: у серии считается состав, а не полнота", collectionID)
	}
	rows, err := s.db.Query(`SELECT COALESCE(s.volume, 0), s.book, s.label,
			EXISTS (SELECT 1 FROM slot_edition se JOIN book_copy c ON c.edition_id = se.edition_id
			        WHERE se.slot_id = s.id)
		FROM collection_slot s WHERE s.collection_id = ? ORDER BY s.volume, s.book`, collectionID)
	if err != nil {
		return Completeness{}, err
	}
	defer rows.Close()
	var r Completeness
	for rows.Next() {
		var sl Slot
		if err := rows.Scan(&sl.Volume, &sl.Book, &sl.Label, &sl.Have); err != nil {
			return Completeness{}, err
		}
		r.Slots = append(r.Slots, sl)
		if sl.Have {
			r.Have++
		} else {
			r.Missing = append(r.Missing, sl)
		}
	}
	return r, rows.Err()
}

// Stats — счётчики для команды stats.
type Stats struct{ Editions, Copies, Shelves, Collections int }

func (s *Store) Stats() (Stats, error) {
	var st Stats
	err := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM edition), (SELECT COUNT(*) FROM book_copy),
		(SELECT COUNT(*) FROM shelf), (SELECT COUNT(*) FROM collection)`).
		Scan(&st.Editions, &st.Copies, &st.Shelves, &st.Collections)
	return st, err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullInt64(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
