package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aleksandrgradoboev-svg/filin/internal/ocr"
)

// Страницы каталога не кэшируются: иначе телефон по «назад» показывает старый список.
func TestPagesAreNotCached(t *testing.T) {
	a := newNetApp(t)
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/", nil)
	r.RemoteAddr = local
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("главная: %d, Cache-Control %q", w.Code, w.Header().Get("Cache-Control"))
	}
}

// fakeOCR — распознавание без моделей: готово ли и что «прочитано».
type fakeOCR struct {
	ready bool
	msg   string
	tsv   []byte
}

func (f fakeOCR) Status() (bool, string) { return f.ready, f.msg }
func (f fakeOCR) Recognize(context.Context, []byte) ([]byte, error) {
	return f.tsv, nil
}

// Без распознавания фото титула не принимается — с понятным текстом, а не падением; пока
// модели скачиваются — человеку говорят именно это.
func TestTitleWithoutEngine(t *testing.T) {
	a := newNetApp(t)
	code, b := a.do(local, "POST", "/api/title", "", "image/jpeg", []byte("jpeg"))
	if code != http.StatusServiceUnavailable || !strings.Contains(string(b), "не подключено") {
		t.Errorf("без распознавания: %d %s", code, b)
	}
	a = newNetApp(t, OCR(fakeOCR{msg: "скачиваю модели распознавания: 3 из 27 МБ"}))
	code, b = a.do(local, "POST", "/api/title", "", "image/jpeg", []byte("jpeg"))
	if code != http.StatusServiceUnavailable || !strings.Contains(string(b), "3 из 27 МБ") {
		t.Errorf("модели ещё качаются: %d %s", code, b)
	}
}

// Фото титула → распознавание → черновик полей и проба на диске.
func TestTitleRecognized(t *testing.T) {
	tsv, err := os.ReadFile(filepath.Join("..", "ocr", "testdata", "overlord-title.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	a := newNetApp(t, OCR(fakeOCR{ready: true, tsv: tsv}))
	code, b := a.do(local, "POST", "/api/title", "", "image/jpeg", []byte("jpeg"))
	if code != http.StatusOK {
		t.Fatalf("титул: %d %s", code, b)
	}
	var d struct {
		Sample string `json:"sample"`
		Title  string `json:"title"`
	}
	// в этой пробе название прочитано с ошибкой («‹«овберлюрд»») — проверяется путь, а не буквы
	if err := json.Unmarshal(b, &d); err != nil || d.Sample == "" || !strings.HasPrefix(d.Title, "Операция") {
		t.Errorf("черновик: %s %v", b, err)
	}
}

// Книга без ISBN, заведённая из черновика титула: город сохраняется, к пробе дописан итог.
func TestBookFromTitleProbe(t *testing.T) {
	a := newNetApp(t)
	probes := ocr.Samples(filepath.Join(a.data, "ocr"))
	id, err := probes.Save([]byte("фото"), []byte("tsv"), ocr.Draft{Title: "Двенадцать стульев"})
	if err != nil {
		t.Fatal(err)
	}
	code, b := a.do(local, "POST", "/api/books", "", "application/json", []byte(`{"title":"Двенадцать стульев",
		"authors":["И. Ильф, Е. Петров"],"publisher":"Художественная литература","city":"Москва","year":1987,"ocr":"`+id+`"}`))
	if code != http.StatusCreated {
		t.Fatalf("книга из титула: %d %s", code, b)
	}
	e, err := a.st.Edition(1)
	if err != nil || e.City != "Москва" || len(e.ISBNs) != 0 || len(e.People) != 2 {
		t.Fatalf("карточка: %+v %v", e, err)
	}
	data, err := os.ReadFile(filepath.Join(a.data, "ocr", id, "final.json"))
	var final struct {
		Book    int64    `json:"book"`
		Authors []string `json:"authors"`
	}
	if err != nil || json.Unmarshal(data, &final) != nil || final.Book != 1 || len(final.Authors) != 2 {
		t.Errorf("итог пробы: %s %v", data, err)
	}
	// каталожная карточка: участники, шифры, аннотация, ISBN — из пробы, без формы
	id, err = probes.Save([]byte("фото"), []byte("tsv"), ocr.Draft{Kind: ocr.KindCard,
		People: []ocr.Person{{Name: "Ржешевский О. А.", Role: "editor"}}, ISBN: []string{"5-01-001234-0", "мусор"},
		BBK: "63.3(0)62", AuthorSign: "Х 24", OrigLanguage: "английский", Annotation: "О высадке.", Pages: 352})
	if err != nil {
		t.Fatal(err)
	}
	code, b = a.do(local, "POST", "/api/books", "", "application/json", []byte(`{"title":"Операция «Оверлорд»",
		"authors":["Хастингс М."],"isbn":"5010012340","ocr":"`+id+`"}`))
	if code != http.StatusCreated {
		t.Fatalf("книга из карточки: %d %s", code, b)
	}
	e, err = a.st.Edition(2)
	if err != nil || len(e.People) != 2 || e.People[1].Role != "editor" || e.BBK != "63.3(0)62" || e.AuthorSign != "Х 24" ||
		e.OrigLanguage != "английский" || e.Annotation != "О высадке." || e.Pages != 352 || len(e.ISBNs) != 1 {
		t.Fatalf("карточка из пробы: %+v %v", e, err)
	}
	// чужой номер пробы не ломает сохранение книги
	if code, b := a.do(local, "POST", "/api/books", "", "application/json", []byte(`{"title":"x","ocr":"../../etc"}`)); code != http.StatusCreated {
		t.Errorf("книга с чужим номером пробы: %d %s", code, b)
	}
}
