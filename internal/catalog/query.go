package catalog

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/db"
	"github.com/aleksandrgradoboev-svg/filin/internal/isbn"
)

// Row — строка списка книг.
type Row struct {
	ID        int64
	Title     string
	Authors   string // авторы через запятую в порядке титула; редакторы и переводчики сюда не входят
	Publisher string
	Year      int
	Volume    string
	Copies    int
	Cover     string // имя файла обложки; пусто — обложки нет
	Borrower  string // у кого экземпляр на руках; пусто — все дома
	Overdue   bool   // срок возврата прошёл
}

// Search — страница списка изданий. Пустой запрос — весь каталог по алфавиту.
// Каждое слово запроса должно найтись в названии или в имени человека издания;
// запрос, похожий на ISBN, ищется ещё и по номеру. total — сколько всего нашлось.
func (s *Store) Search(q string, limit, offset int) (rows []Row, total int, err error) {
	return s.SearchIn(q, 0, limit, offset)
}

// SearchIn — то же, что Search, но только в жанре genreID (0 — во всех).
func (s *Store) SearchIn(q string, genreID int64, limit, offset int) (rows []Row, total int, err error) {
	var conds []string
	var args []any
	for _, w := range strings.Fields(db.Fold(q)) {
		conds = append(conds, "instr(hay, ?) > 0")
		args = append(args, w)
	}
	match := "1"
	if len(conds) > 0 {
		match = strings.Join(conds, " AND ")
	}
	if n, err := isbn.Parse(q); err == nil {
		match = "(" + match + ") OR e.id IN (SELECT edition_id FROM edition_isbn WHERE isbn = ?)"
		args = append(args, n.Norm)
	}
	// жанр — отдельным AND: совпадение по ISBN не должно выводить из жанра
	if genreID != 0 {
		match = "(" + match + ") AND e.id IN (SELECT edition_id FROM edition_genre WHERE genre_id = ?)"
		args = append(args, genreID)
	}
	args = append(args, limit, offset)
	r, err := s.db.Query(`WITH e AS (
			SELECT e.*, fold(e.title || ' ' || COALESCE(
				(SELECT group_concat(p.name, ' ') FROM edition_person p WHERE p.edition_id = e.id), '')
				|| ' ' || COALESCE((SELECT group_concat(c.title, ' ') FROM slot_edition se
					JOIN collection_slot s ON s.id = se.slot_id JOIN collection c ON c.id = s.collection_id
					WHERE se.edition_id = e.id), '')
				|| ' ' || COALESCE((SELECT group_concat(g.name, ' ') FROM edition_genre eg
					JOIN genre g ON g.id = eg.genre_id WHERE eg.edition_id = e.id), '')
				|| ' ' || e.orig_title) AS hay
			FROM edition e)
		SELECT e.id, e.title, e.publisher, COALESCE(e.year, 0), e.volume, e.cover_path,
			COALESCE((SELECT group_concat(p.name, ', ' ORDER BY p.position) FROM edition_person p
			          WHERE p.edition_id = e.id AND p.role = 'author'), ''),
			(SELECT COUNT(*) FROM book_copy c WHERE c.edition_id = e.id),
			COALESCE(ln.borrower, ''), COALESCE(ln.due_on, ''),
			COUNT(*) OVER ()
		FROM e
		-- на руках: если экземпляров у разных людей несколько — тот, чей срок ближе
		LEFT JOIN loan ln ON ln.id = (SELECT l.id FROM loan l JOIN book_copy c ON c.id = l.copy_id
			WHERE c.edition_id = e.id AND l.returned_on IS NULL
			ORDER BY l.due_on IS NULL, l.due_on, l.lent_on LIMIT 1)
		WHERE `+match+`
		ORDER BY fold(e.title), e.year, e.id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer r.Close()
	today := Today()
	for r.Next() {
		var x Row
		var due string
		if err := r.Scan(&x.ID, &x.Title, &x.Publisher, &x.Year, &x.Volume, &x.Cover, &x.Authors, &x.Copies,
			&x.Borrower, &due, &total); err != nil {
			return nil, 0, err
		}
		x.Overdue = due != "" && due < today
		rows = append(rows, x)
	}
	return rows, total, r.Err()
}

// ErrNotFound — записи с таким id нет.
var ErrNotFound = errors.New("не найдено")

// Edition читает издание целиком: поля, люди, ISBN.
func (s *Store) Edition(id int64) (Edition, error) {
	e := Edition{ID: id}
	var livelib sql.NullString
	var circ, weight sql.NullInt64
	dest := append([]any{&e.Title, &e.Publisher, &e.City, &e.Year, &e.Volume, &e.BBK, &e.UDK, &e.AuthorSign,
		&e.Source, &e.SourceURL, &livelib, &e.Notes, &e.Cover, &e.CoverType, &e.Format, &e.Pages},
		e.Details.dest(&circ, &weight)...)
	err := s.db.QueryRow(`SELECT title, publisher, city, COALESCE(year, 0), volume, bbk, udk, author_sign,
		source, source_url, livelib_id, notes, cover_path, cover_type, format, COALESCE(pages, 0), `+detailCols+`
		FROM edition WHERE id = ?`, id).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	if err != nil {
		return e, err
	}
	e.LiveLibID = livelib.String
	e.Circulation, e.Weight = int(circ.Int64), int(weight.Int64)
	if err := s.readSeries(&e); err != nil {
		return e, err
	}
	gs, err := s.EditionGenres(id)
	if err != nil {
		return e, err
	}
	for _, g := range gs {
		e.Genres = append(e.Genres, g.Name)
	}

	rows, err := s.db.Query(`SELECT name, role FROM edition_person WHERE edition_id = ? ORDER BY position`, id)
	if err != nil {
		return e, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.Name, &p.Role); err != nil {
			return e, err
		}
		e.People = append(e.People, p)
	}
	if err := rows.Err(); err != nil {
		return e, err
	}

	rows, err = s.db.Query(`SELECT isbn, valid FROM edition_isbn WHERE edition_id = ? ORDER BY isbn`, id)
	if err != nil {
		return e, err
	}
	defer rows.Close()
	for rows.Next() {
		var n isbn.ISBN
		if err := rows.Scan(&n.Norm, &n.Valid); err != nil {
			return e, err
		}
		n.Raw = n.Norm
		e.ISBNs = append(e.ISBNs, n)
	}
	return e, rows.Err()
}

// EditionByLiveLibID — id издания, уже импортированного с LiveLib; 0 — такого нет.
func (s *Store) EditionByLiveLibID(llID string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM edition WHERE livelib_id = ?`, llID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// UpdateEdition переписывает поля издания, его людей и ISBN одной транзакцией.
// Происхождение записи (source, source_url, livelib_id) не меняется: правка руками
// не делает книгу из LiveLib ручной, а повторный импорт должен её узнать.
func (s *Store) UpdateEdition(e *Edition) error {
	if strings.TrimSpace(e.Title) == "" {
		return errors.New("у издания нет названия")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	args := append([]any{e.Title, e.Publisher, e.City, nullInt(e.Year), e.Volume, e.BBK, e.UDK, e.AuthorSign,
		e.Notes, e.CoverType, e.Format, nullInt(e.Pages)}, e.Details.args()...)
	res, err := tx.Exec(`UPDATE edition SET title = ?, publisher = ?, city = ?, year = ?, volume = ?,
		bbk = ?, udk = ?, author_sign = ?, notes = ?, cover_type = ?, format = ?, pages = ?, `+detailSet+`
		WHERE id = ?`, append(args, e.ID)...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	for _, t := range []string{"edition_person", "edition_isbn"} {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE edition_id = ?`, e.ID); err != nil {
			return err
		}
	}
	if err := writeChildren(tx, e.ID, e); err != nil {
		return err
	}
	return tx.Commit()
}

// SetAnnotation ставит изданию аннотацию и заметки, остального не касаясь: дополнение
// из внешнего источника не должно переписывать людей, ISBN и жанры.
func (s *Store) SetAnnotation(id int64, annotation, notes string) error {
	res, err := s.db.Exec(`UPDATE edition SET annotation = ?, notes = ? WHERE id = ?`, annotation, notes, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCover ставит изданию обложку (имя файла; пусто — снять) и возвращает прежнюю,
// чтобы вызывающий убрал старый файл.
func (s *Store) SetCover(id int64, name string) (old string, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := tx.QueryRow(`SELECT cover_path FROM edition WHERE id = ?`, id).Scan(&old); errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE edition SET cover_path = ? WHERE id = ?`, name, id); err != nil {
		return "", err
	}
	return old, tx.Commit()
}

// ShelvedCopy — экземпляр вместе с названием своей полки.
type ShelvedCopy struct {
	Copy
	Room, Bookcase, Shelf string     // пусто — полка не указана
	Loan                  *Loan      // текущая выдача; nil — дома
	Value                 *Valuation // действующая оценка; nil — не оценён
}

// Copies — экземпляры издания в порядке заведения.
func (s *Store) Copies(editionID int64) ([]ShelvedCopy, error) {
	return s.copies(`c.edition_id = ? ORDER BY c.id`, editionID)
}

// CopyByID читает один экземпляр.
func (s *Store) CopyByID(id int64) (ShelvedCopy, error) {
	cs, err := s.copies(`c.id = ?`, id)
	if err != nil {
		return ShelvedCopy{}, err
	}
	if len(cs) == 0 {
		return ShelvedCopy{}, ErrNotFound
	}
	return cs[0], nil
}

func (s *Store) copies(where string, arg any) ([]ShelvedCopy, error) {
	rows, err := s.db.Query(`SELECT c.id, c.edition_id, COALESCE(c.shelf_id, 0), c.condition, c.binding,
			c.bookplate, c.inscriptions, c.history, c.no_lend, c.notes, COALESCE(c.grade, 0), c.cherished, c.is_read,
			COALESCE(sh.room, ''), COALESCE(sh.bookcase, ''), COALESCE(sh.shelf, ''),
			COALESCE(l.id, 0), COALESCE(l.borrower, ''), COALESCE(l.lent_on, ''), COALESCE(l.due_on, ''),
			COALESCE(l.note, ''),
			COALESCE(v.id, 0), COALESCE(v.amount, 0), COALESCE(v.amount_to, 0), COALESCE(v.valued_on, ''), COALESCE(v.basis, ''),
			COALESCE(v.source_url, '')
		FROM book_copy c LEFT JOIN shelf sh ON sh.id = c.shelf_id
		LEFT JOIN loan l ON l.copy_id = c.id AND l.returned_on IS NULL
		LEFT JOIN valuation v ON v.id = (`+currentValuation+`)
		WHERE `+where, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShelvedCopy
	for rows.Next() {
		var c ShelvedCopy
		var l Loan      // новая на каждой строке: c.Loan хранит указатель на неё
		var v Valuation // то же для c.Value
		if err := rows.Scan(&c.ID, &c.EditionID, &c.ShelfID, &c.Condition, &c.Binding, &c.Bookplate,
			&c.Inscriptions, &c.History, &c.NoLend, &c.Notes, &c.Grade, &c.Cherished, &c.Read,
			&c.Room, &c.Bookcase, &c.Shelf,
			&l.ID, &l.Borrower, &l.LentOn, &l.DueOn, &l.Note,
			&v.ID, &v.Amount, &v.AmountTo, &v.ValuedOn, &v.Basis, &v.SourceURL); err != nil {
			return nil, err
		}
		if l.ID != 0 {
			l.CopyID = c.ID
			c.Loan = &l
		}
		if v.ID != 0 {
			v.CopyID = c.ID
			c.Value = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateCopy переписывает экземпляр; издание у экземпляра не меняется.
func (s *Store) UpdateCopy(c *Copy) error {
	res, err := s.db.Exec(`UPDATE book_copy SET shelf_id = ?, condition = ?, binding = ?, bookplate = ?,
		inscriptions = ?, history = ?, no_lend = ?, notes = ?, grade = ?, cherished = ?, is_read = ? WHERE id = ?`,
		nullInt64(c.ShelfID), c.Condition, c.Binding, c.Bookplate, c.Inscriptions, c.History, c.NoLend, c.Notes,
		nullInt(c.Grade), c.Cherished, c.Read, c.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCopy убирает экземпляр — заведённый по ошибке или ушедший из библиотеки.
// Издание остаётся: оно описывает напечатанное, а не то, что стоит на полке.
// Экземпляр на руках не удаляется: выдача ушла бы вместе с ним, и долг пропал бы без следа.
func (s *Store) DeleteCopy(id int64) error {
	var lent bool
	if err := s.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM loan WHERE copy_id = ? AND returned_on IS NULL)`, id).
		Scan(&lent); err != nil {
		return err
	}
	if lent {
		return InputError("экземпляр на руках — сначала отметьте возврат, потом убирайте")
	}
	_, err := s.db.Exec(`DELETE FROM book_copy WHERE id = ?`, id)
	return err
}

// ShelfRow — полка для подсказок в форме.
type ShelfRow struct {
	ID                    int64
	Room, Bookcase, Shelf string
}

func (s *Store) Shelves() ([]ShelfRow, error) {
	rows, err := s.db.Query(`SELECT id, room, bookcase, shelf FROM shelf ORDER BY room, bookcase, shelf`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShelfRow
	for rows.Next() {
		var sh ShelfRow
		if err := rows.Scan(&sh.ID, &sh.Room, &sh.Bookcase, &sh.Shelf); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}
