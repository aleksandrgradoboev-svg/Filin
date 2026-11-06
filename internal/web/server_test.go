package web

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/db"
)

func TestPeopleByRoles(t *testing.T) {
	r := httptestRequest(url.Values{
		"translators":  {"Гнедич Н."},
		"authors":      {" Ильф И. ,Петров  Е.; "},
		"illustrators": {"Кукрыниксы"},
	})
	ps := readPeople(r)
	want := []catalog.Person{
		{Name: "Ильф И.", Role: "author"}, {Name: "Петров Е.", Role: "author"}, // авторы первыми, как на титуле
		{Name: "Гнедич Н.", Role: "translator"},
		{Name: "Кукрыниксы", Role: "illustrator"},
	}
	if fmt.Sprint(ps) != fmt.Sprint(want) {
		t.Fatalf("получено %+v", ps)
	}
	by := peopleByRole(ps)
	if by["authors"] != "Ильф И., Петров Е." || by["illustrators"] != "Кукрыниксы" || by["editors"] != "" {
		t.Errorf("поля формы: %q", by)
	}
	if g := peopleGroups(ps); len(g) != 3 || g[0].Label != "Авторы" || g[2].Label != "Художник" {
		t.Errorf("строки карточки: %+v", g)
	}
}

func httptestRequest(form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

// Карточка досконально: всё, что ввели в форму, возвращается в форму правки и видно
// в карточке. Новое поле, забытое где-то по пути, ловится здесь.
func TestFullCardThroughForm(t *testing.T) {
	srv, _ := newApp(t)
	form := url.Values{
		"title": {"Белый Клык"}, "authors": {"Лондон Джек"}, "translators": {"Волжина Наталия Альбертовна"},
		"editors": {"Карпова Е."}, "illustrators": {"Любаев Петр"}, "compilers": {"Составитель С."},
		"publisher": {"Нигма"}, "city": {"М."}, "year": {"2025"}, "isbns": {"978-0-306-40615-7"},
		"volume": {"т. 1"}, "series": {"Страна приключений"}, "series_no": {"5"},
		"genres":     {"Зарубежная приключенческая литература, Классическая зарубежная проза"},
		"cover_type": {"7Б - твердая"}, "format": {"84x108/16"}, "pages": {"224"},
		"circulation": {"3000"}, "print_price": {"без цены"}, "age_rating": {"12+"},
		"paper": {"мелованная"}, "illustrations": {"цветные"}, "design": {"ляссе, тиснение объемное"},
		"dimensions": {"267x211x17"}, "weight": {"820"}, "printer": {"Тверской полиграфкомбинат"},
		"edition_note": {"Подарочное издание"}, "orig_title": {"White Fang"}, "orig_language": {"английского"},
		"language": {"русский"}, "annotation": {"Волк-собака на Аляске."}, "contents": {"Белый Клык\nПримечания"},
		"bbk": {"84(7Сое)"}, "udk": {"821.111(73)"}, "author_sign": {"Л76"}, "notes": {"подарок"},
	}
	r, err := noRedirect().PostForm(srv.URL+"/add", form)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	loc := r.Header.Get("Location")
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("добавление: %d", r.StatusCode)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(loc, "/book/"), "?added")
	// сравниваем текст, а не разметку: html/template пишет «+» как &#43;
	edit := html.UnescapeString(body(t, srv, "/book/"+id+"/edit"))
	card := html.UnescapeString(body(t, srv, "/book/"+id))
	for name, v := range form {
		want := v[0]
		if name == "isbns" {
			want = "9780306406157" // хранится нормализованным
		}
		if name == "genres" {
			want = "Классическая зарубежная проза"
		}
		// переводы строк в textarea сохраняются как есть
		if !strings.Contains(edit, want) {
			t.Errorf("поле %q: %q не вернулось в форму правки", name, want)
		}
		if !strings.Contains(card, want) {
			t.Errorf("поле %q: %q не видно в карточке", name, want)
		}
	}
}

