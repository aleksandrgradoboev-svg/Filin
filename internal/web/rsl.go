package web

import (
	"errors"
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
	seen := map[string]bool{}
	for _, n := range base.ISBNs {
		seen[n.Norm] = true
	}
	for _, n := range isbns {
		if !seen[n.Norm] { // один номер из формы и со страницы книги — один раз
			base.ISBNs = append(base.ISBNs, n)
			seen[n.Norm] = true
		}
	}
}
