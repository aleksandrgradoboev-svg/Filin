package web

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/isbn"
	"github.com/aleksandrgradoboev-svg/filin/internal/rsl"
)

// Карточка из записи РГБ: запись выбирает человек (в приложении или браузере),
// Филин её только читает — см. пакет rsl.

// Option — настройка сервера; нужна тестам, чтобы подменить РГБ своим сервером.
type Option func(*server)

// RSLBase направляет чтение записей РГБ на другой адрес (тесты).
func RSLBase(url string) Option { return func(s *server) { s.rsl.Base = url } }

var (
	reQuoteMarks = regexp.MustCompile(`[«»"„“()\[\]!?]`)
	reSpaces     = regexp.MustCompile(`\s+`)
)

// rslQuery — что искать в РГБ для заведённой книги, по тем же правилам, что приложение при
// скане (BookActivity): ISBN, если есть; иначе название до двоеточия без кавычек и фамилия
// автора — самое длинное слово без точки («Хастингс М.» → «Хастингс»). fallback — запрос
// только по автору, если по названию и автору пусто.
func rslQuery(e catalog.Edition) (q, fallback string) {
	for _, n := range e.ISBNs {
		if n.Valid {
			return "isbn:" + n.Norm, ""
		}
	}
	title, _, _ := strings.Cut(e.Title, ":")
	words := strings.Fields(reQuoteMarks.ReplaceAllString(title, " "))
	var surname string
	for _, p := range e.People {
		if p.Role != "author" {
			continue
		}
		first, _, _ := strings.Cut(p.Name, ",")
		for _, w := range reSpaces.Split(strings.TrimSpace(first), -1) {
			if w != "" && !strings.Contains(w, ".") && len([]rune(w)) > len([]rune(surname)) {
				surname = w
			}
		}
		break
	}
	var parts []string
	if len(words) > 0 {
		parts = append(parts, "title:("+strings.Join(words, " ")+")")
	}
	if surname != "" {
		parts = append(parts, "author:"+surname)
		if len(words) > 0 {
			fallback = "author:" + surname
		}
	}
	return strings.Join(parts, " "), fallback
}

// rslPageURL — страница поиска РГБ с запросом (одно место для формы, окна на компьютере):
// поля isbn:/title:/author: — без них поиск тонет в полнотексте оцифровок. isbns — номера из
// поля формы новой книги (со сканера), в карточке их ещё нет.
func rslPageURL(e catalog.Edition, isbns string) string {
	q, _ := rslQuery(e)
	if good, _ := catalog.ParseISBNs(isbns); len(good) > 0 && good[0].Valid {
		q = "isbn:" + good[0].Norm
	}
	return "https://search.rsl.ru/ru/search#q=" + strings.ReplaceAll(url.QueryEscape(q), "+", "%20")
}

// rslSearchURL — ссылка «Дополнить из РГБ» в карточке: /book/{id}/rsl с готовым запросом.
// Приложение на телефоне перехватывает её и ищет своим браузером; в обычном браузере
// сервер ведёт на форму дополнения.
func rslSearchURL(e catalog.Edition) string {
	q, fb := rslQuery(e)
	v := url.Values{"q": {q}}
	if fb != "" {
		v.Set("fallback", fb)
	}
	return fmt.Sprintf("/book/%d/rsl?%s", e.ID, v.Encode())
}

// RSLPicker — окно поиска РГБ на компьютере (desktop.PickRSL): открывает страницу поиска
// pageURL и ждёт, на какую запись нажмёт человек; ok=false — закрыл окно, ничего не выбрав.
type RSLPicker func(pageURL string) (record string, ok bool, err error)

// WithRSLPicker включает поиск РГБ в окне на компьютере. Без него (filin serve, не Windows)
// «Дополнить из РГБ» ведёт на форму со ссылкой на поиск в браузере.
func WithRSLPicker(p RSLPicker) Option { return func(s *server) { s.rslPicker = p } }

// rslJob — открытое окно поиска для книги: страница в браузере опрашивает, чем кончилось.
type rslJob struct {
	done   bool
	record string
	err    string
}

// rslSearch — «Дополнить из РГБ» не из приложения (приложение перехватывает ссылку само):
// на этом компьютере — окно поиска РГБ и страница ожидания; иначе — форма со ссылкой.
// Окно открывается только по запросу с самого компьютера: нажатие с телефона (старое
// приложение без перехвата) не должно выбрасывать окно на чужой экран.
func (s *server) rslSearch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if s.rslPicker == nil || !fromThisComputer(r) {
		http.Redirect(w, r, fmt.Sprintf("/book/%d/edit#rsl", id), http.StatusSeeOther)
		return
	}
	e, err := s.st.Edition(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	q, _ := rslQuery(e) // запрос — из карточки, а не из адреса: окно ищет только по своей книге
	page := rslPageURL(e, "")
	s.rslMu.Lock()
	if s.rslJobs == nil {
		s.rslJobs = map[int64]*rslJob{}
	}
	// окно уже открыто — второе не открываем; закончилось, а итог никто не забрал (вкладку
	// ожидания закрыли) — старый итог не в счёт, открываем заново
	if job, ok := s.rslJobs[id]; !ok || job.done {
		job := &rslJob{}
		s.rslJobs[id] = job
		go func() {
			rec, _, err := s.rslPicker(page)
			s.rslMu.Lock()
			defer s.rslMu.Unlock()
			job.done, job.record = true, rec
			if err != nil {
				job.err = err.Error()
			}
		}()
	}
	s.rslMu.Unlock()
	s.render(w, "rsl_wait", rslWaitPage{Title: "Поиск в РГБ", E: e, Query: q})
}

