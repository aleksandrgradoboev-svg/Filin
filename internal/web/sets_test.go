package web

import (
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// Собрание от заведения до удаления: места заводятся сами, книга ставится из карточки,
// сдвоенный том закрывает два места, том делится на книги, ошибки ввода — в форме.
func TestSetsFlow(t *testing.T) {
	a := newNetApp(t)
	form := func(path string, v url.Values) (int, string) {
		code, b := a.do(local, "POST", path, "", "application/x-www-form-urlencoded", []byte(v.Encode()))
		return code, html.UnescapeString(string(b))
	}
	get := func(path string) string {
		_, b := a.do(local, "GET", path, "", "", nil)
		return html.UnescapeString(string(b))
	}

	if page := get("/value"); !strings.Contains(page, `href="/sets"`) {
		t.Error("на «Ценности» нет ссылки на собрания")
	}

	// ошибка ввода — форма с тем, что человек написал
	code, page := form("/sets", url.Values{"title": {"Диккенс"}, "volumes": {"тридцать"}})
	if code != http.StatusOK || !strings.Contains(page, "сколько томов — числом") || !strings.Contains(page, `value="Диккенс"`) {
		t.Fatalf("ошибка заведения: %d", code)
	}
	code, _ = form("/sets", url.Values{"title": {"Диккенс. Собрание сочинений"}, "volumes": {"5"}, "publisher": {"ГИХЛ"},
		"year_from": {"1957"}, "year_to": {"1963"}})
	if code != http.StatusSeeOther {
		t.Fatalf("заведение: %d", code)
	}
	sets, _ := a.st.Sets()
	if len(sets) != 1 || sets[0].Places != 5 {
		t.Fatalf("собрание: %+v", sets)
	}
	setURL := "/sets/" + strconv.FormatInt(sets[0].ID, 10)

	ed, _ := a.st.AddEdition(&catalog.Edition{Title: "Диккенс. Т. 3-4"})
	if _, err := a.st.AddCopy(&catalog.Copy{EditionID: ed}); err != nil {
		t.Fatal(err)
	}
	book := "/book/" + strconv.FormatInt(ed, 10)
	if page := get(book); !strings.Contains(page, "В собрание сочинений") || !strings.Contains(page, "Диккенс. Собрание сочинений") {
		t.Error("в карточке нет формы «В собрание»")
	}

	put := url.Values{"set": {strconv.FormatInt(sets[0].ID, 10)}, "volumes": {"3-4"}}
	if code, _ := form(book+"/set", put); code != http.StatusSeeOther {
		t.Fatalf("на место: %d", code)
	}
	if page := get(book); !strings.Contains(page, "т. 3") || !strings.Contains(page, "т. 4") || !strings.Contains(page, setURL) {
		t.Error("в карточке не видно мест в собрании")
	}
	page = get(setURL)
	for _, want := range []string{"Есть 2 из 5", "нет: т. 1, т. 2, т. 5", "Диккенс. Т. 3-4"} {
		if !strings.Contains(page, want) {
			t.Errorf("на странице собрания нет %q", want)
		}
	}

	// тома 9 нет — отказ в карточке, а не новый том
	put.Set("volumes", "9")
	if code, page := form(book+"/set", put); code != http.StatusOK || !strings.Contains(page, "в собрании нет места т. 9") {
		t.Errorf("несуществующий том: %d", code)
	}

	// т. 1 вышел в двух книгах
	if code, _ := form(setURL+"/split", url.Values{"volume": {"1"}, "books": {"2"}}); code != http.StatusSeeOther {
		t.Fatalf("деление тома: %d", code)
	}
	if page := get(setURL); !strings.Contains(page, "т. 1, кн. 2") || !strings.Contains(page, "из 6") {
		t.Error("том не разделён")
	}
	if code, page := form(setURL+"/split", url.Values{"volume": {"1"}, "books": {"2"}}); code != http.StatusOK || !strings.Contains(page, "уже разделён") {
		t.Errorf("повторное деление: %d", code)
	}
	if code, _ := form(setURL+"/place", url.Values{"label": {"Указатель"}}); code != http.StatusSeeOther {
		t.Fatalf("том без номера: %d", code)
	}

	// снять с т. 4 из карточки
	places, _ := a.st.EditionPlaces(ed)
	if len(places) != 2 {
		t.Fatalf("мест у книги: %+v", places)
	}
	code, _ = form("/place/"+strconv.FormatInt(places[1].PlaceID, 10)+"/unplace",
		url.Values{"edition": {strconv.FormatInt(ed, 10)}, "back": {"book"}})
	if code != http.StatusSeeOther {
		t.Fatalf("снять с места: %d", code)
	}
	if p, _ := a.st.EditionPlaces(ed); len(p) != 1 || p[0].Place != "т. 3" {
		t.Errorf("после снятия: %+v", p)
	}

	// убрать лишнее место; чужое место через эту форму не убрать
	_, sp, _ := a.st.Set(sets[0].ID)
	last := sp[len(sp)-1] // «Указатель»
	if code, _ := form(setURL+"/remove", url.Values{"place": {strconv.FormatInt(last.ID, 10)}}); code != http.StatusSeeOther {
		t.Fatalf("убрать место: %d", code)
	}
	if _, sp2, _ := a.st.Set(sets[0].ID); len(sp2) != len(sp)-1 {
		t.Errorf("место не убрано: было %d, стало %d", len(sp), len(sp2))
	}
	other, _ := a.st.CreateSet(&catalog.Collection{Title: "Другое", VolumesAnnounced: 1})
	_, op, _ := a.st.Set(other)
	if code, page := form(setURL+"/remove", url.Values{"place": {strconv.FormatInt(op[0].ID, 10)}}); code != http.StatusOK ||
		!strings.Contains(page, "такого места в этом собрании нет") {
		t.Errorf("чужое место: %d", code)
	}
	if _, op2, _ := a.st.Set(other); len(op2) != 1 {
		t.Error("место чужого собрания убрано")
	}
	a.st.DeleteSet(other)

	if code, _ := form(setURL+"/delete", nil); code != http.StatusSeeOther {
		t.Fatalf("удаление собрания: %d", code)
	}
	if s, _ := a.st.Sets(); len(s) != 0 {
		t.Error("собрание не удалилось")
	}
	if _, err := a.st.Edition(ed); err != nil {
		t.Error("книга пропала вместе с собранием")
	}
	if code, _ := a.do(local, "GET", setURL, "", "", nil); code != http.StatusNotFound {
		t.Errorf("удалённое собрание: %d", code)
	}
}

// «Ценность»: собрание с тем, чего не хватает, и суммой оценок его экземпляров.
func TestValueShowsSets(t *testing.T) {
	a := newNetApp(t)
	page := func() string {
		_, b := a.do(local, "GET", "/value", "", "", nil)
		return html.UnescapeString(string(b))
	}
	if !strings.Contains(page(), "Собраний пока нет") {
		t.Error("без собраний нет подсказки")
	}
	id, _ := a.st.CreateSet(&catalog.Collection{Title: "Чехов. Собрание", VolumesAnnounced: 3})
	ed, _ := a.st.AddEdition(&catalog.Edition{Title: "Чехов. Т. 2"})
	c := catalog.Copy{EditionID: ed}
	a.st.AddCopy(&c)
	if err := a.st.PutInSet(ed, id, []catalog.PlaceRef{{Volume: 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.AddValuation(&catalog.Valuation{CopyID: c.ID, Amount: 400, AmountTo: 900}); err != nil {
		t.Fatal(err)
	}
	p := page()
	for _, want := range []string{"Комплекты", "Чехов. Собрание", "есть 1 из 3", "не хватает:</b> т. 1, т. 3",
		"оценено 1 из 1 экз. на " + price(400, 900)} {
		if !strings.Contains(p, want) {
			t.Errorf("на «Ценности» нет %q", want)
		}
	}
}
