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

	"github.com/aleksandrgradoboev-svg/filin/internal/alib"
	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"golang.org/x/text/encoding/charmap"
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

// Аналоги alib → оценка: похожие на издание предложения — первыми и со звёздочкой,
// отмеченные дают подсказку «от – до», оценка ложится на экземпляр этой книги диапазоном.
func TestAlibAnalogs(t *testing.T) {
	srv, _ := fakeAlib(t)
	a := newNetApp(t, AlibBase(srv.URL))
	e := catalog.Edition{Title: "Операция «Оверлорд»", Publisher: "Прогресс", Year: 1989,
		People: []catalog.Person{{Name: "Хастингс Макс", Role: "author"}}}
	id, err := a.st.AddEdition(&e)
	if err != nil {
		t.Fatal(err)
	}
	cid, err := a.st.AddCopy(&catalog.Copy{EditionID: id})
	if err != nil {
		t.Fatal(err)
	}
	book := "/book/" + strconv.FormatInt(id, 10)

	raw, _ := os.ReadFile("../alib/testdata/hastings.html")
	offers := alib.Parse(decode1251(t, raw), "u").Offers
	var picks []int
	for i, o := range offers {
		if o.Rub > 0 && len(picks) < 3 {
			picks = append(picks, i)
		}
	}
	if len(picks) < 2 {
		t.Fatalf("в образце мало предложений с ценой: %d", len(picks))
	}
	lo, hi := offers[picks[0]].Rub, offers[picks[0]].Rub
	for _, i := range picks {
		lo, hi = min(lo, offers[i].Rub), max(hi, offers[i].Rub)
	}

	_, b := a.do(local, "GET", book+"/alib", "", "", nil)
	page := html.UnescapeString(string(b))
	if !strings.Contains(page, "⭐ 1989 г., Прогресс") || !strings.Contains(page, `name="pick"`) {
		t.Error("похожее издание не отмечено или нет галочек")
	}
	if strings.Contains(page, "Оценка по аналогам") {
		t.Error("подсказка без отмеченных предложений")
	}

	q := url.Values{"surname": {"Хастингс"}, "title": {"Операция Оверлорд"}}
	for _, i := range picks {
		q.Add("pick", strconv.Itoa(i))
	}
	q.Add("pick", "9999") // несуществующее — молча мимо
	_, b = a.do(local, "GET", book+"/alib?"+q.Encode(), "", "", nil)
	page = html.UnescapeString(string(b))
	for _, want := range []string{"Оценка по аналогам", `name="amount" inputmode="numeric" required value="` + strconv.Itoa(lo) + `"`,
		`name="copy" value="` + strconv.FormatInt(cid, 10) + `"`, "аналоги на alib.ru: по " + strconv.Itoa(len(picks))} {
		if !strings.Contains(page, want) {
			t.Errorf("в подсказке нет %q", want)
		}
	}
	if hi > lo && !strings.Contains(page, `name="amount_to" inputmode="numeric" value="`+strconv.Itoa(hi)+`"`) {
		t.Errorf("верхняя граница %d не подставлена", hi)
	}

	post := func(form url.Values) (int, []byte) {
		return a.do(local, "POST", book+"/alib/value", "", "application/x-www-form-urlencoded", []byte(form.Encode()))
	}
	form := url.Values{"copy": {strconv.FormatInt(cid, 10)}, "amount": {strconv.Itoa(lo)}, "amount_to": {"1 200"},
		"basis": {"аналоги на alib.ru"}, "source_url": {"https://www.alib.ru/au-hastings/nm-operaciya_overlord/"}}
	if code, b := post(form); code != http.StatusSeeOther {
		t.Fatalf("запись оценки: %d %s", code, b)
	}
	vs, _ := a.st.CopyValuations(cid)
	if len(vs) != 1 || vs[0].Amount != lo || vs[0].AmountTo != 1200 || vs[0].Basis != "аналоги на alib.ru" {
		t.Fatalf("оценка: %+v", vs)
	}
	_, b = a.do(local, "GET", book, "", "", nil)
	if !strings.Contains(string(b), price(lo, 1200)) {
		t.Errorf("в карточке нет диапазона %q", price(lo, 1200))
	}

	// экземпляр чужой книги через эту форму не оценить
	other, _ := a.st.AddEdition(&catalog.Edition{Title: "Чужая"})
	ocid, _ := a.st.AddCopy(&catalog.Copy{EditionID: other})
	form.Set("copy", strconv.FormatInt(ocid, 10))
	if code, _ := post(form); code != http.StatusBadRequest {
		t.Errorf("чужой экземпляр: %d", code)
	}
	// ошибка ввода возвращает ту же форму с сообщением и тем, что человек написал
	form.Set("copy", strconv.FormatInt(cid, 10))
	form.Set("amount_to", "10")
	form.Set("basis", "моя правка основания")
	code, b := post(form)
	page = html.UnescapeString(string(b))
	if code != http.StatusOK || !strings.Contains(page, "верхняя граница оценки меньше нижней") ||
		!strings.Contains(page, `value="моя правка основания"`) || !strings.Contains(page, `name="amount_to" inputmode="numeric" value="10"`) {
		t.Errorf("перевёрнутый диапазон: %d, форма не вернулась с правками", code)
	}
	if vs, _ := a.st.CopyValuations(cid); len(vs) != 1 {
		t.Errorf("ошибочная оценка записалась: %+v", vs)
	}
}

// Издательство совпадает целыми словами, без служебных «изд-во» и кавычек.
func TestSamePublisher(t *testing.T) {
	for _, c := range []struct {
		pub, desc string
		want      bool
	}{
		{"Наука", "М. Наука. 1971г. 632 с.", true},
		{"Изд-во «Наука»", "М. Наука. 1971г.", true},
		{"Детская литература", "М. Детская литература 1985г.", true},
		{"Мир", "Мировая классика. М. Худ. лит. 1985г.", false},
		{"Наука", "Киев. Наукова думка. 1980г.", false},
		{"Детская литература", "М. Детгиз. 1947г. Литература для детей", false},
		{"", "М. Наука. 1971г.", false},
		{"Изд-во", "М. Изд-во 1971г.", false}, // одни служебные слова ничего не различают
	} {
		if got := samePublisher(pubWords(c.pub), c.desc); got != c.want {
			t.Errorf("samePublisher(%q, %q) = %v, want %v", c.pub, c.desc, got, c.want)
		}
	}
}

func TestPrice(t *testing.T) {
	nb := " "
	for _, c := range []struct {
		from, to int
		want     string
	}{
		{1500, 0, "1 500" + nb + "₽"},
		{1500, 1500, "1 500" + nb + "₽"},
		{170, 450, "170" + nb + "–" + nb + "450" + nb + "₽"},
		{900, 1200, "900" + nb + "–" + nb + "1 200" + nb + "₽"},
	} {
		if got := price(c.from, c.to); got != c.want {
			t.Errorf("price(%d, %d) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

func decode1251(t *testing.T, b []byte) string {
	t.Helper()
	s, err := charmap.Windows1251.NewDecoder().Bytes(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(s)
}