type rslWaitPage struct {
	Title string
	E     catalog.Edition
	Query string
}

// rslStatus — чем кончилось окно поиска: ждём / выбрана запись (куда идти) / закрыто / ошибка.
// Итог отдаётся один раз: следующее «Дополнить» откроет окно заново.
func (s *server) rslStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.rslMu.Lock()
	job, ok := s.rslJobs[id]
	var out map[string]string
	switch {
	case !ok:
		out = map[string]string{"state": "none"}
	case !job.done:
		out = map[string]string{"state": "waiting"}
	default:
		delete(s.rslJobs, id)
		switch {
		case job.err != "":
			out = map[string]string{"state": "error", "error": job.err}
		case job.record != "":
			out = map[string]string{"state": "picked",
				"url": fmt.Sprintf("/book/%d/edit?rsl=%s#rsl", id, url.QueryEscape(job.record))}
		default:
			out = map[string]string{"state": "closed"}
		}
	}
	s.rslMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// fromThisComputer — запрос пришёл с самого компьютера (127.0.0.1 / ::1).
func fromThisComputer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// rslCard читает запись по номеру или ссылке.
func (s *server) rslCard(r *http.Request, ref string) (rsl.Card, error) {
	id, err := rsl.RecordID(ref)
	if err != nil {
		return rsl.Card{}, catalog.InputError(err.Error())
	}
	rec, err := s.rsl.Fetch(r.Context(), id)
	if err != nil {
		return rsl.Card{}, err
	}
	c := rsl.ToCard(rec, id)
	good, _ := catalog.ParseISBNs(c.ISBNs...)
	c.Edition.ISBNs = good
	return c, nil
}

// rememberedLives — годы жизни людей из записи РГБ, которой только что заполняли форму.
// Только из памяти клиента: сохранение в РГБ не ходит — справка вторична, а ждать РГБ
// ради неё при сохранении карточки незачем. Запись забыта (прошёл час, перезапуск) — нет справки.
func (s *server) rememberedLives(ref string) map[string]string {
	id, err := rsl.RecordID(ref)
	if err != nil {
		return nil
	}
	rec, ok := s.rsl.Peek(id)
	if !ok {
		return nil
	}
	return rsl.ToCard(rec, id).Lives
}

// onlyPeople — годы жизни только тех, кто есть в карточке: при дополнении человек мог
// оставить своё написание имени, и справка на имя из записи осталась бы без книг.
func onlyPeople(lives map[string]string, people []catalog.Person) map[string]string {
	in := map[string]bool{}
	for _, p := range people {
		in[p.Name] = true
	}
	out := map[string]string{}
	for name, life := range lives {
		if in[name] {
			out[name] = life
		}
	}
	return out
}

// saveLives дописывает годы жизни людям, у которых справки ещё нет. Сбой — не повод
// отказывать в сохранении книги: справка вторична.
func (s *server) saveLives(lives map[string]string) {
	for name, life := range lives {
		p, _, err := s.st.Person(name)
		if err != nil && !errors.Is(err, catalog.ErrNotFound) {
			continue
		}
		if p.Life == "" && p.About == "" && p.Link == "" {
			s.st.SavePerson(catalog.PersonInfo{Name: name, Life: life})
		}
	}
}