func newApp(t *testing.T) (*httptest.Server, *catalog.Store) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "filin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st := catalog.New(d)
	h, err := New(st, filepath.Join(t.TempDir(), "covers"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, st
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func body(t *testing.T, srv *httptest.Server, path string) string {
	t.Helper()
	r, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s", path, r.StatusCode, b)
	}
	return string(b)
}

// Путь, который пройдёт сканер: добавление книги с экземпляром, поиск, карточка,
// и повторный скан того же ISBN показывает, что книга уже есть.
func TestAddFindAndRescan(t *testing.T) {
	srv, st := newApp(t)
	form := url.Values{
		"title": {"Илиада"}, "authors": {"Гомер"}, "translators": {"Гнедич Н."}, "year": {"1978"},
		"isbns": {"978-0-306-40615-7"}, "with_copy": {"on"},
		"room": {"Кабинет"}, "bookcase": {"Шкаф 1"}, "shelf": {"3"},
	}
	r, err := noRedirect().PostForm(srv.URL+"/add", form)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	loc := r.Header.Get("Location")
	if r.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/book/") {
		t.Fatalf("добавление: %d %q", r.StatusCode, loc)
	}

	if b := body(t, srv, "/?q=гнедич"); !strings.Contains(b, "Илиада") {
		t.Error("поиск по переводчику не нашёл книгу")
	}
	if b := body(t, srv, loc); !strings.Contains(b, "Кабинет → Шкаф 1 → 3") || !strings.Contains(b, "Переводчик") || !strings.Contains(b, "Гнедич Н.") {
		t.Error("карточка без полки или без переводчика")
	}
	if b := body(t, srv, "/add?isbn=0306406152"); !strings.Contains(b, "С этим ISBN уже есть") {
		t.Error("повторный скан не показал, что книга уже есть")
	}
	if st, _ := st.Stats(); st.Editions != 1 || st.Copies != 1 {
		t.Errorf("в базе: %+v", st)
	}
}

// Кривой ISBN не должен пропасть молча: форма возвращается с ошибкой и введённым текстом.
func TestBadISBNReturnsForm(t *testing.T) {
	srv, st := newApp(t)
	r, err := noRedirect().PostForm(srv.URL+"/add", url.Values{"title": {"Книга"}, "isbns": {"978-5-12"}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	page := string(b)
	if r.StatusCode != http.StatusOK || !strings.Contains(page, "не разобраны ISBN") {
		t.Fatalf("ждали форму с ошибкой, получено %d", r.StatusCode)
	}
	if st, _ := st.Stats(); st.Editions != 0 {
		t.Error("издание сохранено, хотя ISBN не разобран")
	}
}

func TestForeignOriginIsRefused(t *testing.T) {
	srv, _ := newApp(t)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/add", strings.NewReader("title=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	r, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("запись с чужой страницы прошла: %d", r.StatusCode)
	}
}

// Обложка: загрузка с формы, отдача, снятие — файл с диска тоже убирается.
func TestCoverUploadAndRemove(t *testing.T) {
	srv, st := newApp(t)
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 200, 300))); err != nil {
		t.Fatal(err)
	}
	post := func(path string, fields map[string]string, file []byte) *http.Response {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		for k, v := range fields {
			mw.WriteField(k, v)
		}
		if file != nil {
			fw, _ := mw.CreateFormFile("cover", "photo.png")
			fw.Write(file)
		}
		mw.Close()
		r, err := noRedirect().Post(srv.URL+path, mw.FormDataContentType(), &b)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r
	}

	r := post("/add", map[string]string{"title": "С обложкой"}, img.Bytes())
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("добавление: %d", r.StatusCode)
	}
	rows, _, _ := st.Search("", 10, 0)
	if len(rows) != 1 || rows[0].Cover == "" {
		t.Fatalf("обложка не записана: %+v", rows)
	}
	name := rows[0].Cover
	if !strings.Contains(body(t, srv, "/"), "/cover/"+name) {
		t.Error("плитка без обложки")
	}
	cr, err := http.Get(srv.URL + "/cover/" + name)
	if err != nil {
		t.Fatal(err)
	}
	cr.Body.Close()
	if cr.StatusCode != http.StatusOK || cr.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("отдача обложки: %d %s", cr.StatusCode, cr.Header.Get("Content-Type"))
	}

	if r := post(fmt.Sprintf("/book/%d/edit", rows[0].ID), map[string]string{"title": "С обложкой", "remove_cover": "on"}, nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("правка: %d", r.StatusCode)
	}
	if cr, _ := http.Get(srv.URL + "/cover/" + name); cr.StatusCode != http.StatusNotFound {
		t.Errorf("файл снятой обложки остался: %d", cr.StatusCode)
	}

	if r := post("/add", map[string]string{"title": "Не картинка"}, []byte("%PDF-1.4")); r.StatusCode != http.StatusOK {
		t.Errorf("не картинка должна вернуть форму с ошибкой, получено %d", r.StatusCode)
	}
	// имя обложки — только «id-время.jpg»: выйти из каталога обложек к базе нельзя
	cr, err = http.Get(srv.URL + "/cover/..%2Ffilin.db")
	if err != nil {
		t.Fatal(err)
	}
	cr.Body.Close()
	if cr.StatusCode != http.StatusNotFound {
		t.Errorf("обход каталога обложек: %d", cr.StatusCode)
	}
}

