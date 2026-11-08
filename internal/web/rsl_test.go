package web

import (
	"encoding/json"
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

// fakeRSL — РГБ, отдающая настоящую запись из образца пакета rsl.
func fakeRSL(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	page, err := os.ReadFile("../rsl/testdata/01015228461.html")
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/ru/record/01015228461" {
			http.NotFound(w, r)
			return
		}
		w.Write(page)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// Приложение: предпросмотр записи для формы, потом сохранение — полная карточка из РГБ,
// а правка человека в форме главнее записи.
func TestAppFillsFromRSL(t *testing.T) {
	rslSrv, hits := fakeRSL(t)
	a := newNetApp(t, RSLBase(rslSrv.URL))

	code, b := a.do(local, "GET", "/api/rsl/01015228461", "", "", nil)
	var pre struct {
		Title     string   `json:"title"`
		Authors   []string `json:"authors"`
		Publisher string   `json:"publisher"`
		Year      int      `json:"year"`
	}
	if code != http.StatusOK || json.Unmarshal(b, &pre) != nil || pre.Year != 2026 || pre.Publisher != "Эксмо" ||
		len(pre.Authors) != 1 || pre.Authors[0] != "Дубровин Николай Федорович" {
		t.Fatalf("предпросмотр: %d %s", code, b)
	}
	if code, _ := a.do(local, "GET", "/api/rsl/..%2F..%2Fetc", "", "", nil); code != http.StatusBadRequest {
		t.Errorf("кривой номер записи: %d", code)
	}

	// человек поправил название — оно главнее записи; остальное — из РГБ
	code, b = a.do(local, "POST", "/api/books", "", "application/json", []byte(`{"rsl":"01015228461",
		"title":"Первая оборона Севастополя","authors":["Дубровин Николай Федорович"],"isbn":"9785042431180"}`))
	if code != http.StatusCreated {
		t.Fatalf("сохранение: %d %s", code, b)
	}
	e, err := a.st.Edition(1)
	if err != nil {
		t.Fatal(err)
	}
	if e.Title != "Первая оборона Севастополя" || e.Pages != 475 || e.Series != "Герои русских побед" ||
		e.Circulation != 3000 || e.AgeRating != "16+" || e.Source != "rsl" || len(e.ISBNs) != 1 {
		t.Errorf("карточка из РГБ: %+v", e)
	}
	p, _, _ := a.st.Person("Дубровин Николай Федорович")
	if p.Life != "1837–1904" {
		t.Errorf("годы жизни автора не легли: %q", p.Life)
	}
	// предпросмотр и сохранение одной записи — один запрос в РГБ (кривой номер в РГБ не ходил)
	if *hits != 1 {
		t.Errorf("запросов в РГБ: %d, ждали 1", *hits)
	}
}

// Веб: ссылка на запись → форма заполнена; сохранение помнит источник.
func TestWebFillsFromRSL(t *testing.T) {
	rslSrv, hits := fakeRSL(t)
	a := newNetApp(t, RSLBase(rslSrv.URL))
	_, b := a.do(local, "GET", "/add?rsl=https%3A%2F%2Fsearch.rsl.ru%2Fru%2Frecord%2F01015228461", "", "", nil)
	page := html.UnescapeString(string(b))
	for _, want := range []string{"Первая оборона Севастополя", "Эксмо", "Герои русских побед", "475", "Заполнено из РГБ",
		`name="rsl" value="https://search.rsl.ru/ru/record/01015228461"`} {
		if !strings.Contains(page, want) {
			t.Errorf("в форме нет %q", want)
		}
	}
	_, b = a.do(local, "GET", "/add?rsl=не-ссылка", "", "", nil)
	if !strings.Contains(html.UnescapeString(string(b)), "это не запись РГБ") {
		t.Error("кривая ссылка без ошибки в форме")
	}
	// сохранение формы, заполненной из РГБ, второй раз в РГБ не ходит
	form := url.Values{"title": {"Первая оборона Севастополя"}, "rsl": {"https://search.rsl.ru/ru/record/01015228461"}}
	if code, b := a.do(local, "POST", "/add", "", "application/x-www-form-urlencoded", []byte(form.Encode())); code != http.StatusSeeOther {
		t.Fatalf("сохранение: %d %s", code, b)
	}
	if *hits != 1 {
		t.Errorf("запросов в РГБ: %d, ждали 1", *hits)
	}
}

// «Дополнить из РГБ» у заведённой книги: пустые поля заполняются, заполненные человеком
// остаются, расхождения показаны; после сохранения — ссылка в заметках и годы жизни.
func TestEditFillsFromRSL(t *testing.T) {
	rslSrv, hits := fakeRSL(t)
	a := newNetApp(t, RSLBase(rslSrv.URL))
	id, err := a.st.AddEdition(&catalog.Edition{Title: "Первая оборона Севастополя", Publisher: "Изд. Эксмо-Пресс",
		Notes: "подарок", People: []catalog.Person{{Name: "Дубровин Н. Ф.", Role: "author"}}})
	if err != nil {
		t.Fatal(err)
	}
	edit := "/book/" + strconv.FormatInt(id, 10) + "/edit"

	_, b := a.do(local, "GET", "/book/"+strconv.FormatInt(id, 10), "", "", nil)
	if !strings.Contains(string(b), edit+"#rsl") {
		t.Error("в карточке нет кнопки «Дополнить из РГБ»")
	}

	_, b = a.do(local, "GET", edit+"?rsl=01015228461", "", "", nil)
	page := html.UnescapeString(string(b))
	for _, want := range []string{
		`name="publisher" value="Изд. Эксмо-Пресс"`, // заполненное человеком не тронуто
		`name="year" value="2026"`,                  // пустое — из записи
		`name="pages" value="475"`,
		`name="series" value="Герои русских побед"`,
		`value="Дубровин Н. Ф."`, // автор человека, а не из записи
		"Из РГБ добавлено:", "год", "тираж",
		"В РГБ иначе", "издательство: в РГБ «Эксмо»",
		`name="rsl" value="https://search.rsl.ru/ru/record/01015228461"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("в форме нет %q", want)
		}
	}
	if strings.Contains(page, `value="Дубровин Николай Федорович"`) {
		t.Error("автор из записи подмешан к автору человека")
	}
	if e, _ := a.st.Edition(id); e.Year != 0 {
		t.Error("книга изменилась до сохранения")
	}

	if !strings.Contains(page, "подарок\nДополнено из РГБ: https://search.rsl.ru/ru/record/01015228461") {
		t.Error("ссылка на запись не легла в заметки формы")
	}
	if strings.Contains(page, "название: в РГБ") {
		t.Error("название с подзаголовком в записи показано как расхождение")
	}

	// заметки сохраняются как в форме: человек мог убрать ссылку — она не возвращается
	form := url.Values{"title": {"Первая оборона Севастополя"}, "publisher": {"Изд. Эксмо-Пресс"}, "year": {"2026"},
		"pages": {"475"}, "authors": {"Дубровин Н. Ф."}, "notes": {"подарок"},
		"rsl": {"https://search.rsl.ru/ru/record/01015228461"}}
	if code, b := a.do(local, "POST", edit, "", "application/x-www-form-urlencoded", []byte(form.Encode())); code != http.StatusSeeOther {
		t.Fatalf("сохранение без ссылки: %d %s", code, b)
	}
	if e, _ := a.st.Edition(id); e.Notes != "подарок" {
		t.Errorf("убранная ссылка вернулась: %q", e.Notes)
	}
	form.Set("notes", "подарок\nДополнено из РГБ: https://search.rsl.ru/ru/record/01015228461")
	if code, b := a.do(local, "POST", edit, "", "application/x-www-form-urlencoded", []byte(form.Encode())); code != http.StatusSeeOther {
		t.Fatalf("сохранение: %d %s", code, b)
	}
	e, _ := a.st.Edition(id)
	if e.Year != 2026 || e.Pages != 475 || e.Notes != "подарок\nДополнено из РГБ: https://search.rsl.ru/ru/record/01015228461" {
		t.Errorf("после сохранения: год %d, страниц %d, заметки %q", e.Year, e.Pages, e.Notes)
	}
	// годы жизни — только людям из карточки: автор назван иначе, чем в записи, — справки
	// о человеке без книг не появляется
	if p, _, err := a.st.Person("Дубровин Николай Федорович"); err == nil && p.Life != "" {
		t.Errorf("справка о человеке не из карточки: %+v", p)
	}
	if *hits != 1 {
		t.Errorf("запросов в РГБ: %d, ждали 1", *hits)
	}

	// кривая ссылка — ошибка в форме, ссылка остаётся в поле
	_, b = a.do(local, "GET", edit+"?rsl=не-ссылка", "", "", nil)
	if page := html.UnescapeString(string(b)); !strings.Contains(page, "это не запись РГБ") || !strings.Contains(page, `value="не-ссылка"`) {
		t.Error("кривая ссылка: нет ошибки или ссылка пропала")
	}
}