// apiRSL — предпросмотр записи для формы приложения: что предложить человеку проверить.
func (s *server) apiRSL(w http.ResponseWriter, r *http.Request) {
	c, err := s.rslCard(r, r.PathValue("id"))
	if err != nil {
		if catalog.IsInput(err) {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		apiError(w, http.StatusBadGateway, err.Error())
		return
	}
	e := c.Edition
	authors := []string{}
	others := []string{}
	for _, p := range e.People {
		if p.Role == "author" {
			authors = append(authors, p.Name)
		} else {
			others = append(others, roleTitle(p.Role)+": "+p.Name)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"title": e.Title, "authors": authors, "others": others, "publisher": e.Publisher, "city": e.City,
		"year": e.Year, "pages": e.Pages, "series": e.Series, "series_no": e.SeriesNo, "volume": e.Volume,
		"circulation": e.Circulation, "url": e.SourceURL,
	})
}

// fillReport — что дала запись РГБ уже заведённой книге: какие пустые поля заполнены и где
// запись расходится с карточкой. Расхождения только показываются — карточка человека главнее.
type fillReport struct {
	Filled  []string // «год», «тираж», «переводчик»
	Differs []string // «издательство: в РГБ «Наука»»
}

// fillEmpty дополняет издание e записью РГБ src: заполняет только пустые поля, людей —
// только тех ролей, которых в карточке нет, ISBN — недостающие. Ничего не перезаписывает.
func fillEmpty(e *catalog.Edition, src catalog.Edition) fillReport {
	var rep fillReport
	str := func(label string, dst *string, v string) {
		v = strings.TrimSpace(v)
		switch {
		case v == "":
		case *dst == "":
			*dst = v
			rep.Filled = append(rep.Filled, label)
		case !strings.EqualFold(strings.TrimSpace(*dst), v):
			rep.Differs = append(rep.Differs, label+": в РГБ «"+v+"»")
		}
	}
	num := func(label string, dst *int, v int) {
		switch {
		case v == 0:
		case *dst == 0:
			*dst = v
			rep.Filled = append(rep.Filled, label)
		case *dst != v:
			rep.Differs = append(rep.Differs, fmt.Sprintf("%s: в РГБ %d", label, v))
		}
	}
	if e.Title == "" || !sameTitle(e.Title, src.Title) {
		str("название", &e.Title, src.Title)
	}
	str("издательство", &e.Publisher, src.Publisher)
	str("город", &e.City, src.City)
	num("год", &e.Year, src.Year)
	str("том", &e.Volume, src.Volume)
	str("серия", &e.Series, src.Series)
	str("номер в серии", &e.SeriesNo, src.SeriesNo)
	str("ББК", &e.BBK, src.BBK)
	str("УДК", &e.UDK, src.UDK)
	str("авторский знак", &e.AuthorSign, src.AuthorSign)
	str("формат", &e.Format, src.Format)
	str("тип обложки", &e.CoverType, src.CoverType)
	num("страниц", &e.Pages, src.Pages)
	d, s := &e.Details, src.Details
	str("название оригинала", &d.OrigTitle, s.OrigTitle)
	str("язык оригинала", &d.OrigLanguage, s.OrigLanguage)
	str("язык издания", &d.Language, s.Language)
	str("сведения об издании", &d.EditionNote, s.EditionNote)
	num("тираж", &d.Circulation, s.Circulation)
	str("цена на книге", &d.PrintPrice, s.PrintPrice)
	str("типография", &d.Printer, s.Printer)
	str("возрастная маркировка", &d.AgeRating, s.AgeRating)
	str("аннотация", &d.Annotation, s.Annotation)
	str("содержание", &d.Contents, s.Contents)
	str("иллюстрации", &d.Illustrations, s.Illustrations)
	str("размеры", &d.Dimensions, s.Dimensions)

	have := map[string]bool{}
	for _, p := range e.People {
		have[p.Role] = true
	}
	added := map[string]bool{}
	for _, p := range src.People {
		if have[p.Role] {
			continue // авторы, переводчики… уже указаны человеком — не смешиваем
		}
		e.People = append(e.People, p)
		if !added[p.Role] {
			added[p.Role] = true
			rep.Filled = append(rep.Filled, strings.ToLower(roleTitle(p.Role)))
		}
	}
	if addISBNs(&e.ISBNs, src.ISBNs) {
		rep.Filled = append(rep.Filled, "ISBN")
	}
	return rep
}

// addISBNs дописывает к списку недостающие номера; true — что-то добавилось. Один номер
// из двух источников — один раз.
func addISBNs(dst *[]isbn.ISBN, more []isbn.ISBN) bool {
	seen := map[string]bool{}
	for _, n := range *dst {
		seen[n.Norm] = true
	}
	added := false
	for _, n := range more {
		if !seen[n.Norm] {
			*dst = append(*dst, n)
			seen[n.Norm] = true
			added = true
		}
	}
	return added
}

// sameTitle — запись РГБ про ту же книгу: название совпадает или продолжает карточку
// подзаголовком и выходными данными («Первая оборона Севастополя, 1854-1855 гг. …»).
func sameTitle(card, record string) bool {
	c, r := strings.Join(words(card), " "), strings.Join(words(record), " ")
	return c != "" && (c == r || strings.HasPrefix(r, c+" "))
}

// mergeForm кладёт поверх записи РГБ то, что человек оставил в форме: его правка главнее.
// Авторов из формы ставит вместо авторов записи; переводчики, редакторы, художники — из записи.
func mergeForm(base *catalog.Edition, title, publisher string, year int, authors []catalog.Person, isbns []isbn.ISBN) {
	if t := strings.TrimSpace(title); t != "" {
		base.Title = t
	}
	if p := strings.TrimSpace(publisher); p != "" {
		base.Publisher = p
	}
	if year != 0 {
		base.Year = year
	}
	if len(authors) > 0 {
		people := append([]catalog.Person{}, authors...)
		for _, p := range base.People {
			if p.Role != "author" {
				people = append(people, p)
			}
		}
		base.People = people
	}
	addISBNs(&base.ISBNs, isbns) // один номер из формы и со страницы книги — один раз
}