// Выдача: отдать, увидеть на плитке и в «На руках», вернуть; ошибка ввода — в карточке.
func TestLendFlow(t *testing.T) {
	srv, st := newApp(t)
	id, err := st.AddEdition(&catalog.Edition{Title: "Мастер и Маргарита"})
	if err != nil {
		t.Fatal(err)
	}
	c := catalog.Copy{EditionID: id}
	if _, err := st.AddCopy(&c); err != nil {
		t.Fatal(err)
	}
	lend := func(form url.Values) *http.Response {
		r, err := noRedirect().PostForm(fmt.Sprintf("%s/copy/%d/lend", srv.URL, c.ID), form)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r
	}
	if r := lend(url.Values{"borrower": {""}}); r.StatusCode != http.StatusOK {
		t.Errorf("выдача без имени: ждали карточку с ошибкой, получено %d", r.StatusCode)
	}
	if r := lend(url.Values{"borrower": {"Петя"}, "lent_on": {"2026-01-10"}, "due_on": {"2026-02-01"}}); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("выдача: %d", r.StatusCode)
	}
	if b := body(t, srv, "/"); !strings.Contains(b, "📖 Петя, срок прошёл") {
		t.Error("на плитке нет метки просроченной выдачи")
	}
	if b := body(t, srv, "/loans"); !strings.Contains(b, "Мастер и Маргарита") || !strings.Contains(b, "10.01.2026") {
		t.Error("книги нет в «На руках»")
	}
	if b := body(t, srv, "/loans/who?name="+url.QueryEscape("Петя")); !strings.Contains(b, "Мастер и Маргарита") {
		t.Error("книги нет на странице человека")
	}

	cs, _ := st.Copies(id)
	r, err := noRedirect().PostForm(fmt.Sprintf("%s/loan/%d/return", srv.URL, cs[0].Loan.ID),
		url.Values{"back": {"/loans"}})
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/loans" {
		t.Fatalf("возврат: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if b := body(t, srv, "/book/"+fmt.Sprint(id)); !strings.Contains(b, "Кто брал раньше (1)") {
		t.Error("в карточке нет истории выдач")
	}
}

