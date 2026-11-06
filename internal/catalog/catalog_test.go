package catalog

import (
	"path/filepath"
	"testing"

	"github.com/aleksandrgradoboev-svg/filin/internal/db"
	"github.com/aleksandrgradoboev-svg/filin/internal/isbn"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "filin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

// isbns — номера для теста; всё должно разобраться.
func isbns(t *testing.T, raw ...string) []isbn.ISBN {
	t.Helper()
	good, bad := ParseISBNs(raw...)
	if len(bad) > 0 {
		t.Fatalf("не разобрались: %q", bad)
	}
	return good
}

// Издательства повторно используют ISBN: поиск обязан вернуть оба издания, а не первое.
// Случай из разведки — Штильмарк, «Наследник из Калькутты», 1990 и 1991 под одним ISBN.
func TestEditionsByISBNReturnsAllReuses(t *testing.T) {
	s := newStore(t)
	for _, y := range []int{1991, 1990} {
		if _, err := s.AddEdition(&Edition{Title: "Наследник из Калькутты", Publisher: "Правда", Year: y,
			People: []Person{{Name: "Штильмарк Р."}},
			ISBNs:  isbns(t, "978-0-306-40615-7")}); err != nil {
			t.Fatal(err)
		}
	}
	// ищем тем же номером в 10 цифрах — должен найтись после нормализации
	got, err := s.EditionsByISBN("0-306-40615-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Year != 1990 {
		t.Fatalf("ждали два издания, 1990 первым; получено %+v", got)
	}
}

func TestBadChecksumIsKeptAndMarked(t *testing.T) {
	s := newStore(t)
	id, err := s.AddEdition(&Edition{Title: "Книга с опечаткой в ISBN",
		ISBNs: isbns(t, "978-5-87399-050-6", "отсутствует")})
	if err != nil {
		t.Fatal(err)
	}
	var n, valid int
	if err := s.db.QueryRow(`SELECT COUNT(*), MAX(valid) FROM edition_isbn WHERE edition_id = ?`, id).
		Scan(&n, &valid); err != nil {
		t.Fatal(err)
	}
	if n != 1 || valid != 0 {
		t.Fatalf("ждали один номер с valid=0, получено n=%d valid=%d", n, valid)
	}
}

func TestEditionWithoutISBNIsNormal(t *testing.T) {
	s := newStore(t)
	if _, err := s.AddEdition(&Edition{Title: "История СССР с древнейших времён до конца XVIII в.",
		Publisher: "Высшая школа", Year: 1983,
		People: []Person{{Name: "Рыбаков Б. А.", Role: "editor"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdition(&Edition{}); err == nil {
		t.Fatal("издание без названия должно отклоняться")
	}
}

func TestLiveLibIDIsUnique(t *testing.T) {
	s := newStore(t)
	if _, err := s.AddEdition(&Edition{Title: "А", LiveLibID: "123"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdition(&Edition{Title: "Б", LiveLibID: "123"}); err == nil {
		t.Fatal("повторный импорт той же книги LiveLib должен отклоняться")
	}
	// пустой LiveLib-id — не из LiveLib, таких может быть сколько угодно
	for range 2 {
		if _, err := s.AddEdition(&Edition{Title: "В"}); err != nil {
			t.Fatal(err)
		}
	}
}

// Комплектность считается по книгам: том 2 из двух книг, сдвоенный том 3-4 закрывает два места.
func TestCompleteness(t *testing.T) {
	s := newStore(t)
	shelf, err := s.Shelf("Кабинет", "Шкаф 1", "3")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Shelf("Кабинет", "Шкаф 1", "3"); again != shelf {
		t.Fatalf("та же полка завелась второй раз: %d и %d", shelf, again)
	}

	col, err := s.AddCollection(&Collection{Kind: "set", Title: "Собрание в 4 т.", VolumesAnnounced: 4})
	if err != nil {
		t.Fatal(err)
	}
	type place struct{ vol, book int }
	slots := map[place]int64{}
	for _, p := range []place{{1, 0}, {2, 1}, {2, 2}, {3, 0}, {4, 0}} {
		id, err := s.AddSlot(col, p.vol, p.book, "")
		if err != nil {
			t.Fatal(err)
		}
		slots[p] = id
	}
	add := func(title string, places ...place) {
		ed, err := s.AddEdition(&Edition{Title: title})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range places {
			if err := s.LinkSlot(slots[p], ed); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.AddCopy(&Copy{EditionID: ed, ShelfID: shelf}); err != nil {
			t.Fatal(err)
		}
	}
	add("Том 1", place{1, 0})
	add("Том 2, кн. 1", place{2, 1})
	add("Том 3-4", place{3, 0}, place{4, 0})

	// издание тома 2 кн. 2 известно, но экземпляра нет — место не закрыто
	ed, _ := s.AddEdition(&Edition{Title: "Том 2, кн. 2"})
	s.LinkSlot(slots[place{2, 2}], ed)

	c, err := s.Completeness(col)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Slots) != 5 || c.Have != 4 || len(c.Missing) != 1 ||
		c.Missing[0].Volume != 2 || c.Missing[0].Book != 2 {
		t.Fatalf("ждали 4 из 5, нет т. 2 кн. 2; получено %+v", c)
	}

	ser, _ := s.AddCollection(&Collection{Kind: "series", Title: "Библиотека приключений"})
	if _, err := s.Completeness(ser); err == nil {
		t.Fatal("у серии полнота не считается")
	}
}

func TestUnnumberedSlotIsUnique(t *testing.T) {
	s := newStore(t)
	col, _ := s.AddCollection(&Collection{Kind: "set", Title: "Собрание"})
	if _, err := s.AddSlot(col, 0, 0, "Дополнительный том"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSlot(col, 0, 0, "Дополнительный том"); err == nil {
		t.Fatal("то же место без номера тома завелось второй раз")
	}
	if _, err := s.AddSlot(col, 0, 0, "Письма"); err != nil {
		t.Fatalf("другое место без номера должно заводиться: %v", err)
	}
	// члены серии без номера и подписи — обычное дело
	ser, _ := s.AddCollection(&Collection{Kind: "series", Title: "Библиотека приключений"})
	for range 2 {
		if _, err := s.AddSlot(ser, 0, 0, ""); err != nil {
			t.Fatalf("второй член серии без подписи: %v", err)
		}
	}
}

func TestUpdatedAtIsTouched(t *testing.T) {
	s := newStore(t)
	ed, _ := s.AddEdition(&Edition{Title: "Х"})
	// сдвигаем отметку в прошлое — правка должна вернуть её к текущему времени
	if _, err := s.db.Exec(`UPDATE edition SET updated_at = '2000-01-01T00:00:00Z' WHERE id = ?`, ed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE edition SET notes = 'правка' WHERE id = ?`, ed); err != nil {
		t.Fatal(err)
	}
	var at string
	if err := s.db.QueryRow(`SELECT updated_at FROM edition WHERE id = ?`, ed).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if at == "2000-01-01T00:00:00Z" {
		t.Fatal("updated_at не обновился при правке")
	}
}

func TestEditionWithCopiesCannotBeDeleted(t *testing.T) {
	s := newStore(t)
	ed, _ := s.AddEdition(&Edition{Title: "Х"})
	if _, err := s.AddCopy(&Copy{EditionID: ed}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM edition WHERE id = ?`, ed); err == nil {
		t.Fatal("удаление издания с экземплярами должно отклоняться — иначе книги с полки потеряют описание")
	}
}
