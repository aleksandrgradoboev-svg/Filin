package web

import (
	"net/http"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// Люди издания — отдельным полем на каждую роль, как в выходных данных и в карточках
// магазинов: «Автор», «Переводчик», «Художник»… Несколько человек — через запятую.
type roleField struct {
	Role, Name, Label, Plural, Placeholder string
}

var roleFields = []roleField{
	{"author", "authors", "Автор", "Авторы", "Лондон Джек"},
	{"translator", "translators", "Переводчик", "Перевод", "Волжина Н. А."},
	{"editor", "editors", "Редактор", "Редакторы", "Карпова Е."},
	{"illustrator", "illustrators", "Художник", "Художники", "Любаев П."},
	{"compiler", "compilers", "Составитель", "Составители", ""},
}

// splitNames делит «Ильф И., Петров Е.; Иванов» на имена.
func splitNames(s string) []string {
	var out []string
	for _, n := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if n = strings.Join(strings.Fields(n), " "); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// readPeople собирает людей из полей по ролям: сначала авторы, потом остальные —
// порядок титульного листа.
func readPeople(r *http.Request) []catalog.Person {
	var out []catalog.Person
	for _, f := range roleFields {
		for _, n := range splitNames(r.FormValue(f.Name)) {
			out = append(out, catalog.Person{Name: n, Role: f.Role})
		}
	}
	return out
}

// peopleByRole — значения полей формы: имена через запятую по каждой роли.
func peopleByRole(ps []catalog.Person) map[string]string {
	names := map[string][]string{}
	for _, p := range ps {
		role := p.Role
		if role == "" {
			role = "author"
		}
		names[role] = append(names[role], p.Name)
	}
	out := map[string]string{}
	for _, f := range roleFields {
		out[f.Name] = strings.Join(names[f.Role], ", ")
	}
	return out
}

// peopleGroup — строка карточки: «Перевод: Волжина Н. А.».
type peopleGroup struct {
	Label string
	Names []string
}

func peopleGroups(ps []catalog.Person) []peopleGroup {
	var out []peopleGroup
	for _, f := range roleFields {
		var names []string
		for _, p := range ps {
			if p.Role == f.Role || (p.Role == "" && f.Role == "author") {
				names = append(names, p.Name)
			}
		}
		switch {
		case len(names) == 1:
			out = append(out, peopleGroup{f.Label, names})
		case len(names) > 1:
			out = append(out, peopleGroup{f.Plural, names})
		}
	}
	return out
}
