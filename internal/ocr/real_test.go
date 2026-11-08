package ocr

import (
	"os"
	"reflect"
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
