package rsl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func readRecord(t *testing.T, id string) Record {
	t.Helper()
	b, err := os.ReadFile("testdata/" + id + ".html")
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseRecordHTML(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const (
	denikinVol   = "01009856457" // запись тома: «Т. 2», ISBN, страницы, содержание
	denikinWhole = "01009864382" // запись всего издания: автор, название, Харвест, тираж
)

// Настоящий случай: Деникин, «Очерки русской смуты», т. 2 (Минск, Харвест, 2017).
// Карточка из одной записи тома выходила безымянной — «Т. 2» без автора и издательства.
func TestVolumeWithWholeRecord(t *testing.T) {
	vol, whole := readRecord(t, denikinVol), readRecord(t, denikinWhole)
	if got := WholeID(vol); got != denikinWhole {
		t.Fatalf("номер записи издания: %q", got)
	}
	if got := WholeID(whole); got != "" {
		t.Errorf("у записи издания нашлась ссылка на издание: %q", got)
	}
	c := VolumeCard(vol, denikinVol, whole)
	e := c.Edition
	for name, v := range map[string][2]string{
		"название":     {e.Title, "Очерки русской смуты"},
		"том":          {e.Volume, "Т. 2"},
		"город":        {e.City, "Минск"},
		"издательство": {e.Publisher, "Харвест"},
		"издание":      {e.EditionNote, "2-е изд., с изм."},
		"формат":       {e.Format, "21 см"},
		"серия":        {e.Series, "Воспоминания. Мемуары"},
		"возраст":      {e.AgeRating, "18+"},
		"ББК":          {e.BBK, "Т3(2)711,013.1"},
		"иллюстрации":  {e.Illustrations, "ил."},
		"источник":     {e.SourceURL, RecordURL(denikinVol)},
	} {
		if v[0] != v[1] {
			t.Errorf("%s: %q, ждали %q", name, v[0], v[1])
		}
	}
	if e.Year != 2017 || e.Pages != 1279 || e.Circulation != 1500 {
		t.Errorf("год/страницы/тираж: %d %d %d", e.Year, e.Pages, e.Circulation)
	}
	if len(e.People) != 1 || e.People[0].Name != "Деникин Антон Иванович" || e.People[0].Role != "author" {
		t.Errorf("люди: %+v", e.People)
	}
	if c.Lives["Деникин Антон Иванович"] != "1872–1947" {
		t.Errorf("годы жизни: %q", c.Lives)
	}
	if len(c.ISBNs) != 1 || c.ISBNs[0] != "978-985-18-4088-1" {
		t.Errorf("ISBN: %q", c.ISBNs)
	}
	lines := strings.Split(e.Contents, "\n")
	if len(lines) != 5 || lines[0] != "Белое движение и борьба Добровольческой армии. Май-октябрь 1918" ||
		lines[4] != "Заключительный период борьбы. Январь 1919 - март 1920" {
		t.Errorf("содержание тома (505 $t): %q", lines)
	}
}

// Вторая запись не прочиталась — карточка из ссылки 773 в записи тома: без формата
// и тиража, но с автором, названием и издательством.
func TestVolumeFromLinkOnly(t *testing.T) {
	c := VolumeCard(readRecord(t, denikinVol), denikinVol, nil)
	e := c.Edition
	for name, v := range map[string][2]string{
		"название":     {e.Title, "Очерки русской смуты"},
		"том":          {e.Volume, "Т. 2"},
		"город":        {e.City, "Минск"},
		"издательство": {e.Publisher, "Харвест"},
		"издание":      {e.EditionNote, "2-е изд., с изм."},
		"возраст":      {e.AgeRating, "18+"},
	} {
		if v[0] != v[1] {
			t.Errorf("%s: %q, ждали %q", name, v[0], v[1])
		}
	}
	if e.Year != 2017 || e.Pages != 1279 {
		t.Errorf("год/страницы: %d %d", e.Year, e.Pages)
	}
	if len(e.People) != 1 || e.People[0].Name != "Деникин Антон Иванович" || c.Lives["Деникин Антон Иванович"] != "1872–1947" {
		t.Errorf("автор: %+v %q", e.People, c.Lives)
	}
}

// Клиент сам дочитывает запись издания; РГБ не отдала её — карточка всё равно собирается.
func TestClientCardReadsWholeRecord(t *testing.T) {
	pages := map[string]string{}
	for _, id := range []string{denikinVol, denikinWhole} {
		b, err := os.ReadFile("testdata/" + id + ".html")
		if err != nil {
			t.Fatal(err)
		}
		pages["/ru/record/"+id] = string(b)
	}
	var wholeDown atomic.Bool
	var asks atomic.Int32 // обработчик — в своей горутине
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asks.Add(1)
		p, ok := pages[r.URL.Path]
		if !ok || wholeDown.Load() && strings.HasSuffix(r.URL.Path, denikinWhole) {
			http.Error(w, "нет", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(p))
	}))
	defer srv.Close()

	c := &Client{Base: srv.URL}
	card, err := c.Card(context.Background(), denikinVol)
	if err != nil {
		t.Fatal(err)
	}
	if card.Edition.Title != "Очерки русской смуты" || card.Edition.Circulation != 1500 || asks.Load() != 2 {
		t.Errorf("с записью издания: %q, тираж %d, запросов %d", card.Edition.Title, card.Edition.Circulation, asks.Load())
	}
	// повторно — из памяти, в РГБ не ходим; PeekCard собирает то же
	if _, err := c.Card(context.Background(), denikinVol); err != nil || asks.Load() != 2 {
		t.Errorf("повторное чтение ушло в РГБ: %d запросов (%v)", asks.Load(), err)
	}
	if p, ok := c.PeekCard(denikinVol); !ok || p.Edition.Circulation != 1500 {
		t.Errorf("PeekCard: %v %+v", ok, p.Edition)
	}

	wholeDown.Store(true)
	c2 := &Client{Base: srv.URL}
	card, err = c2.Card(context.Background(), denikinVol)
	if err != nil {
		t.Fatalf("запись издания недоступна — карточка тома должна собраться: %v", err)
	}
	if card.Edition.Title != "Очерки русской смуты" || card.Edition.Publisher != "Харвест" {
		t.Errorf("из ссылки 773: %+v", card.Edition)
	}
}

