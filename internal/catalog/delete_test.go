package catalog

import (
	"errors"
	"testing"
)

// Удаление книги: уходит всё, что к ней привязано, остаётся чужое — другая книга той же
// серии, места собрания, справки о людях. Книга на руках не удаляется.
func TestDeleteEditionRemovesEverythingItOwns(t *testing.T) {
	s := newStore(t)
	ed, err := s.AddEdition(&Edition{Title: "Т. 2", Series: "Воспоминания. Мемуары", SeriesNo: "7",
		People: []Person{{Name: "Деникин Антон Иванович", Role: "author"}},
		ISBNs:  isbns(t, "978-985-18-4088-1"), Genres: []string{"Мемуары"}})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.AddEdition(&Edition{Title: "Другая книга серии", Series: "Воспоминания. Мемуары", SeriesNo: "8"})
	lonely, _ := s.AddEdition(&Edition{Title: "Единственная в серии", Series: "Серия-опечатка"})
	if err := s.SavePerson(PersonInfo{Name: "Деникин Антон Иванович", Life: "1872–1947"}); err != nil {
		t.Fatal(err)
	}
	set, err := s.CreateSet(&Collection{Title: "Очерки русской смуты", VolumesAnnounced: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutInSet(ed, set, []PlaceRef{{Volume: 2}}); err != nil {
		t.Fatal(err)
	}
	cp, _ := s.AddCopy(&Copy{EditionID: ed})
	if _, err := s.AddValuation(&Valuation{CopyID: cp, Amount: 900}); err != nil {
		t.Fatal(err)
	}
	loan, err := s.Lend(&Loan{CopyID: cp, Borrower: "Сосед"})
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE edition SET cover_path = '5-1.jpg' WHERE id = ?`, ed)

	// на руках — отказ, и ничего не тронуто
	if f, _ := s.EditionFootprint(ed); !f.Lent {
		t.Error("выдача не видна в подсчёте")
	}
	if _, err := s.DeleteEdition(ed); !IsInput(err) {
		t.Fatalf("книгу на руках удалили: %v", err)
	}
	if _, err := s.Edition(ed); err != nil {
		t.Fatalf("после отказа книги нет: %v", err)
	}

	if _, err := s.Return(loan, ""); err != nil {
		t.Fatal(err)
	}
	f, err := s.EditionFootprint(ed)
	if err != nil {
		t.Fatal(err)
	}
	if f != (Footprint{Copies: 1, Valuations: 1, PastLoans: 1, Places: 1}) {
		t.Errorf("подсчёт: %+v", f)
	}
	gone, err := s.DeleteEdition(ed)
	if err != nil {
		t.Fatal(err)
	}
	if gone != (Removed{Title: "Т. 2", Cover: "5-1.jpg"}) {
		t.Errorf("удалено: %+v", gone)
	}
	// свой жанр без книг уходит, как при правке
	var genres int
	s.db.QueryRow(`SELECT COUNT(*) FROM genre WHERE name = 'Мемуары'`).Scan(&genres)
	if genres != 0 {
		t.Error("жанр-сирота остался в подсказках")
	}
	if _, err := s.Edition(ed); !errors.Is(err, ErrNotFound) {
		t.Errorf("издание осталось: %v", err)
	}
	for _, c := range []struct {
		table, col string
		id         int64
	}{{"book_copy", "edition_id", ed}, {"edition_person", "edition_id", ed}, {"edition_isbn", "edition_id", ed},
		{"edition_genre", "edition_id", ed}, {"slot_edition", "edition_id", ed},
		{"valuation", "copy_id", cp}, {"loan", "copy_id", cp}} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+c.table+` WHERE `+c.col+` = ?`, c.id).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: осталось %d (%v)", c.table, n, err)
		}
	}

	// чужое на месте: другая книга серии, сама серия, место собрания, справка о человеке
	if e, _ := s.Edition(other); e.Series != "Воспоминания. Мемуары" {
		t.Errorf("у другой книги пропала серия: %+v", e.Series)
	}
	var places int
	s.db.QueryRow(`SELECT COUNT(*) FROM collection_slot WHERE collection_id = ?`, set).Scan(&places)
	if places != 2 {
		t.Errorf("мест в собрании: %d, ждали 2 (т. 2 — пустой)", places)
	}
	if p, _, _ := s.Person("Деникин Антон Иванович"); p.Life != "1872–1947" {
		t.Errorf("справка о человеке пропала: %+v", p)
	}

	// последняя книга серии — серия уходит вместе с ней, как при снятии серии в правке
	if _, err := s.DeleteEdition(lonely); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM collection WHERE kind = 'series' AND title = 'Серия-опечатка'`).Scan(&n)
	if n != 0 {
		t.Error("опустевшая серия осталась в подсказках")
	}
	if _, err := s.DeleteEdition(ed); !errors.Is(err, ErrNotFound) {
		t.Errorf("повторное удаление: %v", err)
	}
}
