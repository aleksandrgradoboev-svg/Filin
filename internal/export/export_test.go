package export

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/db"
)

// library заводит каталог, где есть всё, что выгружается: люди по ролям, ISBN, серия,
// собрание, жанр, тема, полка, текущая и прошлая выдача, две оценки — и издание без
// экземпляров. Привязанный телефон — чтобы проверить, что его в выгрузке нет.
func library(t *testing.T) (*catalog.Store, int64, int64) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "filin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st := catalog.New(d)
	isbns, _ := catalog.ParseISBNs("978-0-306-40615-7")
	e := &catalog.Edition{Title: "Белый Клык", Volume: "т. 2", Publisher: "Нигма", City: "М.", Year: 2025,
		People: []catalog.Person{{Name: "Лондон Джек"}, {Name: "Волжина Н.", Role: "translator"},
			{Name: "Любаев П.", Role: "illustrator"}},
		ISBNs: isbns, Series: "Страна приключений", SeriesNo: "12", Genres: []string{"Приключения"},
		Subjects: []string{"Литература -- США -- XX век"}, Pages: 543, CoverType: "твёрдая",
		Details: catalog.Details{Annotation: "Повесть о волке; «<b>» — не тег", Circulation: 5000,
			OrigTitle: "White Fang", OrigLanguage: "английский"},
		Notes: "=HYPERLINK(\"http://evil\")"}
	eid, err := st.AddEdition(e)
	if err != nil {
		t.Fatal(err)
	}
	shelf, err := st.Shelf("Кабинет", "Шкаф 1", "3")
	if err != nil {
		t.Fatal(err)
	}
	cid, err := st.AddCopy(&catalog.Copy{EditionID: eid, ShelfID: shelf, Grade: 4, Cherished: true, Read: true,
		Bookplate: "Из книг Г.", Notes: "-- см. т. 1"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := st.Lend(&catalog.Loan{CopyID: cid, Borrower: "Петя", LentOn: "2026-01-10"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Return(old, "2026-02-01"); err != nil {
		t.Fatal(err)
	}
	// нынешняя выдача записана задним числом — раньше вернувшейся: по дате она вторая, но на руках именно она
	if _, err := st.Lend(&catalog.Loan{CopyID: cid, Borrower: "Маша", LentOn: "2025-12-01", DueOn: "2025-12-31"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []catalog.Valuation{{CopyID: cid, Amount: 300, ValuedOn: "2026-01-01"},
		{CopyID: cid, Amount: 500, AmountTo: 800, ValuedOn: "2026-05-01", Basis: "alib"}} {
		if _, err := st.AddValuation(&v); err != nil {
			t.Fatal(err)
		}
	}
	set, err := st.CreateSet(&catalog.Collection{Title: "Собрание сочинений", VolumesAnnounced: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutInSet(eid, set, []catalog.PlaceRef{{Volume: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePerson(catalog.PersonInfo{Name: "Лондон Джек", Life: "1876–1916"}); err != nil {
		t.Fatal(err)
	}
	bare, err := st.AddEdition(&catalog.Edition{Title: "Без экземпляров"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDevice("realme"); err != nil {
		t.Fatal(err)
	}
	return st, eid, bare
}

func TestJSON(t *testing.T) {
	st, eid, bare := library(t)
	lib, err := Build(st, "v9.9.9", time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := JSON(&buf, lib); err != nil {
		t.Fatal(err)
	}
	raw := buf.String()
	// экранированный «<» (обратная косая + u003c) — собран по частям, чтобы правка файла его не раскрыла
	for _, bad := range []string{"realme", "token", "\\" + "u00" + "3c"} {
		if strings.Contains(raw, bad) {
			t.Errorf("в JSON лишнее: %q", bad)
		}
	}
	var got Library
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Format != Format || got.Version != Version || got.Program != "v9.9.9" || got.ExportedAt != "2026-11-15T12:00:00Z" {
		t.Errorf("метка файла: %+v", got)
	}
	if len(got.Books) != 2 || got.Books[0].ID != eid || got.Books[1].ID != bare {
		t.Fatalf("издания: %+v", got.Books)
	}
	b := got.Books[0]
	if len(b.People) != 3 || b.People[0] != (Role{"Лондон Джек", "author"}) || b.People[1].Role != "translator" {
		t.Errorf("люди: %+v", b.People)
	}
	if len(b.ISBNs) != 1 || b.ISBNs[0] != (ISBN{"9780306406157", true}) {
		t.Errorf("ISBN: %+v", b.ISBNs)
	}
	if b.Series == nil || *b.Series != (Series{"Страна приключений", "12"}) {
		t.Errorf("серия: %+v", b.Series)
	}
	if len(b.Sets) != 1 || b.Sets[0].Set != "Собрание сочинений" || b.Sets[0].Place != "т. 2" {
		t.Errorf("собрания: %+v", b.Sets)
	}
	if strings.Join(b.Genres, "|") != "Приключения" || strings.Join(b.Subjects, "|") != "Литература -- США -- XX век" {
		t.Errorf("жанры, темы: %v %v", b.Genres, b.Subjects)
	}
	if b.Annotation != "Повесть о волке; «<b>» — не тег" || b.Circulation != 5000 || b.OrigTitle != "White Fang" || b.Pages != 543 {
		t.Errorf("выходные данные: %+v", b)
	}
	if len(b.Copies) != 1 {
		t.Fatalf("экземпляры: %+v", b.Copies)
	}
	c := b.Copies[0]
	if c.Shelf == nil || *c.Shelf != (Shelf{"Кабинет", "Шкаф 1", "3"}) || c.GradeName != "очень хорошее" ||
		!c.Read || !c.Cherished || c.NoLend || c.Bookplate != "Из книг Г." {
		t.Errorf("экземпляр: %+v", c)
	}
	if len(c.Loans) != 2 || c.Loans[0].Borrower != "Петя" || c.Loans[0].ReturnedOn != "2026-02-01" ||
		c.Loans[1].Borrower != "Маша" || c.Loans[1].ReturnedOn != "" {
		t.Errorf("выдачи: %+v", c.Loans)
	}
	if len(c.Valuations) != 2 || c.Valuations[0].Amount != 500 || c.Valuations[0].AmountTo != 800 {
		t.Errorf("оценки: %+v", c.Valuations)
	}
	if nb := got.Books[1]; len(nb.Copies) != 0 || nb.Copies == nil || nb.Series != nil || nb.Genres == nil {
		t.Errorf("издание без экземпляров: %+v", nb)
	}
	if len(got.People) != 1 || got.People[0].Life != "1876–1916" {
		t.Errorf("люди: %+v", got.People)
	}
	if len(got.Sets) != 1 || len(got.Sets[0].Places) != 3 || got.Sets[0].Places[1].Place != "т. 2" ||
		len(got.Sets[0].Places[1].Books) != 1 || got.Sets[0].Places[1].Books[0] != eid || len(got.Sets[0].Places[0].Books) != 0 {
		t.Errorf("собрания: %+v", got.Sets)
	}
}

func TestCSV(t *testing.T) {
	st, eid, _ := library(t)
	lib, err := Build(st, "dev", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := CSV(&buf, lib); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte(utf8BOM)) {
		t.Fatal("нет BOM: русский Excel покажет кракозябры")
	}
	r := csv.NewReader(bytes.NewReader(buf.Bytes()[len(utf8BOM):]))
	r.Comma = ';'
	rows, err := r.ReadAll() // заодно проверяет, что во всех строках столько же колонок, сколько в шапке
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("строк %d, ждали шапку + экземпляр + издание без экземпляров", len(rows))
	}
	col := map[string]string{}
	for i, h := range rows[0] {
		col[h] = rows[1][i]
	}
	want := map[string]string{
		"Издание №": "1", "Авторы": "Лондон Джек", "Название": "Белый Клык", "Том": "т. 2", "Год": "2025",
		"ISBN": "9780306406157", "Серия": "Страна приключений", "Номер в серии": "12",
		"Собрания": "Собрание сочинений, т. 2", "Переводчики": "Волжина Н.", "Художники": "Любаев П.",
		"Полка": "Кабинет / Шкаф 1 / 3", "Состояние": "очень хорошее", "Прочитана": "да", "Дорожу": "да",
		"Не выдавать": "", "На руках у": "Маша", "Выдана": "2025-12-01", "Срок возврата": "2025-12-31",
		"Оценка, ₽": "500", "Оценка до, ₽": "800", "Дата оценки": "2026-05-01", "Тираж": "5000",
		// формулы обезврежены: Excel не исполнит
		"Заметки к изданию": `'=HYPERLINK("http://evil")`, "Заметки к экземпляру": "'-- см. т. 1",
	}
	if eid != 1 {
		t.Fatalf("id издания %d", eid)
	}
	for k, v := range want {
		if got, ok := col[k]; !ok || got != v {
			t.Errorf("%s: %q, ждали %q", k, got, v)
		}
	}
	if rows[2][3] != "Без экземпляров" || rows[2][1] != "" {
		t.Errorf("издание без экземпляров: %q", rows[2])
	}
}
