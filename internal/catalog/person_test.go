package catalog

import (
	"errors"
	"testing"
)

func TestPersonPage(t *testing.T) {
	s := newStore(t)
	for _, e := range []Edition{
		{Title: "Белый Клык", Year: 1984, People: []Person{{Name: "Лондон Джек", Role: "author"}}},
		{Title: "Мартин Иден", Year: 1977, People: []Person{{Name: "Лондон Джек", Role: "author"}}},
		{Title: "Сборник", People: []Person{{Name: "Иванов И.", Role: "author"}, {Name: "Лондон Джек", Role: "compiler"}}},
	} {
		if _, err := s.AddEdition(&e); err != nil {
			t.Fatal(err)
		}
	}
	_, books, err := s.Person("  Лондон   Джек ")
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 3 || books[0].Title != "Мартин Иден" || books[2].Role != "compiler" {
		t.Fatalf("книги человека: сначала авторские по году, потом прочие роли: %+v", books)
	}
	if err := s.SavePerson(PersonInfo{Name: "Лондон Джек", Life: "1876–1916", About: "Американский писатель.",
		Link: "https://ru.wikipedia.org/wiki/Лондон,_Джек"}); err != nil {
		t.Fatal(err)
	}
	info, _, _ := s.Person("Лондон Джек")
	if info.Life != "1876–1916" || info.About == "" {
		t.Errorf("справка: %+v", info)
	}
	if err := s.SavePerson(PersonInfo{Name: "Лондон Джек", Link: "javascript:alert(1)"}); !IsInput(err) {
		t.Errorf("ссылка не http: %v", err)
	}
	if _, _, err := s.Person("Никто"); !errors.Is(err, ErrNotFound) {
		t.Errorf("неизвестный человек: %v", err)
	}
	authors, _ := s.People(true)
	if len(authors) != 2 || authors[1].Name != "Лондон Джек" || authors[1].Count != 2 || authors[1].Life != "1876–1916" {
		t.Errorf("список авторов: %+v", authors)
	}
}
