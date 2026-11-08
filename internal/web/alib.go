package web

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/aleksandrgradoboev-svg/filin/internal/alib"
	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/db"
)

// Аннотация с alib: Филин сам собирает адрес страницы «фамилия + название» и показывает
// человеку аннотацию и предложения продавцов; в карточку текст попадает после его «Взять».

// AlibBase направляет чтение alib на другой адрес и снимает паузу (тесты).
func AlibBase(url string) Option {
	return func(s *server) { s.alib.Base, s.alib.Delay = url, time.Millisecond }
}

type alibPage struct {
	Title   string
	E       catalog.Edition
	Surname string
	Name    string // название для alib
	Err     string
	Page    alib.Page
	Offers  []offerRow // предложения: сначала похожие на это издание
	Copies  []catalog.ShelvedCopy
	Suggest *suggestion // человек отметил аналоги — подсказка оценки
	Today   string
}

// offerRow — предложение продавца на странице: Index — его номер на alib (по нему
// отмечают галочкой), Match — тот же год или то же издательство, что у этого издания.
type offerRow struct {
	alib.Offer
	Index  int
	Match  string // чем похоже: «1989 г.», «Прогресс»; пусто — не похоже
	Picked bool
}

// suggestion — оценка по отмеченным аналогам: от самой низкой цены до самой высокой.
// Сумму ставит человек — подсказка только заполняет форму. Поля — текст формы: при
// ошибке ввода форма возвращается с тем, что человек написал.
type suggestion struct {
	From, To string // To пусто — точная сумма
	Basis    string
}