// Ссылка 773 с живым автором и обозначением «[Карты]»; ISBN комплекта — только тому без своего.
func TestVolumeLinkEdgeCases(t *testing.T) {
	row := func(tag, body string) string {
		return `<tr><td><strong>` + tag + `</strong></td><td><strong>##</strong><br/>` + body + `</td></tr>`
	}
	page := func(rows ...string) Record {
		r, err := ParseRecordHTML(`<div id="marc-rec"><table>` + strings.Join(rows, "") + `</table></div>`)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	link := row("773", `<strong>$a</strong>&nbsp;Степкин, Алексей Андреевич (1975-) <strong>$t</strong>&nbsp;Атлас походов [Карты] : в 3 т. <strong>$d</strong>&nbsp;Москва : Степкин А. А., 2023 <strong>$w</strong>&nbsp;012345678`)
	vol := page(row("245", `<strong>$a</strong>&nbsp;Т. 1`), link)
	c := VolumeCard(vol, "01000000001", nil)
	if e := c.Edition; e.Title != "Атлас походов" || len(e.People) != 1 || e.People[0].Name != "Степкин Алексей Андреевич" ||
		c.Lives["Степкин Алексей Андреевич"] != "1975–" || e.Publisher != "Степкин А. А." || e.Year != 2023 {
		t.Errorf("из ссылки: %+v / %q", e, c.Lives)
	}

	whole := page(row("245", `<strong>$a</strong>&nbsp;Атлас походов`), row("020", `<strong>$a</strong>&nbsp;978-5-0000-0000-1`))
	if c := VolumeCard(vol, "01000000001", whole); len(c.ISBNs) != 1 || c.ISBNs[0] != "978-5-0000-0000-1" {
		t.Errorf("том без своего ISBN — ISBN комплекта: %q", c.ISBNs)
	}
	volISBN := page(row("245", `<strong>$a</strong>&nbsp;Т. 1`), row("020", `<strong>$a</strong>&nbsp;978-5-0000-0001-8`), link)
	if c := VolumeCard(volISBN, "01000000001", whole); len(c.ISBNs) != 1 || c.ISBNs[0] != "978-5-0000-0001-8" {
		t.Errorf("у тома свой ISBN — комплектный не нужен: %q", c.ISBNs)
	}
}

func TestNoiseSubtitle(t *testing.T) {
	for s, want := range map[string]bool{
		"в двух томах": true, "в 20 т.": true, "в 3 кн": true, "пер. с англ": true, "перевод с английского": true,
		"Книга о великих мореплавателях": false, "роман": false, "пер. с англ. Н. Волжиной": false,
	} {
		if noiseSubtitle(s) != want {
			t.Errorf("noiseSubtitle(%q) = %v", s, !want)
		}
	}
}
