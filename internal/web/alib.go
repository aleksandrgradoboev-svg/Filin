package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/alib"
	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
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
	s.render(w, "alib", p)
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