func TestParseRub(t *testing.T) {
	for in, want := range map[string]int{
		"1500": 1500, "1 500 ₽": 1500, "1.500 руб.": 1500, "12,500": 12500, "1500,50": 1500,
		"1500.5": 1500, "950": 950, " 2 400 руб ": 2400, "1.234.567": 1234567,
	} {
		if got, err := parseRub(in); err != nil || got != want {
			t.Errorf("parseRub(%q) = %d, %v; ждали %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "дорого", "1.50.0", "-100", "15 тыс"} {
		if _, err := parseRub(bad); err == nil {
			t.Errorf("parseRub(%q) должна отказать", bad)
		}
	}
}

func TestRub(t *testing.T) {
	nb, nn := string(rune(0x202F)), string(rune(0x00A0))
	for n, want := range map[int]string{0: "0" + nn + "₽", 950: "950" + nn + "₽", 1500: "1" + nb + "500" + nn + "₽",
		1234567: "1" + nb + "234" + nb + "567" + nn + "₽"} {
		if got := rub(n); got != want {
			t.Errorf("rub(%d) = %q, ждали %q", n, got, want)
		}
	}
}

// Оценка: «1 500 руб.» понимается как 1500, кривая сумма возвращает карточку с ошибкой.
func TestValueFlow(t *testing.T) {
	srv, st := newApp(t)
	id, _ := st.AddEdition(&catalog.Edition{Title: "Три мушкетёра"})
	c := catalog.Copy{EditionID: id}
	if _, err := st.AddCopy(&c); err != nil {
		t.Fatal(err)
	}
	post := func(form url.Values) int {
		r, err := noRedirect().PostForm(fmt.Sprintf("%s/copy/%d/value", srv.URL, c.ID), form)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if code := post(url.Values{"amount": {"дорого"}}); code != http.StatusOK {
		t.Errorf("кривая сумма: ждали карточку с ошибкой, получено %d", code)
	}
	if code := post(url.Values{"amount": {"1 500 руб."}, "basis": {"alib"}}); code != http.StatusSeeOther {
		t.Fatalf("оценка: %d", code)
	}
	if b := body(t, srv, "/value"); !strings.Contains(b, rub(1500)) || !strings.Contains(b, "Три мушкетёра") {
		t.Error("на странице «Ценность» нет оценки")
	}
}

// «Прочитана» — у экземпляра: галочка в форме экземпляра → база → метка в карточке и снова
// отмеченная галочка в форме; снятая галочка снимает отметку. Второй экземпляр не задет.
func TestReadFlag(t *testing.T) {
	srv, st := newApp(t)
	id, _ := st.AddEdition(&catalog.Edition{Title: "Остров сокровищ"})
	c, other := catalog.Copy{EditionID: id}, catalog.Copy{EditionID: id}
	st.AddCopy(&c)
	st.AddCopy(&other)
	edit := func(form url.Values) {
		r, err := noRedirect().PostForm(fmt.Sprintf("%s/copy/%d/edit", srv.URL, c.ID), form)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusSeeOther {
			t.Fatalf("правка экземпляра: %d", r.StatusCode)
		}
	}
	read := func(id int64) bool {
		cp, err := st.CopyByID(id)
		if err != nil {
			t.Fatal(err)
		}
		return cp.Read
	}

	edit(url.Values{"is_read": {"on"}})
	if !read(c.ID) || read(other.ID) {
		t.Fatalf("после отметки: %v / второй %v", read(c.ID), read(other.ID))
	}
	// считаем метку, а не слова: галочка «📖 прочитана» есть и в форме нового экземпляра
	if b := body(t, srv, fmt.Sprintf("/book/%d", id)); strings.Count(b, `class="tag read"`) != 1 {
		t.Error("в карточке нет метки «прочитана» (или она у обоих экземпляров)")
	}
	if b := body(t, srv, fmt.Sprintf("/copy/%d/edit", c.ID)); !strings.Contains(b, `name="is_read" checked`) {
		t.Error("форма экземпляра не помнит галочку")
	}
	edit(url.Values{})
	if read(c.ID) {
		t.Error("снятая галочка не сняла отметку")
	}
}

// Серия: из формы — в карточку со ссылкой, страница серии со всеми книгами.
func TestSeriesFlow(t *testing.T) {
	srv, st := newApp(t)
	for _, f := range []url.Values{
		{"title": {"Остров сокровищ"}, "series": {"Библиотека приключений"}, "series_no": {"3"}},
		{"title": {"Всадник без головы"}, "series": {"Библиотека приключений"}},
	} {
		r, err := noRedirect().PostForm(srv.URL+"/add", f)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusSeeOther {
			t.Fatalf("добавление: %d", r.StatusCode)
		}
	}
	rows, _, _ := st.Search("остров", 10, 0)
	e, _ := st.Edition(rows[0].ID)
	if b := body(t, srv, fmt.Sprintf("/book/%d", e.ID)); !strings.Contains(b, fmt.Sprintf(`href="/series/%d"`, e.SeriesID)) ||
		!strings.Contains(b, "№ 3") {
		t.Error("в карточке нет серии со ссылкой и номером")
	}
	if b := body(t, srv, fmt.Sprintf("/series/%d", e.SeriesID)); !strings.Contains(b, "Всадник без головы") {
		t.Error("на странице серии нет второй книги")
	}
	r, _ := noRedirect().PostForm(srv.URL+"/add", url.Values{"title": {"x"}, "series_no": {"5"}})
	r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Errorf("номер без серии должен вернуть форму с ошибкой, получено %d", r.StatusCode)
	}
}

// Страница автора: из карточки по ссылке, справка сохраняется, книги видны.
func TestPersonFlow(t *testing.T) {
	srv, st := newApp(t)
	if _, err := st.AddEdition(&catalog.Edition{Title: "Белый Клык",
		People: []catalog.Person{{Name: "Лондон Джек", Role: "author"}, {Name: "Волжина Н.", Role: "translator"}}}); err != nil {
		t.Fatal(err)
	}
	r, err := noRedirect().PostForm(srv.URL+"/person", url.Values{"name": {"Лондон Джек"}, "life": {"1876–1916"},
		"about": {"Американский писатель."}})
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusSeeOther {
		t.Fatalf("справка: %d", r.StatusCode)
	}
	b := body(t, srv, personURL("Лондон Джек"))
	if !strings.Contains(b, "1876–1916") || !strings.Contains(b, "Белый Клык") {
		t.Error("на странице автора нет справки или книги")
	}
	if b := html.UnescapeString(body(t, srv, "/book/1")); !strings.Contains(b, `href="`+personURL("Волжина Н.")+`"`) {
		t.Error("из карточки нет ссылки на страницу переводчика")
	}
	if b := body(t, srv, "/people"); !strings.Contains(b, "Лондон Джек") || strings.Contains(b, "Волжина") {
		t.Error("список авторов: переводчик попал или автор пропал")
	}
}

func TestFormInt(t *testing.T) {
	for in, want := range map[string]int{
		"543": 543, "543 с. + 16 л. ил.": 543, "100 000 экз.": 100000, "100" + string(rune(0x00A0)) + "000": 100000,
		"": 0, "XII, 224 с.": 224,
	} {
		var errs []string
		if got := formInt(httptestRequest(url.Values{"pages": {in}}), "pages", "страниц", &errs); got != want || len(errs) != 0 {
			t.Errorf("formInt(%q) = %d %v, ждали %d", in, got, errs, want)
		}
	}
	var errs []string
	if formInt(httptestRequest(url.Values{"pages": {"много"}}), "pages", "страниц", &errs); len(errs) != 1 {
		t.Error("не число без ошибки")
	}
}

// Резервная копия: с компьютера — архив с базой и обложками, с телефона — нельзя даже
// привязанному.
func TestBackupDownload(t *testing.T) {
	a := newNetApp(t)
	if _, err := a.st.AddEdition(&catalog.Edition{Title: "Илиада"}); err != nil {
		t.Fatal(err)
	}
	code, b := a.do(local, "GET", "/backup", "", "", nil)
	if code != http.StatusOK || !bytes.HasPrefix(b, []byte("PK")) {
		t.Fatalf("копия: %d, %d байт", code, len(b))
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil || len(z.File) == 0 || z.File[0].Name != "filin.db" {
		t.Fatalf("архив: %v", err)
	}
	if code, _ := a.do("192.168.1.50:5000", "GET", "/backup", "", "", nil); code != http.StatusForbidden {
		t.Errorf("копия по сети: %d", code)
	}
	// с чужого сайта (картинкой) — нет: браузер пометил запрос cross-site
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/backup", nil)
	r.RemoteAddr = local
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("копия по чужой ссылке: %d", w.Code)
	}
}

// DNS rebinding: чужой сайт перепривязал своё имя на 127.0.0.1 — запрос «изнутри»
// компьютера, но с чужим именем в Host. Хозяйских прав он получить не должен: ни
// резервной копии, ни кода привязки. По IP и localhost — работает; страницы не встраиваются.
func TestHostGuard(t *testing.T) {
	a := newNetApp(t)
	for host, want := range map[string]int{
		"evil.example:8080": http.StatusMisdirectedRequest,
		"filin.evil.com":    http.StatusMisdirectedRequest,
		"127.0.0.1:8080":    http.StatusOK,
		"localhost:8080":    http.StatusOK,
		"[::1]:8080":        http.StatusOK,
		"192.168.1.10:8080": http.StatusOK,
	} {
		r := httptest.NewRequest("GET", "/backup", nil)
		r.Host, r.RemoteAddr = host, local
		w := httptest.NewRecorder()
		a.h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %s: %d, ждали %d", host, w.Code, want)
		}
		if want == http.StatusOK && (w.Header().Get("X-Frame-Options") != "DENY" ||
			!strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")) {
			t.Errorf("Host %s: нет запрета фреймов", host)
		}
	}
}
