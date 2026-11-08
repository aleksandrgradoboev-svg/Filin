package web

import (
	"html"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// Темы в карточке и каталоге: длинная цепочка свёрнута в середине, уровень — ссылка
// на отбор, отбор показан плашкой и снимается.
func TestSubjectsOnCardAndCatalog(t *testing.T) {
	a := newNetApp(t)
	get := func(path string) string {
		_, b := a.do(local, "GET", path, "", "", nil)
		return html.UnescapeString(string(b))
	}
	long := "История -- СССР -- Эпоха социализма -- Период гражданской войны (1918-1920) -- Разгром похода Деникина -- Воспоминания"
	ed, _ := a.st.AddEdition(&catalog.Edition{Title: "Очерки русской смуты", Subjects: []string{long, "Военное дело -- СССР"}})
	a.st.AddEdition(&catalog.Edition{Title: "Посторонняя книга"})

	card := get("/book/" + strconv.FormatInt(ed, 10))
	for _, want := range []string{
		`<dt>Темы</dt>`, `class="crumb-more"`, // шесть уровней — середина свёрнута
		`>Разгром похода Деникина</a>`, `>Воспоминания</a>`, `>История</a>`,
		`href="/?t=` + url.QueryEscape("Период гражданской войны (1918-1920)") + `"`,
		`>Военное дело</a> › <a`, // короткая цепочка — без свёртки
	} {
		if !strings.Contains(card, want) {
			t.Errorf("в карточке нет %q", want)
		}
	}

	list := get("/?t=" + url.QueryEscape("Период гражданской войны (1918-1920)"))
	if !strings.Contains(list, "Тема: Период гражданской войны (1918-1920)") || !strings.Contains(list, "Очерки русской смуты") ||
		strings.Contains(list, "Посторонняя книга") || !strings.Contains(list, `name="t"`) {
		t.Error("отбор по теме в каталоге")
	}
}

// Дополнение из РГБ ставит темы только в пустое поле: свои темы человек подбирал сам.
func TestFillSubjectsOnlyEmpty(t *testing.T) {
	src := catalog.Edition{Subjects: []string{"История -- СССР"}}
	var e catalog.Edition
	rep := fillEmpty(&e, src)
	if !reflect.DeepEqual(e.Subjects, src.Subjects) || !strings.Contains(strings.Join(rep.Filled, ","), "темы") {
		t.Errorf("пустые темы не заполнены: %q / %q", e.Subjects, rep.Filled)
	}
	mine := catalog.Edition{Subjects: []string{"Моя тема"}}
	fillEmpty(&mine, src)
	if !reflect.DeepEqual(mine.Subjects, []string{"Моя тема"}) {
		t.Errorf("свои темы перезаписаны: %q", mine.Subjects)
	}
}
