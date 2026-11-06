package web

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// fakeAlib — alib, отдающий сохранённую страницу «Оверлорда» по её адресу и пустую на прочие.
func fakeAlib(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	found, err := os.ReadFile("../alib/testdata/hastings.html")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := os.ReadFile("../alib/testdata/empty.html")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/au-hastings/nm-operaciya_overlord/" {
			w.Write(found)
			return
		}
		w.Write(empty) // alib на несуществующую книгу отвечает 200 и пустой страницей
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func TestAlibAnnotation(t *testing.T) {
	srv, paths := fakeAlib(t)
	a := newNetApp(t, AlibBase(srv.URL))
	e := catalog.Edition{Title: "Операция «Оверлорд»: Как был открыт второй фронт", Publisher: "Прогресс", Year: 1989,
		People: []catalog.Person{{Name: "Хастингс Макс", Role: "author"}, {Name: "Федотов Е. М.", Role: "translator"}},
		Notes:  "куплена в 1990"}
	id, err := a.st.AddEdition(&e)
	if err != nil {
		t.Fatal(err)
	}
	book := "/book/" + strconv.FormatInt(id, 10)

	// в карточке — кнопка
	_, b := a.do(local, "GET", book, "", "", nil)
	if !strings.Contains(string(b), book+"/alib") {
		t.Error("в карточке нет кнопки alib")
	}

	// первый заход ищет по догадке: фамилия автора (не переводчика), название до двоеточия
	code, b := a.do(local, "GET", book+"/alib", "", "", nil)
	page := html.UnescapeString(string(b))
	if code != http.StatusOK || len(*paths) != 1 || (*paths)[0] != "/au-hastings/nm-operaciya_overlord/" {
		t.Fatalf("догадка: %d %v", code, *paths)
	}
	for _, want := range []string{"В книге рассказывается о самой крупной десантной операции", "Прогресс. 1989г.",
		"170 руб.", `name="source" value="https://www.alib.ru/au-hastings/nm-operaciya_overlord/"`, "Взять в аннотацию"} {
		if !strings.Contains(page, want) {
			t.Errorf("на странице нет %q", want)
		}
	}
	if strings.Contains(page, `name="replace"`) {
		t.Error("«заменить» при пустой аннотации")
	}

	// человек поправил запрос, а книги такой нет — честное «нет», не ошибка
	_, b = a.do(local, "GET", book+"/alib?surname=Хастингс&title=Оверлорд", "", "", nil)
	if !strings.Contains(html.UnescapeString(string(b)), "На alib такой страницы нет") {
		t.Error("пустая страница alib не названа пустой")
	}

	// «Взять»: аннотация (с правкой человека) ложится, источник — в заметки
	take := func(form url.Values) (int, []byte) {
		return a.do(local, "POST", book+"/alib", "", "application/x-www-form-urlencoded", []byte(form.Encode()))
	}
	src := "https://www.alib.ru/au-hastings/nm-operaciya_overlord/"
	if code, b := take(url.Values{"annotation": {"О высадке в Нормандии.\r\nВторой абзац."}, "source": {src}}); code != http.StatusSeeOther {
		t.Fatalf("взять: %d %s", code, b)
	}
	got, _ := a.st.Edition(id)
	if got.Annotation != "О высадке в Нормандии.\nВторой абзац." || got.Notes != "куплена в 1990\nАннотация — alib: "+src {
		t.Errorf("после «Взять»: %q / %q", got.Annotation, got.Notes)
	}
	if len(got.People) != 2 || got.Year != 1989 {
		t.Errorf("дополнение задело остальную карточку: %+v", got)
	}

	// своя аннотация без «заменить» не перезаписывается; с «заменить» — да, источник не задваивается
	// отказ возвращает форму с правленым текстом человека, а не страницу ошибки
	_, b = take(url.Values{"annotation": {"Другая, правленая"}, "source": {src}})
	page = html.UnescapeString(string(b))
	if !strings.Contains(page, "отметьте «заменить»") || !strings.Contains(page, ">Другая, правленая</textarea>") ||
		!strings.Contains(page, `name="replace"`) || !strings.Contains(page, "170 руб.") {
		t.Errorf("отказ без «заменить» потерял текст или предложения:\n%s", page)
	}
	if got, _ := a.st.Edition(id); got.Annotation != "О высадке в Нормандии.\nВторой абзац." {
		t.Errorf("перезапись без «заменить»: %q", got.Annotation)
	}
	// повторное открытие — из памяти, alib второй раз не спрашивают
	before := len(*paths)
	a.do(local, "GET", book+"/alib", "", "", nil)
	if len(*paths) != before {
		t.Errorf("повторное открытие сходило в alib: %v", *paths)
	}
	take(url.Values{"annotation": {"Другая"}, "source": {src}, "replace": {"1"}})
	got, _ = a.st.Edition(id)
	if got.Annotation != "Другая" || strings.Count(got.Notes, src) != 1 {
		t.Errorf("замена: %q / %q", got.Annotation, got.Notes)
	}

	// чужой адрес в источнике в заметки не пишется
	take(url.Values{"annotation": {"Третья"}, "source": {"https://evil.example/"}, "replace": {"1"}})
	got, _ = a.st.Edition(id)
	if strings.Contains(got.Notes, "evil") {
		t.Errorf("чужой источник в заметках: %q", got.Notes)
	}
}

// Догадка по карточке: из LiveLib имя «Имя Фамилия», из РГБ — «Фамилия Имя Отчество».
func TestAlibGuess(t *testing.T) {
	for _, c := range []struct {
		e              catalog.Edition
		surname, title string
	}{
		{catalog.Edition{Title: "Справочник любителя астрономии", LiveLibID: "123",
			People: []catalog.Person{{Name: "Петр Куликовский"}}}, "Куликовский", "Справочник любителя астрономии"},
		{catalog.Edition{Title: "Солдат. Пер. с англ", Source: "rsl",
			People: []catalog.Person{{Name: "Риджуэй Мэтью", Role: "author"}}}, "Риджуэй", "Солдат. Пер. с англ"},
		{catalog.Edition{Title: "Шахматы: Наука, опыт, мастерство"}, "", "Шахматы"},
	} {
		s, ti := alibGuess(c.e)
		if s != c.surname || ti != c.title {
			t.Errorf("%q: %q / %q", c.e.Title, s, ti)
		}
	}
}