// words — слова текста для сравнения: регистр, ё, кавычки и знаки не важны.
func words(s string) []string {
	return strings.FieldsFunc(db.Fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// pubGeneric — слова, которые есть в названии многих издательств и ничего не различают.
var pubGeneric = map[string]bool{"изд": true, "во": true, "издательство": true, "издательский": true,
	"дом": true, "тип": true, "типография": true, "ооо": true, "ао": true, "зао": true, "оао": true}

// pubWords — различающие слова издательства: «Изд-во „Наука“» → [наука].
func pubWords(publisher string) []string {
	var out []string
	for _, w := range words(publisher) {
		if !pubGeneric[w] {
			out = append(out, w)
		}
	}
	return out
}

// samePublisher — все различающие слова издательства стоят в описании целыми словами:
// «Мир» не совпадает с «Мировая классика», «Наука» — с «Наукова думка».
func samePublisher(pub []string, desc string) bool {
	if len(pub) == 0 {
		return false
	}
	have := map[string]bool{}
	for _, w := range words(desc) {
		have[w] = true
	}
	for _, w := range pub {
		if !have[w] {
			return false
		}
	}
	return true
}

// offerRows раскладывает предложения: похожие на это издание — первыми. alib держит
// одну страницу на произведение, и издания разных лет там вперемешку.
func offerRows(e catalog.Edition, offers []alib.Offer, picked map[int]bool) []offerRow {
	pub := pubWords(e.Publisher)
	var match, rest []offerRow
	for i, o := range offers {
		row := offerRow{Offer: o, Index: i, Picked: picked[i] && o.Rub > 0}
		var why []string
		if e.Year != 0 && o.Year == e.Year {
			why = append(why, fmt.Sprintf("%d г.", o.Year))
		}
		if samePublisher(pub, o.Desc) {
			why = append(why, e.Publisher)
		}
		row.Match = strings.Join(why, ", ")
		if row.Match != "" {
			match = append(match, row)
		} else {
			rest = append(rest, row)
		}
	}
	return append(match, rest...)
}

// suggest — оценка по отмеченным предложениям; nil — ни одного с ценой не отмечено.
func suggest(rows []offerRow) *suggestion {
	var prices []int
	for _, r := range rows {
		if r.Picked {
			prices = append(prices, r.Rub)
		}
	}
	if len(prices) == 0 {
		return nil
	}
	sort.Ints(prices)
	list := make([]string, len(prices))
	for i, p := range prices {
		list[i] = strconv.Itoa(p)
	}
	n := len(prices)
	word := "предложениям"
	if n%10 == 1 && n%100 != 11 {
		word = "предложению"
	}
	sg := &suggestion{From: strconv.Itoa(prices[0])}
	if prices[n-1] > prices[0] {
		sg.To = strconv.Itoa(prices[n-1])
	}
	sg.Basis = fmt.Sprintf("аналоги на alib.ru: по %d %s — %s руб.", n, word, strings.Join(list, ", "))
	return sg
}

// alibGuess — фамилия и короткое название для адреса alib по карточке.
func alibGuess(e catalog.Edition) (surname, title string) {
	for _, p := range e.People {
		if p.Role == "" || p.Role == "author" {
			// из LiveLib имена приходят «Имя Фамилия», остальные источники — «Фамилия Имя»
			surname = alib.Surname(p.Name, e.Source == "livelib" || e.LiveLibID != "")
			break
		}
	}
	return surname, alib.ShortTitle(e.Title)
}

func (s *server) alibForm(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	e, err := s.st.Edition(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := alibPage{Title: "alib: " + e.Title, E: e}
	p.Surname, p.Name = alibGuess(e)
	q := r.URL.Query()
	if q.Has("title") { // человек поправил фамилию или название
		p.Surname, p.Name = strings.TrimSpace(q.Get("surname")), strings.TrimSpace(q.Get("title"))
	}
	// первый заход ищет сразу по догадке: одна страница alib, как и при поиске руками;
	// повторное открытие берётся из памяти клиента
	if p.Page, err = s.alib.Fetch(r.Context(), p.Surname, p.Name); err != nil {
		p.Err = err.Error()
	}
	picked := map[int]bool{}
	for _, v := range q["pick"] {
		if i, err := strconv.Atoi(v); err == nil {
			picked[i] = true
		}
	}
	p.Offers = offerRows(e, p.Page.Offers, picked)
	if p.Suggest = suggest(p.Offers); p.Suggest != nil {
		if p.Copies, err = s.st.Copies(id); err != nil {
			s.fail(w, err)
			return
		}
		p.Today = catalog.Today()
	}
	s.render(w, "alib", p)
}

// alibValue записывает оценку, подсказанную аналогами: экземпляр выбран на странице alib.
func (s *server) alibValue(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := parseForm(w, r); err != nil {
		s.fail(w, catalog.InputError(err.Error()))
		return
	}
	copyID, err := strconv.ParseInt(r.FormValue("copy"), 10, 64)
	if err != nil {
		s.fail(w, catalog.InputError("не выбран экземпляр"))
		return
	}
	c, err := s.st.CopyByID(copyID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if c.EditionID != id { // экземпляр другой книги — форму подделали или перепутали вкладки
		s.fail(w, catalog.InputError("экземпляр не этой книги"))
		return
	}
	v, msg := valuationForm(r, copyID)
	if msg == "" {
		_, err := s.st.AddValuation(&v)
		if err == nil {
			http.Redirect(w, r, fmt.Sprintf("/book/%d", id), http.StatusSeeOther)
			return
		}
		if !catalog.IsInput(err) {
			s.fail(w, err)
			return
		}
		msg = err.Error()
	}
	// ошибка ввода: та же страница alib с формой, как её заполнил человек, и сообщением —
	// отмеченные аналоги и правки не пропадают
	e, err := s.st.Edition(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := alibPage{Title: "alib: " + e.Title, E: e, Err: msg, Today: r.FormValue("valued_on"),
		Suggest: &suggestion{From: r.FormValue("amount"), To: r.FormValue("amount_to"), Basis: r.FormValue("basis")}}
	p.Surname, p.Name = alibGuess(e)
	if pg, ok := s.alib.Peek(r.FormValue("source_url")); ok {
		p.Page = pg
	} else {
		p.Page = alib.Page{URL: r.FormValue("source_url"), Heading: e.Title}
	}
	p.Offers = offerRows(e, p.Page.Offers, nil)
	if p.Copies, err = s.st.Copies(id); err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "alib", p) // как у оценки в карточке: форма с сообщением, а не страница ошибки
}

func (s *server) alibTake(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := parseForm(w, r); err != nil {
		s.fail(w, catalog.InputError(err.Error()))
		return
	}
	e, err := s.st.Edition(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	text := strings.TrimSpace(strings.ReplaceAll(r.FormValue("annotation"), "\r\n", "\n"))
	src := strings.TrimSpace(r.FormValue("source"))
	if text == "" {
		s.fail(w, catalog.InputError("аннотация пустая"))
		return
	}
	if !strings.HasPrefix(src, "https://www.alib.ru/au-") {
		src = "" // источник — только страница alib, иначе в заметки ничего не пишем
	}
	// своя аннотация без явного «заменить» не перезаписывается; правленый текст человека
	// возвращается в форму, а не теряется на странице ошибки
	if e.Annotation != "" && r.FormValue("replace") == "" {
		p := alibPage{Title: "alib: " + e.Title, E: e, Err: "У книги уже есть аннотация — отметьте «заменить»."}
		p.Surname, p.Name = alibGuess(e)
		pg, ok := s.alib.Peek(src)
		if !ok {
			pg = alib.Page{URL: src, Heading: e.Title}
		}
		pg.Annotation = text
		p.Page = pg
		p.Offers = offerRows(e, pg.Offers, nil)
		s.render(w, "alib", p)
		return
	}
	notes := e.Notes
	if src != "" && !strings.Contains(notes, src) {
		line := "Аннотация — alib: " + src
		if notes != "" {
			notes += "\n"
		}
		notes += line
	}
	if err := s.st.SetAnnotation(id, text, notes); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/book/%d", id), http.StatusSeeOther)
}
