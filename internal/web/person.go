package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// personURL — адрес страницы человека.
func personURL(name string) string { return "/person?name=" + url.QueryEscape(name) }

// roleTitle — подпись роли в списке книг человека.
func roleTitle(role string) string {
	for _, f := range roleFields {
		if f.Role == role {
			return f.Label
		}
	}
	return role
}

type personPage struct {
	Title string
	P     catalog.PersonInfo
	Books []catalog.PersonBook
	Err   string
	Edit  bool
}

func (s *server) person(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	p, books, err := s.st.Person(name)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "person", personPage{Title: p.Name, P: p, Books: books, Edit: r.URL.Query().Has("edit")})
}

func (s *server) savePerson(w http.ResponseWriter, r *http.Request) {
	p := catalog.PersonInfo{Name: r.FormValue("name"), Life: r.FormValue("life"), About: r.FormValue("about"),
		Link: r.FormValue("link")}
	if err := s.st.SavePerson(p); err != nil {
		if catalog.IsInput(err) {
			_, books, _ := s.st.Person(p.Name)
			s.render(w, "person", personPage{Title: p.Name, P: p, Books: books, Err: err.Error(), Edit: true})
			return
		}
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, personURL(strings.Join(strings.Fields(p.Name), " ")), http.StatusSeeOther)
}

type peoplePage struct {
	Title  string
	People []catalog.PersonCount
	All    bool
}

// people — авторы по алфавиту; ?all — все люди из карточек, с переводчиками и художниками.
func (s *server) people(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Has("all")
	ps, err := s.st.People(!all)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "people", peoplePage{Title: "Авторы", People: ps, All: all})
}
