package web

import (
	"errors"
	"fmt"
	"net/http"
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
