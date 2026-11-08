package ocr

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// Настоящие пробы с телефона: Tesseract по фото М. Хастингса «Операция „Оверлорд“»
// (Прогресс, 1989) — оборот титула с каталожной карточкой и сам титул. Ожидаемое
// списано с фотографии, а не с выдачи разбора.

func probe(t *testing.T, name string) Draft {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	d := Parse(ParseTSV(data))
	t.Logf("строки: %q", d.Lines)
	return d
}

func TestRealCatalogCard(t *testing.T) {
	d := probe(t, "overlord-card.tsv")
	if d.Kind != KindCard {
		t.Fatalf("карточка не узнана: %q", d.Kind)
	}
	check := func(field string, got, want any) {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %q, ждали %q", field, got, want)
		}
	}
	check("название", d.Title, "Операция «Оверлорд»")
	check("подзаголовок", d.Subtitle, "Как был открыт второй фронт")
	check("авторы", d.Authors, []string{"Хастингс М."})
	check("участники", d.People, []Person{{Name: "Ржешевский О. А.", Role: "editor"}})
	check("язык оригинала", d.OrigLanguage, "английский")
	check("город", d.City, "Москва")
	check("издательство", d.Publisher, "Прогресс")
	check("год", d.Year, 1989)
	check("страницы", d.Pages, 0) // в карточке «000 с.» — заглушка
	check("ББК", d.BBK, "63.3(0)62")
	check("авторский знак", d.AuthorSign, "Х 24")
	check("аннотация", d.Annotation, "В книге рассказывается о самой крупной десантной операции второй мировой войны — "+
		"высадке англо-американских войск летом 1944 г. в Северной Франции, рассматриваются действия как западных "+
		"союзников, так и немецко-фашистского командования и войск на различных этапах операции.")
}

func TestRealTitlePage(t *testing.T) {
	d := probe(t, "overlord-title.tsv")
	if d.Kind != KindTitle {
		t.Fatalf("титул принят за %q", d.Kind)
	}
	if !reflect.DeepEqual(d.Authors, []string{"Макс Хастингс"}) || d.City != "Москва" || d.Publisher != "Прогресс" || d.Year != 1989 {
		t.Errorf("титул: %+v", d)
	}
	if d.OrigLanguage != "английский" {
		t.Errorf("язык оригинала %q", d.OrigLanguage)
	}
	// «Ржешщевского» — так прочитал Tesseract; разбор отвечает за роль и падеж, а не за буквы
	want := []Person{{Name: "Федотов Е. М.", Role: "translator"}, {Name: "Ржешщевский О. А.", Role: "editor"}}
	if !reflect.DeepEqual(d.People, want) {
		t.Errorf("участники %q", d.People)
	}
}

func TestNominative(t *testing.T) {
	for in, want := range map[string]string{"Ржешевского": "Ржешевский", "Федотова": "Федотов", "Федотовой": "Федотова",
		"Забелок": "Забелок", "Ильина": "Ильин", "Горбачевского": "Горбачевский"} {
		if got := nominative(in); got != want {
			t.Errorf("%s → %s, ждали %s", in, got, want)
		}
	}
}

func TestRoleNames(t *testing.T) {
	people, authors := roleNames(" И. Ильф, Е. Петров; предисл. Д. Николаева; худож. Кукрыниксы")
	if !reflect.DeepEqual(authors, []string{"Ильф И.", "Петров Е."}) {
		t.Errorf("авторы %q", authors)
	}
	if len(people) != 0 {
		t.Errorf("предисловие и художник без инициалов — не люди с ролью: %q", people)
	}
	people, _ = roleNames("Пер. с нем. Н. Волжиной; Под ред. А. А. Смирнова; Сост. И. Петрова")
	want := []Person{{"Волжина Н.", "translator"}, {"Смирнов А. А.", "editor"}, {"Петрова И.", "compiler"}}
	if !reflect.DeepEqual(people, want) {
		t.Errorf("участники %q", people)
	}
}

// Титул «Солдата» (1958): «ПОД РЕДАКЦИЕЙ … генерал-майора МИЛЬШТЕЙНА М. А.» — фамилия капсом
// перед инициалами, в родительном. Раньше редактор терялся целиком.
func TestRoleNamesSurnameFirst(t *testing.T) {
	people, _ := roleNames("Перевод с английского ПОД РЕДАКЦИЕЙ кандидата военных наук генерал-майора МИЛЬШТЕЙНА М. А.")
	if len(people) != 1 || people[0] != (Person{Name: "Мильштейн М. А.", Role: "editor"}) {
		t.Errorf("редактор: %+v", people)
	}
	// обычный порядок в том же куске главнее: обратный не придумывает лишних людей
	people, _ = roleNames("Пер. с англ. Е. М. Федотовой, И. С. Петрова")
	if len(people) != 2 || people[0].Name != "Федотова Е. М." || people[1].Name != "Петров И. С." {
		t.Errorf("обычный порядок: %+v", people)
	}
	// именительный после «Редактор»: капс приводится, падеж не трогается
	people, _ = roleNames("Редактор РИМСКИЙ-КОРСАКОВ Н. А.")
	if len(people) != 1 || people[0].Name != "Римский-Корсаков Н. А." {
		t.Errorf("капс с дефисом: %+v", people)
	}
}

