package catalog

import (
	"database/sql"
	"errors"
)

// Footprint — что уйдёт вместе с изданием. Показывается человеку до удаления:
// оценки и история выдач вводились руками, и терять их молча нельзя.
type Footprint struct {
	Copies     int  // экземпляры
	Valuations int  // оценки экземпляров, все записи
	PastLoans  int  // возвращённые выдачи
	Places     int  // места в собраниях сочинений (сами места останутся пустыми)
	Lent       bool // экземпляр на руках — удалять нельзя
}

// EditionFootprint считает, что связано с изданием.
func (s *Store) EditionFootprint(id int64) (Footprint, error) {
	var f Footprint
	err := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM book_copy WHERE edition_id = ?),
		(SELECT COUNT(*) FROM valuation v JOIN book_copy c ON c.id = v.copy_id WHERE c.edition_id = ?),
		(SELECT COUNT(*) FROM loan l JOIN book_copy c ON c.id = l.copy_id
			WHERE c.edition_id = ? AND l.returned_on IS NOT NULL),
		(SELECT COUNT(*) FROM slot_edition se JOIN collection_slot s ON s.id = se.slot_id
			JOIN collection k ON k.id = s.collection_id WHERE se.edition_id = ? AND k.kind = 'set'),
		EXISTS (SELECT 1 FROM loan l JOIN book_copy c ON c.id = l.copy_id
			WHERE c.edition_id = ? AND l.returned_on IS NULL)`, id, id, id, id, id).
		Scan(&f.Copies, &f.Valuations, &f.PastLoans, &f.Places, &f.Lent)
	return f, err
}

// DeleteEdition убирает книгу из каталога целиком — заведённую по ошибке или разобранную
// неверно: издание, его экземпляры с оценками и историей выдач, людей, ISBN, жанры,
// место в серии. Места в собраниях сочинений остаются — пустыми: собрание описывает,
// сколько томов должно быть, а не что есть. Справки о людях (годы жизни, о человеке)
// не трогаются: человек мог быть и в других книгах, а справку писали руками.
//
// Книга, экземпляр которой на руках, не удаляется: долг пропал бы без следа.
// Возвращает, что удалено; файл обложки убрать должен вызывающий, после удачной записи.
func (s *Store) DeleteEdition(id int64) (Removed, error) {
	var r Removed
	tx, err := s.db.Begin()
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	err = tx.QueryRow(`SELECT title, volume, cover_path FROM edition WHERE id = ?`, id).Scan(&r.Title, &r.Volume, &r.Cover)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	} else if err != nil {
		return r, err
	}
	var lent bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM loan l JOIN book_copy c ON c.id = l.copy_id
		WHERE c.edition_id = ? AND l.returned_on IS NULL)`, id).Scan(&lent); err != nil {
		return r, err
	}
	if lent {
		return r, InputError("экземпляр этой книги на руках — сначала отметьте возврат, потом удаляйте")
	}
	// экземпляры держат издание (ON DELETE RESTRICT); выдачи и оценки уходят с ними каскадом
	if _, err := tx.Exec(`DELETE FROM book_copy WHERE edition_id = ?`, id); err != nil {
		return r, err
	}
	// люди, ISBN, жанры, места в сериях и собраниях — каскадом от издания
	if _, err := tx.Exec(`DELETE FROM edition WHERE id = ?`, id); err != nil {
		return r, err
	}
	if err := dropEmptySeries(tx); err != nil {
		return r, err
	}
	if err := dropOrphanGenres(tx); err != nil {
		return r, err
	}
	return r, tx.Commit()
}

// Removed — что было удалено: название для отметки в каталоге и файл обложки.
type Removed struct {
	Title, Volume, Cover string
}
