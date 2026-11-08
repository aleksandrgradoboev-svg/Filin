package rsl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Настоящая запись РГБ (только блок MARC): Дубровин, «Первая оборона Севастополя», Эксмо, 2026.
func TestRealRecord(t *testing.T) {
	b, err := os.ReadFile("testdata/01015228461.html")
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseRecordHTML(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := r.first("020"); f.Sub("c") != "3000 экз." || f.Ind != "##" {
		t.Errorf("020: %+v", r["020"])
	}
	if len(r["084"]) != 2 {
		t.Errorf("два ББК, получено %d", len(r["084"]))
	}
	c := ToCard(r, "01015228461")
	e := c.Edition
	checks := map[string][2]string{
		"название":     {e.Title, `Первая оборона Севастополя, 1854-1855 гг. "Русская Троя"`},
		"город":        {e.City, "Москва"},
		"издательство": {e.Publisher, "Эксмо"},
		"формат":       {e.Format, "21 см"},
		"серия":        {e.Series, "Герои русских побед"},
		"возраст":      {e.AgeRating, "16+"},
		"ББК":          {e.BBK, "Т3(2)472-684.134,02"},
		"источник":     {e.SourceURL, "https://search.rsl.ru/ru/record/01015228461"},
	}
	for name, v := range checks {
		if v[0] != v[1] {
			t.Errorf("%s: %q, ждали %q", name, v[0], v[1])
		}
	}
	if e.Year != 2026 || e.Pages != 475 || e.Circulation != 3000 || e.PrintPrice != "" {
		t.Errorf("год/страницы/тираж/цена: %d %d %d %q", e.Year, e.Pages, e.Circulation, e.PrintPrice)
	}
	if len(e.People) != 1 || e.People[0].Name != "Дубровин Николай Федорович" || e.People[0].Role != "author" {
		t.Errorf("люди: %+v", e.People)
	}
	if c.Lives["Дубровин Николай Федорович"] != "1837–1904" {
		t.Errorf("годы жизни: %q", c.Lives)
	}
	if len(c.ISBNs) != 1 || c.ISBNs[0] != "978-5-04-243118-0" {
		t.Errorf("ISBN: %q", c.ISBNs)
	}
	if e.Language != "" {
		t.Errorf("русский язык издания не пишется: %q", e.Language)
	}
}

// Синтетическая запись: то, чего нет в настоящей, — переводчик, редактор, цена и тираж,
// номер в серии, том, издание, язык оригинала, содержание.
const synthetic = `<div id="marc-rec"><table class="expanded_search">
<tr class="table-body"><td class="table-text"><strong>020</strong></td><td class="table-text"><strong>##</strong><br/><strong>$c</strong>&nbsp;1 р. 20 к., 100 000 экз.</td></tr>
<tr class="table-body"><td class="table-text"><strong>041</strong></td><td class="table-text"><strong>1#</strong><br/><strong>$a</strong>&nbsp;rus<br/><strong>$h</strong>&nbsp;eng</td></tr>
<tr class="table-body"><td class="table-text"><strong>100</strong></td><td class="table-text"><strong>1#</strong><br/><strong>$a</strong>&nbsp;Скотт, Вальтер<br/><strong>$d</strong>&nbsp;1771-1832</td></tr>
<tr class="table-body"><td class="table-text"><strong>245</strong></td><td class="table-text"><strong>10</strong><br/><strong>$a</strong>&nbsp;Собрание сочинений :<br/><strong>$b</strong>&nbsp;в 20 т. /<br/><strong>$n</strong>&nbsp;Т. 3.<br/><strong>$p</strong>&nbsp;Айвенго</td></tr>
<tr class="table-body"><td class="table-text"><strong>250</strong></td><td class="table-text"><strong>##</strong><br/><strong>$a</strong>&nbsp;2-е изд.</td></tr>
<tr class="table-body"><td class="table-text"><strong>260</strong></td><td class="table-text"><strong>##</strong><br/><strong>$a</strong>&nbsp;Москва ;<br/><strong>$a</strong>&nbsp;Ленинград :<br/><strong>$b</strong>&nbsp;Гослитиздат,<br/><strong>$c</strong>&nbsp;1961</td></tr>
<tr class="table-body"><td class="table-text"><strong>300</strong></td><td class="table-text"><strong>##</strong><br/><strong>$a</strong>&nbsp;543 с.<br/><strong>$b</strong>&nbsp;ил.<br/><strong>$c</strong>&nbsp;21 см</td></tr>
<tr class="table-body"><td class="table-text"><strong>490</strong></td><td class="table-text"><strong>0#</strong><br/><strong>$a</strong>&nbsp;Библиотека приключений<br/><strong>$v</strong>&nbsp;№ 12</td></tr>
<tr class="table-body"><td class="table-text"><strong>505</strong></td><td class="table-text"><strong>0#</strong><br/><strong>$a</strong>&nbsp;Айвенго -- Примечания</td></tr>
<tr class="table-body"><td class="table-text"><strong>700</strong></td><td class="table-text"><strong>1#</strong><br/><strong>$a</strong>&nbsp;Бекетова, Е.<br/><strong>$4</strong>&nbsp;trl</td></tr>
<tr class="table-body"><td class="table-text"><strong>700</strong></td><td class="table-text"><strong>1#</strong><br/><strong>$a</strong>&nbsp;Иванов, И. И.<br/><strong>$e</strong>&nbsp;ред.</td></tr>
</table></div>`

func TestSyntheticRecord(t *testing.T) {
	r, err := ParseRecordHTML(synthetic)
	if err != nil {
		t.Fatal(err)
	}
	e := ToCard(r, "000000001").Edition
	if e.Title != "Собрание сочинений" || e.Volume != "Т. 3. Айвенго" { // «в 20 т.» — не название
		t.Errorf("название и том: %q / %q", e.Title, e.Volume)
	}
	if e.PrintPrice != "1 р. 20 к." || e.Circulation != 100000 {
		t.Errorf("цена и тираж: %q %d", e.PrintPrice, e.Circulation)
	}
	if e.City != "Москва" || e.Publisher != "Гослитиздат" || e.Year != 1961 || e.EditionNote != "2-е изд." {
		t.Errorf("выходные: %q %q %d %q", e.City, e.Publisher, e.Year, e.EditionNote)
	}
	if e.Series != "Библиотека приключений" || e.SeriesNo != "12" || e.Illustrations != "ил." {
		t.Errorf("серия и иллюстрации: %q %q %q", e.Series, e.SeriesNo, e.Illustrations)
	}
	if e.OrigLanguage != "английский" || e.Contents != "Айвенго\nПримечания" {
		t.Errorf("язык и содержание: %q %q", e.OrigLanguage, e.Contents)
	}
	roles := map[string]string{}
	for _, p := range e.People {
		roles[p.Name] = p.Role
	}
	if roles["Скотт Вальтер"] != "author" || roles["Бекетова Е."] != "translator" || roles["Иванов И. И."] != "editor" {
		t.Errorf("роли: %v", roles)
	}
}

func TestRecordID(t *testing.T) {
	for in, want := range map[string]string{
		"https://search.rsl.ru/ru/record/01015228461":     "01015228461",
		"https://search.rsl.ru/ru/record/01015228461?x=1": "01015228461",
		"01015228461": "01015228461",
		" search.rsl.ru/ru/record/01015228461#marc ": "01015228461",
	} {
		if got, err := RecordID(in); err != nil || got != want {
			t.Errorf("RecordID(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "https://search.rsl.ru/ru/search#q=isbn:978", "../../etc", "123"} {
		if _, err := RecordID(bad); err == nil {
			t.Errorf("RecordID(%q) должна отказать", bad)
		}
	}
}

func TestFetchSendsUserAgentAndParses(t *testing.T) {
	page, _ := os.ReadFile("testdata/01015228461.html")
	var ua, path string
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua, path = r.UserAgent(), r.URL.Path
		hits++
		if r.URL.Path == "/ru/record/00000000000" {
			http.Error(w, "нет", http.StatusNotFound)
			return
		}
		w.Write(page)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL}
	r, err := c.Fetch(context.Background(), "01015228461")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/ru/record/01015228461" || !strings.HasPrefix(ua, "Filin/") {
		t.Errorf("запрос: %q %q", path, ua)
	}
	// предпросмотр и сохранение одной записи — один запрос в РГБ
	if _, err := c.Fetch(context.Background(), "01015228461"); err != nil || hits != 1 {
		t.Errorf("повторное чтение: %v, запросов %d", err, hits)
	}
	// неудачный ответ не запоминается: следующая попытка снова идёт в РГБ
	for i := 0; i < 2; i++ {
		if _, err := c.Fetch(context.Background(), "00000000000"); err == nil {
			t.Error("404 принят за запись")
		}
	}
	if hits != 3 {
		t.Errorf("ошибка запомнилась: запросов %d, ждали 3", hits)
	}
	if _, ok := r.first("245"); !ok {
		t.Error("запись не разобрана")
	}
	if _, err := ParseRecordHTML("<html>страница без записи</html>"); err == nil {
		t.Error("страница без MARC разобрана")
	}
}