// Оборот «Талейрана» Тарле (Республика, 1993), снятый перевёрнутым телефоном: таблица — от
// прохода, развёрнутого на 180°. Tesseract читает «ISBN» как «15ВХ», «.— М.:» как «--- М.:»,
// знак «Т20» как «т20», «317 с.» как «317 е.»; раньше страница принималась за титул, и ББК,
// знак и аннотация терялись. Ожидаемое списано с фотографии.
func TestRealCardMisreadMarks(t *testing.T) {
	d := probe(t, "tarle-card-upside-down.tsv")
	if d.Kind != KindCard {
		t.Fatalf("карточка не узнана: %q", d.Kind)
	}
	check := func(field string, got, want any) {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %q, ждали %q", field, got, want)
		}
	}
	check("название", d.Title, "Талейран")
	check("подзаголовок", d.Subtitle, "Из мемуаров Талейрана")
	check("авторы", d.Authors, []string{"Тарле Е. В."})
	check("город", d.City, "Москва")
	check("издательство", d.Publisher, "Республика")
	check("год", d.Year, 1993)
	check("страницы", d.Pages, 317)
	check("ISBN", d.ISBN, []string{"5-250-02008-9"})
	check("ББК", d.BBK, "63.3(4Фр)")
	check("авторский знак", d.AuthorSign, "Т 20")
	if !strings.HasPrefix(d.Annotation, "Герой кни") { // «книги» Tesseract читает «книти»
		t.Errorf("аннотация — абзац после номера: %.60q", d.Annotation)
	}
}

// Оборот Степкина (2017): описание не по ГОСТу, но «ISBN» (прочитан «15ВЖХ») и УДК делают
// страницу карточкой — номер и УДК попадают в черновик, а по номеру карточку заполнит РГБ.
func TestRealBackWithISBNAndUDK(t *testing.T) {
	d := probe(t, "stepkin-back.tsv")
	if d.Kind != KindCard {
		t.Fatalf("оборот с ISBN и УДК принят за %q", d.Kind)
	}
	if len(d.ISBN) == 0 || d.ISBN[0] != "978-5-600-01715-3" || d.UDK != "94:355:359" {
		t.Errorf("ISBN %q, УДК %q", d.ISBN, d.UDK)
	}
}

// Карточка без аннотации: после шифров — знак охраны авторского права, это не аннотация.
func TestCardWithoutAnnotation(t *testing.T) {
	at := func(top int, s string) Line { return Line{Text: s, Top: top, Height: 30, Conf: 90} }
	var d Draft
	parseCard([]Line{
		at(100, "Х 24 Операция «Оверлорд»: Как был открыт второй фронт.— М.: Прогресс, 1989.— 352 с.: ил."),
		at(400, "ББК 63.3(0)62"),
		at(600, "© «Прогресс», 1989"),
	}, &d)
	if d.Annotation != "" {
		t.Errorf("аннотация из выходных сведений: %q", d.Annotation)
	}
	if !IsCard([]string{"15ВЖХ 978-5-600-01715-3", "УДК 94:355:359"}) {
		t.Error("искажённая подпись с номером — признак карточки")
	}
	if IsCard([]string{"15ВН", "УДК"}) { // подпись без номера — не в счёт, остаётся один признак
		t.Error("искажённая подпись без номера засчитана")
	}
}

// Терпимость к подменам не должна делать карточкой обычный титул.
func TestTitleStaysTitle(t *testing.T) {
	if d := probe(t, "overlord-title.tsv"); d.Kind != KindTitle {
		t.Errorf("титул «Оверлорда» принят за %q", d.Kind)
	}
	for _, lines := range [][]string{
		{"М. РИДЖУЭЙ", "СОЛДАТ", "Перевод с английского", "ВОЕННОЕ ИЗДАТЕЛЬСТВО", "МОСКВА — 1958"},
		{"Е. В. Тарле", "ТАЛЕЙРАН", "Из мемуаров Талейрана", "Москва", "Издательство «Республика»", "1993"},
		{"в 20 томах", "Том 3"}, // строчная буква с числом через пробел — не авторский знак
	} {
		if IsCard(lines) {
			t.Errorf("титул принят за карточку: %q", lines)
		}
	}
}

func TestNominativeConsonantStem(t *testing.T) {
	for in, want := range map[string]string{
		"Мильштейна": "Мильштейн", "Шварца": "Шварц", "Гринберга": "Гринберг",
		"Петрова": "Петров", "Ржешевского": "Ржешевский", // прежние правила целы
		"Дюма":   "Дюма",   // короткая несклоняемая — не трогаем
		"Гарсиа": "Гарсиа", // «а» после гласной
		"Кац":    "Кац",
	} {
		if got := nominative(in); got != want {
			t.Errorf("nominative(%q) = %q, want %q", in, got, want)
		}
	}
}
