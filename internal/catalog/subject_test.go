package catalog

import (
	"reflect"
	"testing"
)

func TestParseSubjects(t *testing.T) {
	got := ParseSubjects("  История --  СССР--Воспоминания \n\nистория -- ссср -- воспоминания\nВоенное дело\n -- \n")
	want := []string{"История -- СССР -- Воспоминания", "Военное дело"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSubjects: %q, ждали %q", got, want)
	}
	if l := SubjectLevels("A --  B -- -- C"); !reflect.DeepEqual(l, []string{"A", "B", "C"}) {
		t.Errorf("SubjectLevels: %q", l)
	}
}

// Темы хранятся цепочками; отбор — по уровню целиком на любой глубине, поиск — по словам.
func TestSubjectsStoredAndFiltered(t *testing.T) {
	s := newStore(t)
	denikin, err := s.AddEdition(&Edition{Title: "Очерки русской смуты", Subjects: []string{
		"История. Исторические науки -- СССР -- Период гражданской войны (1918-1920) -- Разгром похода Деникина -- Воспоминания",
		"Военная наука. Военное дело -- СССР -- Борьба с контрреволюцией на юге России",
	}})
	if err != nil {
		t.Fatal(err)
	}
	kenez, _ := s.AddEdition(&Edition{Title: "Красная атака, белое сопротивление",
		Subjects: []string{"История. Исторические науки -- СССР -- Период гражданской войны (1918-1920)"}})
	s.AddEdition(&Edition{Title: "Мемуары посторонние", Subjects: []string{"Литература -- Воспоминания о детстве"}})

	e, err := s.Edition(denikin)
	if err != nil || len(e.Subjects) != 2 || e.Subjects[1] != "Военная наука. Военное дело -- СССР -- Борьба с контрреволюцией на юге России" {
		t.Fatalf("темы издания: %q (%v)", e.Subjects, err)
	}

	ids := func(f Filter, q string) []int64 {
		rows, _, err := s.SearchBy(q, f, 50, 0)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	// уровень в середине цепочки, другим регистром — обе книги про гражданскую войну
	if got := ids(Filter{Subject: "период гражданской войны (1918-1920)"}, ""); len(got) != 2 {
		t.Errorf("отбор по уровню: %v", got)
	}
	// «Воспоминания» — только целый уровень: «Воспоминания о детстве» сюда не попадает
	if got := ids(Filter{Subject: "Воспоминания"}, ""); !reflect.DeepEqual(got, []int64{denikin}) {
		t.Errorf("уровень целиком: %v", got)
	}
	// часть уровня — не уровень
	if got := ids(Filter{Subject: "СС"}, ""); len(got) != 0 {
		t.Errorf("кусок уровня нашёл книги: %v", got)
	}
	// поиск по словам видит темы; отбор по теме сужает поиск
	if got := ids(Filter{}, "контрреволюцией"); !reflect.DeepEqual(got, []int64{denikin}) {
		t.Errorf("поиск по словам темы: %v", got)
	}
	if got := ids(Filter{Subject: "СССР"}, "красная"); !reflect.DeepEqual(got, []int64{kenez}) {
		t.Errorf("поиск внутри темы: %v", got)
	}

	// правка без тем — темы сняты; удаление издания уносит их
	e.Subjects = nil
	if err := s.UpdateEdition(&e); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Edition(denikin); len(e.Subjects) != 0 {
		t.Errorf("темы после снятия: %q", e.Subjects)
	}
	if _, err := s.DeleteEdition(kenez); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM edition_subject WHERE edition_id = ?`, kenez).Scan(&n)
	if n != 0 {
		t.Errorf("темы удалённой книги остались: %d", n)
	}
}
