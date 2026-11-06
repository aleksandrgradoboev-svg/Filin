package catalog

import (
	"errors"
	"testing"
)

func lentCopy(t *testing.T, s *Store, title string) (editionID, copyID int64) {
	t.Helper()
	id, err := s.AddEdition(&Edition{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	c := Copy{EditionID: id}
	if _, err := s.AddCopy(&c); err != nil {
		t.Fatal(err)
	}
	return id, c.ID
}

func TestLendAndReturn(t *testing.T) {
	s := newStore(t)
	eid, cid := lentCopy(t, s, "Мастер и Маргарита")

	l := Loan{CopyID: cid, Borrower: "  Петя ", LentOn: "2026-10-01", DueOn: "2026-10-15"}
	if _, err := s.Lend(&l); err != nil {
		t.Fatal(err)
	}
	// второй раз тот же экземпляр не отдать, пока не вернули
	if _, err := s.Lend(&Loan{CopyID: cid, Borrower: "Вася"}); !errors.Is(err, ErrLent) {
		t.Fatalf("ждали ErrLent, получено %v", err)
	}

	cs, err := s.Copies(eid)
	if err != nil {
		t.Fatal(err)
	}
	if cs[0].Loan == nil || cs[0].Loan.Borrower != "Петя" || !cs[0].Loan.Overdue("2026-10-30") {
		t.Fatalf("экземпляр не на руках у Пети или не просрочен: %+v", cs[0].Loan)
	}
	rows, _, err := s.Search("мастер", 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("поиск: %v %v", rows, err)
	}
	if rows[0].Borrower != "Петя" || !rows[0].Overdue {
		t.Errorf("метка в списке: %+v", rows[0])
	}

	if _, err := s.Return(l.ID, "2026-09-01"); err == nil {
		t.Error("вернули раньше, чем отдали, — должно быть отказано")
	}
	if _, err := s.Return(l.ID, "2999-01-01"); !IsInput(err) {
		t.Errorf("возврат будущей датой: %v", err)
	}
	// экземпляр на руках не удаляется: долг пропал бы вместе с ним
	if err := s.DeleteCopy(cid); !IsInput(err) {
		t.Errorf("удаление экземпляра на руках: %v", err)
	}
	if _, err := s.Return(l.ID, "2026-10-20"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Return(l.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("повторный возврат: %v", err)
	}
	cs, _ = s.Copies(eid)
	if cs[0].Loan != nil {
		t.Errorf("после возврата экземпляр всё ещё на руках: %+v", cs[0].Loan)
	}
	// после возврата снова можно отдать, а история остаётся
	if _, err := s.Lend(&Loan{CopyID: cid, Borrower: "Вася"}); err != nil {
		t.Fatal(err)
	}
	hist, err := s.CopyLoans(cid)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[1].Borrower != "Петя" || hist[1].ReturnedOn != "2026-10-20" {
		t.Errorf("история: %+v", hist)
	}
}

func TestOnLoanOrderAndBorrowers(t *testing.T) {
	s := newStore(t)
	_, a := lentCopy(t, s, "Без срока")
	_, b := lentCopy(t, s, "Просрочена")
	_, c := lentCopy(t, s, "Давно и вернули")
	for _, l := range []Loan{
		{CopyID: a, Borrower: "Петя", LentOn: "2026-01-01"},
		{CopyID: b, Borrower: "Вася", LentOn: "2026-05-01", DueOn: "2026-06-01"},
		{CopyID: c, Borrower: "Петя", LentOn: "2025-01-01"},
	} {
		if _, err := s.Lend(&l); err != nil {
			t.Fatal(err)
		}
		if l.CopyID == c {
			if _, err := s.Return(l.ID, "2025-02-01"); err != nil {
				t.Fatal(err)
			}
		}
	}
	on, err := s.OnLoan()
	if err != nil {
		t.Fatal(err)
	}
	if len(on) != 2 || on[0].Title != "Просрочена" {
		t.Fatalf("на руках: просроченная должна быть первой и вернувшаяся не видна: %+v", on)
	}
	his, _ := s.LoansOf("Петя")
	if len(his) != 2 || his[0].ReturnedOn != "" {
		t.Errorf("у Пети: сначала то, что на руках: %+v", his)
	}
	if bs, _ := s.Borrowers(); len(bs) != 2 || bs[0] != "Вася" {
		t.Errorf("подсказки имён: %v", bs)
	}
	if _, err := s.Lend(&Loan{CopyID: a, Borrower: " "}); err == nil {
		t.Error("выдача без имени прошла")
	}
	if _, err := s.Lend(&Loan{CopyID: c, Borrower: "Коля", LentOn: "2026-10-10", DueOn: "2026-10-01"}); err == nil {
		t.Error("срок раньше выдачи прошёл")
	}
}
