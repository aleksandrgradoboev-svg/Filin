package catalog

import "testing"

func TestSeries(t *testing.T) {
	s := newStore(t)
	add := func(title, series, no string) int64 {
		t.Helper()
		id, err := s.AddEdition(&Edition{Title: title, Series: series, SeriesNo: no})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := add("Остров сокровищ", "Библиотека  приключений", "3")
	b := add("Остров сокровищ (переиздание)", "Библиотека приключений", "3") // переиздание — то же место
	c := add("Всадник без головы", "Библиотека приключений", "")
	d := add("Капитан Сорви-голова", "Библиотека приключений", "12а")

	e, err := s.Edition(a)
	if err != nil {
		t.Fatal(err)
	}
	if e.Series != "Библиотека приключений" || e.SeriesNo != "3" || e.SeriesID == 0 {
		t.Fatalf("серия издания: %q %q %d", e.Series, e.SeriesNo, e.SeriesID)
	}
	title, items, err := s.Series(e.SeriesID)
	if err != nil {
		t.Fatal(err)
	}
	if title != "Библиотека приключений" || len(items) != 4 || items[0].No != "3" || items[1].No != "3" {
		t.Fatalf("состав серии: %q %+v", title, items)
	}
	if names, _ := s.SeriesNames(); len(names) != 1 {
		t.Errorf("пробелы в названии завели вторую серию: %q", names)
	}
	if rows, _, _ := s.Search("приключений всадник", 10, 0); len(rows) != 1 || rows[0].ID != c {
		t.Errorf("поиск по серии: %+v", rows)
	}

	// смена номера и снятие серии
	e.SeriesNo = "4"
	if err := s.UpdateEdition(&e); err != nil {
		t.Fatal(err)
	}
	if e2, _ := s.Edition(a); e2.SeriesNo != "4" {
		t.Errorf("номер не сменился: %q", e2.SeriesNo)
	}
	for _, id := range []int64{a, b, c, d} {
		x, _ := s.Edition(id)
		x.Series = ""
		if err := s.UpdateEdition(&x); err != nil {
			t.Fatal(err)
		}
	}
	if names, _ := s.SeriesNames(); len(names) != 0 {
		t.Errorf("пустая серия осталась в подсказках: %q", names)
	}
}
