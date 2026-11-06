package catalog

import (
	"reflect"
	"testing"
)

func TestParseGenres(t *testing.T) {
	got := ParseGenres(" Приключения, исторический  роман; приключения\nЁлочная ,, ")
	want := []string{"Приключения", "исторический роман", "Ёлочная"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseGenres = %q, ждали %q", got, want)
	}
}

// Карточка целиком: всё, что записали, читается обратно — иначе поле придётся дозаполнять.
func TestEditionFullCardRoundTrip(t *testing.T) {
	s := newStore(t)
	in := Edition{
		Title: "Айвенго", Publisher: "Правда", City: "М.", Year: 1987, Volume: "т. 1",
		BBK: "84(4Вел)", UDK: "820", AuthorSign: "С32", Notes: "заметка",
		Series: "Библиотека приключений", SeriesNo: "7",
		CoverType: "твёрдая", Format: "84x108/32 (130x200 мм)", Pages: 543,
		Genres: []string{"исторический роман", "приключения"},
		Details: Details{
			OrigTitle: "Ivanhoe", OrigLanguage: "английский", Language: "", EditionNote: "2-е изд., испр.",
			Circulation: 500000, PrintPrice: "2 р. 40 к.", Printer: "Типография «Красный пролетарий»",
			AgeRating: "12+", Annotation: "Роман о рыцарях.", Contents: "Айвенго\nПримечания",
			Paper: "мелованная", Design: "ляссе, тиснение", Illustrations: "цветные", Dimensions: "267x211x17",
			Weight: 820,
		},
		People: []Person{{Name: "Скотт В.", Role: "author"}, {Name: "Бекетова Е.", Role: "translator"},
			{Name: "Любаев П.", Role: "illustrator"}, {Name: "Карпова Е.", Role: "editor"}},
		ISBNs: isbns(t, "978-0-306-40615-7"),
	}
	id, err := s.AddEdition(&in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Edition(id)
	if err != nil {
		t.Fatal(err)
	}
	// поля, которые Edition() дописывает сам, к сравнению не относятся
	got.Source, got.SeriesID = "", 0
	for i := range got.ISBNs {
		got.ISBNs[i].Raw = in.ISBNs[i].Raw
	}
	in.ID = id
	if !reflect.DeepEqual(got, in) {
		t.Errorf("карточка не вернулась целиком:\n было  %+v\n стало %+v", in, got)
	}

	// правка: жанры и подробности переписываются, а не копятся
	got.Genres = []string{"Приключения"} // тот же жанр иначе по регистру
	got.Details.Circulation, got.Pages = 0, 0
	if err := s.UpdateEdition(&got); err != nil {
		t.Fatal(err)
	}
	again, _ := s.Edition(id)
	if !reflect.DeepEqual(again.Genres, []string{"приключения"}) || again.Circulation != 0 || again.Pages != 0 {
		t.Errorf("после правки: жанры %q, тираж %d, страниц %d", again.Genres, again.Circulation, again.Pages)
	}
	gs, _ := s.Genres()
	if len(gs) != 1 || gs[0].Count != 1 {
		t.Errorf("жанр без книг остался или задвоился: %+v", gs)
	}
	rows, _, err := s.SearchIn("", gs[0].ID, 10, 0)
	if err != nil || len(rows) != 1 {
		t.Errorf("отбор по жанру: %+v %v", rows, err)
	}
	if rows, _, _ := s.Search("ivanhoe", 10, 0); len(rows) != 1 {
		t.Error("поиск по названию оригинала")
	}
}
