package catalog

import (
	"errors"
	"testing"
)

func seed(t *testing.T, s *Store) {
	t.Helper()
	for _, e := range []Edition{
		{Title: "Мастер и Маргарита", Year: 1989, People: []Person{{Name: "Булгаков М."}}},
		{Title: "Ёлка у Ивановых", Year: 1990, People: []Person{{Name: "Введенский А."}}},
		{Title: "Наследник из Калькутты", Year: 1990, People: []Person{{Name: "Штильмарк Р."}},
			ISBNs: isbns(t, "978-0-306-40615-7")},
		{Title: "Илиада", Year: 1978, People: []Person{{Name: "Гомер"}, {Name: "Гнедич Н.", Role: "translator"}}},
	} {
		if _, err := s.AddEdition(&e); err != nil {
			t.Fatal(err)
		}
	}
}

func titles(rows []Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

// Встроенный LIKE в SQLite не знает регистра кириллицы, а «ё» в каталогах пишут через раз.
func TestSearchFoldsCyrillic(t *testing.T) {
	s := newStore(t)
	seed(t, s)
	cases := map[string]string{
		"мастер":          "Мастер и Маргарита",
		"МАРГАРИТА":       "Мастер и Маргарита",
		"елка":            "Ёлка у Ивановых",
		"булгаков мастер": "Мастер и Маргарита", // слова из автора и названия вместе
		"гнедич":          "Илиада",             // переводчик ищется, хоть и не автор
		"0-306-40615-2":   "Наследник из Калькутты",
	}
	for q, want := range cases {
		rows, total, err := s.Search(q, 10, 0)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if total != 1 || len(rows) != 1 || rows[0].Title != want {
			t.Errorf("%q: ждали %q, получено %v (всего %d)", q, want, titles(rows), total)
		}
	}
	if rows, _, _ := s.Search("булгаков илиада", 10, 0); len(rows) != 0 {
		t.Errorf("каждое слово должно найтись в одной книге; получено %v", titles(rows))
	}
}

func TestSearchPagesAndAuthors(t *testing.T) {
	s := newStore(t)
	seed(t, s)
	rows, total, err := s.Search("", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(rows) != 2 {
		t.Fatalf("ждали страницу из 2 при 4 всего; получено %v, всего %d", titles(rows), total)
	}
	rows, _, _ = s.Search("илиада", 10, 0)
	if rows[0].Authors != "Гомер" {
		t.Errorf("переводчик попал в авторы: %q", rows[0].Authors)
	}
}

func TestUpdateEditionKeepsOrigin(t *testing.T) {
	s := newStore(t)
	id, err := s.AddEdition(&Edition{Title: "Черновик", Source: "livelib", LiveLibID: "123",
		People: []Person{{Name: "Кто-то"}}, ISBNs: isbns(t, "978-0-306-40615-7")})
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Edition(id)
	if err != nil {
		t.Fatal(err)
	}
	e.Title, e.Year = "Чистовик", 1985
	e.People = []Person{{Name: "Первый"}, {Name: "Второй", Role: "editor"}}
	e.ISBNs = nil
	e.Source, e.LiveLibID = "manual", ""
	if err := s.UpdateEdition(&e); err != nil {
		t.Fatal(err)
	}
	got, err := s.Edition(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Чистовик" || got.Year != 1985 || len(got.People) != 2 || got.People[1].Role != "editor" ||
		len(got.ISBNs) != 0 {
		t.Fatalf("правка не легла: %+v", got)
	}
	if got.Source != "livelib" || got.LiveLibID != "123" {
		t.Fatalf("правка руками стёрла происхождение: %q %q", got.Source, got.LiveLibID)
	}
	if err := s.UpdateEdition(&Edition{ID: 9999, Title: "нет такого"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ждали ErrNotFound, получено %v", err)
	}
}

func TestCopiesOnShelves(t *testing.T) {
	s := newStore(t)
	id, _ := s.AddEdition(&Edition{Title: "Книга"})
	sh, err := s.Shelf("Кабинет", "Шкаф 1", "3")
	if err != nil {
		t.Fatal(err)
	}
	a := Copy{EditionID: id, ShelfID: sh}
	b := Copy{EditionID: id}
	for _, c := range []*Copy{&a, &b} {
		if _, err := s.AddCopy(c); err != nil {
			t.Fatal(err)
		}
	}
	cs, err := s.Copies(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Room != "Кабинет" || cs[1].ShelfID != 0 {
		t.Fatalf("экземпляры: %+v", cs)
	}

	b.ShelfID, b.NoLend, b.History = sh, true, "добыли за 20 кг газет"
	if err := s.UpdateCopy(&b); err != nil {
		t.Fatal(err)
	}
	got, err := s.CopyByID(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Shelf != "3" || !got.NoLend || got.History == "" {
		t.Fatalf("правка экземпляра не легла: %+v", got)
	}
	if err := s.DeleteCopy(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CopyByID(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("экземпляр не удалён: %v", err)
	}
	if shs, _ := s.Shelves(); len(shs) != 1 {
		t.Fatalf("полок: %d", len(shs))
	}
}

// last_seen обновляется не чаще раза в минуту, но отозванный токен не пускает сразу.
func TestDeviceSeenThrottled(t *testing.T) {
	s := newStore(t)
	tok, err := s.AddDevice("Realme")
	if err != nil {
		t.Fatal(err)
	}
	d1, err := s.DeviceByToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	// давнюю отметку следующая встреча обновляет, а свежую — в ту же минуту — не трогает
	const longAgo = "2026-01-01T00:00:00Z"
	if _, err := s.db.Exec(`UPDATE device SET last_seen = ? WHERE id = ?`, longAgo, d1.ID); err != nil {
		t.Fatal(err)
	}
	ds, _ := s.Devices()
	if ds[0].LastSeen != longAgo {
		t.Fatalf("подмена отметки не легла: %+v", ds)
	}
	if _, err := s.DeviceByToken(tok); err != nil {
		t.Fatal(err)
	}
	ds, _ = s.Devices()
	if ds[0].LastSeen == longAgo {
		t.Errorf("старая отметка (больше минуты назад) не обновилась")
	}
	seen := ds[0].LastSeen
	s.DeviceByToken(tok)
	if ds, _ = s.Devices(); ds[0].LastSeen != seen {
		t.Errorf("отметка переписана чаще раза в минуту: %q → %q", seen, ds[0].LastSeen)
	}
	if err := s.RevokeDevice(d1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeviceByToken(tok); !errors.Is(err, ErrNotFound) {
		t.Errorf("отозванный токен: %v", err)
	}
}
