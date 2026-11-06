package web

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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
	rslSrv, _ := fakeRSL(t)
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
}

// Веб: ссылка на запись → форма заполнена; сохранение помнит источник.
func TestWebFillsFromRSL(t *testing.T) {
	rslSrv, _ := fakeRSL(t)
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
}
