package catalog

import (
	"errors"
	"testing"
)

func TestSetLifecycle(t *testing.T) {
	s := newStore(t)
	c := Collection{Title: "  Диккенс. Собрание сочинений  ", Publisher: "ГИХЛ", YearFrom: 1957, YearTo: 1963, VolumesAnnounced: 5}
	id, err := s.CreateSet(&c)
	if err != nil {
		t.Fatal(err)
	}
	set, places, err := s.Set(id)
	if err != nil || set.Title != "Диккенс. Собрание сочинений" || len(places) != 5 {
		t.Fatalf("собрание: %+v, мест %d, %v", set, len(places), err)
	}

	e1, _ := lentCopy(t, s, "Диккенс. Т. 1")
	e67, _ := lentCopy(t, s, "Диккенс. Т. 6-7")
	eNo, _ := s.AddEdition(&Edition{Title: "Диккенс. Т. 2 (издание без экземпляра)"})

	// сдвоенный том — два места; опечатка в номере тома новый том не заводит
	if err := s.PutInSet(e67, id, mustRefs(t, "6-7", 0)); !IsInput(err) {
		t.Fatalf("тома 6-7 в собрании из 5 нет — ждали отказ, получили %v", err)
	}
	if err := s.AddPlace(id, ""); err != nil { // т. 6
		t.Fatal(err)
	}
	if err := s.AddPlace(id, ""); err != nil { // т. 7
		t.Fatal(err)
	}
	if err := s.PutInSet(e67, id, mustRefs(t, "6-7", 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutInSet(e1, id, mustRefs(t, "1", 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutInSet(e1, id, mustRefs(t, "1", 0)); err != nil { // повтор — без дубля
		t.Fatal(err)
	}
	if err := s.PutInSet(eNo, id, mustRefs(t, "2", 0)); err != nil {
		t.Fatal(err)
	}

	// т. 3 делится на две книги; ставить «т. 3» целиком после этого нельзя
	if err := s.SplitVolume(id, 3, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.SplitVolume(id, 3, 2); !IsInput(err) {
		t.Errorf("повторное деление: %v", err)
	}
	if err := s.PutInSet(e1, id, mustRefs(t, "3", 0)); !IsInput(err) {
		t.Errorf("целый т. 3 после деления: %v", err)
	}
	if err := s.AddPlace(id, "Указатель"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPlace(id, "Указатель"); !IsInput(err) {
		t.Errorf("второй «Указатель»: %v", err)
	}

	comp, err := s.Completeness(id)
	if err != nil {
		t.Fatal(err)
	}
	// мест: т.1, т.2, т.3 кн.1, т.3 кн.2, т.4, т.5, т.6, т.7, Указатель = 9; есть т.1, т.6, т.7
	// (у т. 2 издание есть, а экземпляра нет — место не закрыто)
	if len(comp.Slots) != 9 || comp.Have != 3 {
		t.Fatalf("комплектность: мест %d, есть %d", len(comp.Slots), comp.Have)
	}
	rows, err := s.Sets()
	if err != nil || len(rows) != 1 || rows[0].Places != 9 || rows[0].Have != 3 {
		t.Fatalf("список собраний: %+v %v", rows, err)
	}

	_, places, _ = s.Set(id)
	var names []string
	for _, p := range places {
		names = append(names, p.Name())
	}
	want := []string{"т. 1", "т. 2", "т. 3, кн. 1", "т. 3, кн. 2", "т. 4", "т. 5", "т. 6", "т. 7", "Указатель"}
	if len(names) != len(want) {
		t.Fatalf("места: %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("места: %v, ждали %v", names, want)
		}
	}
	if !places[0].Have || places[1].Have || len(places[1].Editions) != 1 {
		t.Errorf("т. 1 есть, у т. 2 издание без экземпляра: %+v / %+v", places[0], places[1])
	}

	ep, err := s.EditionPlaces(e67)
	if err != nil || len(ep) != 2 || ep[0].Place != "т. 6" || ep[1].Place != "т. 7" || ep[0].SetID != id {
		t.Fatalf("где стоит сдвоенный том: %+v %v", ep, err)
	}
	if setID, err := s.Unplace(ep[1].PlaceID, e67); err != nil || setID != id {
		t.Fatal(err)
	}
	if ep, _ := s.EditionPlaces(e67); len(ep) != 1 {
		t.Errorf("после снятия с т. 7: %+v", ep)
	}
	if _, err := s.DeletePlace(places[4].ID); err != nil { // т. 5 — лишний
		t.Fatal(err)
	}

	// собрание удаляется, книги остаются
	if err := s.DeleteSet(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Edition(e67); err != nil {
		t.Errorf("книга пропала вместе с собранием: %v", err)
	}
	if _, _, err := s.Set(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("удалённое собрание: %v", err)
	}
}

func TestSetRefusals(t *testing.T) {
	s := newStore(t)
	for _, c := range []Collection{
		{Title: "", VolumesAnnounced: 3},
		{Title: "Без томов"},
		{Title: "Опечатка", VolumesAnnounced: MaxVolumes + 1},
		{Title: "Годы наоборот", VolumesAnnounced: 2, YearFrom: 1990, YearTo: 1980},
	} {
		if _, err := s.CreateSet(&c); !IsInput(err) {
			t.Errorf("%q: ждали отказ, получили %v", c.Title, err)
		}
	}
	// издательская серия — не собрание: на неё не ставят и её не показывают среди собраний
	ser, _ := s.AddCollection(&Collection{Kind: "series", Title: "Библиотека приключений"})
	e, _ := s.AddEdition(&Edition{Title: "Остров сокровищ"})
	if err := s.PutInSet(e, ser, []PlaceRef{{1, 0}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("серия как собрание: %v", err)
	}
	if rows, _ := s.Sets(); len(rows) != 0 {
		t.Errorf("серия попала в собрания: %+v", rows)
	}
	if _, _, err := s.Set(ser); !errors.Is(err, ErrNotFound) {
		t.Errorf("серия открылась как собрание: %v", err)
	}
	if err := s.DeleteSet(ser); !errors.Is(err, ErrNotFound) {
		t.Errorf("серия удалилась как собрание: %v", err)
	}
}

func TestParseVolumes(t *testing.T) {
	for in, want := range map[string]int{"3": 1, "6-7": 2, "6–7": 2, "2, 5": 2, " 1 ; 3-4 ": 3} {
		refs, err := ParseVolumes(in, 0)
		if err != nil || len(refs) != want {
			t.Errorf("ParseVolumes(%q) = %v, %v", in, refs, err)
		}
	}
	for _, bad := range []string{"", "т. 3", "0", "7-6", "1-100", "-3", "3-"} {
		if _, err := ParseVolumes(bad, 0); err == nil {
			t.Errorf("ParseVolumes(%q) принят", bad)
		}
	}
}

func mustRefs(t *testing.T, s string, book int) []PlaceRef {
	t.Helper()
	r, err := ParseVolumes(s, book)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
