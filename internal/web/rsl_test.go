package web

import (
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

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

	// кнопка ведёт на /book/{id}/rsl с готовым запросом: телефон перехватывает её и ищет сам,
	// обычный браузер попадает на форму дополнения
	search := "/book/" + strconv.FormatInt(id, 10) + "/rsl?"
	_, b := a.do(local, "GET", "/book/"+strconv.FormatInt(id, 10), "", "", nil)
	if !strings.Contains(html.UnescapeString(string(b)), search) {
		t.Error("в карточке нет кнопки «Дополнить из РГБ»")
	}
	if code, b := a.do(local, "GET", search+"q=x", "", "", nil); code != http.StatusSeeOther || !strings.Contains(string(b), edit+"#rsl") {
		t.Errorf("«Дополнить» в браузере: %d %s", code, b)
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

// Запрос в РГБ для заведённой книги — по тем же правилам, что у приложения при скане.
func TestRSLQuery(t *testing.T) {
	good, _ := catalog.ParseISBNs("978-5-04-243118-0")
	bad, _ := catalog.ParseISBNs("978-5-04-243118-1") // контрольная цифра не сходится — не ищем по нему
	for _, c := range []struct {
		e     catalog.Edition
		q, fb string
	}{
		{catalog.Edition{Title: "Первая оборона Севастополя", ISBNs: good}, "isbn:9785042431180", ""},
		{catalog.Edition{Title: "Операция «Оверлорд»: Как был открыт второй фронт", ISBNs: bad,
			People: []catalog.Person{{Name: "Федотов Е. М.", Role: "translator"}, {Name: "Хастингс М.", Role: "author"}}},
			"title:(Операция Оверлорд) author:Хастингс", "author:Хастингс"},
		{catalog.Edition{Title: "Шахматы"}, "title:(Шахматы)", ""},
		{catalog.Edition{Title: "", People: []catalog.Person{{Name: "Риджуэй Мэтью", Role: "author"}}}, "author:Риджуэй", ""},
	} {
		q, fb := rslQuery(c.e)
		if q != c.q || fb != c.fb {
			t.Errorf("%q: %q / %q, ждали %q / %q", c.e.Title, q, fb, c.q, c.fb)
		}
	}
}

// «Дополнить из РГБ» в браузере на компьютере — как на телефоне: поиск (здесь — подставной
// невидимый браузер) по запросу из карточки, на странице Филина — список «Это она».
func TestRSLSearchList(t *testing.T) {
	type call struct{ page, fb string }
	calls := make(chan call, 4)
	answer := make(chan func() (RSLResults, error), 4)
	search := func(page, fb string) (RSLResults, error) {
		calls <- call{page, fb}
		return (<-answer)()
	}
	a := newNetApp(t, WithRSLSearcher(search))
	id, _ := a.st.AddEdition(&catalog.Edition{Title: "Водители фрегатов: Книга о великих мореплавателях",
		People: []catalog.Person{{Name: "Николай Чуковский", Role: "author"}}})
	book := "/book/" + strconv.FormatInt(id, 10)
	status := func() map[string]any {
		var s map[string]any
		for i := 0; i < 200; i++ {
			_, b := a.do(local, "GET", book+"/rsl/status", "", "", nil)
			if err := json.Unmarshal(b, &s); err != nil {
				t.Fatalf("статус: %s", b)
			}
			if s["state"] != "waiting" {
				return s
			}
			time.Sleep(10 * time.Millisecond)
		}
		return s
	}

	// с компьютера: страница «Ищу в РГБ», поиск по запросу из карточки (не из адреса) с запасным по автору
	code, b := a.do(local, "GET", book+"/rsl?q=подмена", "", "", nil)
	if code != http.StatusOK || !strings.Contains(html.UnescapeString(string(b)), "Ищу в РГБ") {
		t.Fatalf("страница поиска: %d", code)
	}
	c := <-calls
	want := "https://search.rsl.ru/ru/search#q=" + strings.ReplaceAll(url.QueryEscape("title:(Водители фрегатов) author:Чуковский"), "+", "%20")
	if c.page != want || c.fb != "author:Чуковский" {
		t.Errorf("поиск: %q / %q", c.page, c.fb)
	}
	// второе нажатие, пока ищем, второго поиска не запускает
	a.do(local, "GET", book+"/rsl", "", "", nil)
	select {
	case <-calls:
		t.Error("второй поиск для той же книги")
	default:
	}
	answer <- func() (RSLResults, error) {
		return RSLResults{Query: "author:Чуковский", Info: "Результатов: 2, найдено", Items: []RSLFound{
			{ID: "01001111111", Author: "Чуковский Н. К.", Info: "Водители фрегатов. 1985"},
			{ID: "../../etc", Author: "подлог", Info: "не запись"}, // номер не похож на запись — в список не идёт
			{ID: "01002222222", Author: "Чуковский Н. К.", Info: "Водители фрегатов. 1956"},
		}}, nil
	}
	s := status()
	items, _ := s["items"].([]any)
	if s["state"] != "found" || s["query"] != "author:Чуковский" || len(items) != 2 {
		t.Fatalf("выдача: %v", s)
	}
	first := items[0].(map[string]any)
	if first["url"] != book+"/edit?rsl=01001111111#rsl" || first["info"] != "Водители фрегатов. 1985" {
		t.Errorf("«Это она»: %v", first)
	}
	if s := status(); s["state"] != "none" {
		t.Errorf("итог отдан дважды: %v", s)
	}

	// ошибка поиска — сообщением; незабранный итог не мешает искать заново
	a.do(local, "GET", book+"/rsl", "", "", nil)
	<-calls
	answer <- func() (RSLResults, error) {
		return RSLResults{}, errors.New("РГБ не ответила за 20 секунд")
	}
	time.Sleep(50 * time.Millisecond)
	a.do(local, "GET", book+"/rsl", "", "", nil) // итог ошибки никто не забрал
	select {
	case <-calls:
		answer <- func() (RSLResults, error) { return RSLResults{Info: "Результатов: 0"}, nil }
	case <-time.After(time.Second):
		t.Fatal("после незабранного итога поиск не запустился заново")
	}
	if s := status(); s["state"] != "found" || len(s["items"].([]any)) != 0 {
		t.Errorf("пустая выдача: %v", s)
	}

	// не с компьютера — поиск не запускается, форма со ссылкой
	_, page := a.do(local, "POST", "/devices/code", "", "", nil)
	m := regexp.MustCompile(`class="code">(\d{6})<`).FindSubmatch(page)
	_, pb := a.do(phone, "POST", "/api/pair", "", "application/json", []byte(`{"code":"`+string(m[1])+`","name":"Realme"}`))
	var pair struct{ Token string }
	json.Unmarshal(pb, &pair)
	if code, _ := a.do(phone, "GET", book+"/rsl", pair.Token, "", nil); code != http.StatusSeeOther {
		t.Errorf("с телефона: %d", code)
	}
	select {
	case <-calls:
		t.Error("поиск запущен по запросу с телефона")
	default:
	}
}

// Одна книга, если одно название продолжает другое подзаголовком — в любую сторону.
func TestSameTitle(t *testing.T) {
	for _, c := range []struct {
		card, record string
		want         bool
	}{
		{"Первая оборона Севастополя", "Первая оборона Севастополя, 1854-1855 гг. \"Русская Троя\"", true},
		{"Водители фрегатов: Книга о великих мореплавателях", "Водители фрегатов", true},
		{"Водители фрегатов", "Водители", true},
		{"Водители фрегатов", "Водитель фрегата", false},
		{"Мир", "Мировая история", false}, // слово целиком, не начало слова
		{"", "Что угодно", false},
		{"Что угодно", "", false},
	} {
		if got := sameTitle(c.card, c.record); got != c.want {
			t.Errorf("sameTitle(%q, %q) = %v, want %v", c.card, c.record, got, c.want)
		}
	}
}
