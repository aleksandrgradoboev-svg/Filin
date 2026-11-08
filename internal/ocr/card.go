package ocr

import (
	"regexp"
	"strconv"
	"strings"
)

// Каталожная карточка — библиографическое описание по ГОСТу на обороте титула или
// в конце книги: «Автор. Название: подзаголовок / ответственность.— Город: Издательство,
// Год.— 352 с.: ил.», рядом ББК, авторский знак и аннотация. Пунктуация в ней — часть
// стандарта, поэтому разбор идёт по знакам, а не по расположению строк, как у титула.

// Распознавание путает латиницу с похожей кириллицей и цифрами, и шаблоны это прощают —
// каждая поправка списана с настоящей выдачи Tesseract по снимкам с телефона:
// «ISBN» → «15ВЖХ», «15ВХ», «1SBN»; «.— М.:» → «--- М.:»; «317 с.:» → «317 е.:»;
// авторский знак «Т20» → «т20»; тире в номере «5—250—02008—9».
const isbnLabel = `(?:ISBN|[I1lі|][S5Ѕ][BВ8][NНХИЖ]{1,2})`

var (
	signRe    = regexp.MustCompile(`^([А-ЯЁ])\s?(\d{1,3})(?:\s|$)|^([а-яё])(\d{1,3})(?:\s|$)`) // «Х 24»; строчная — только слитно: «т20»
	areaRe    = regexp.MustCompile(`\.\s*[—–]\s*|\.\s+-\s+|\s*[—–-]{2,}\s*`) // «.— » между областями описания
	imprintRe = regexp.MustCompile(`^(.+?)\s*:\s*(.+?),\s*(?:\D{0,3})?(\d{4})`)
	pagesRe   = regexp.MustCompile(`(\d+)\s*[сеc]\.`)
	isbnRe    = regexp.MustCompile(`(?i)` + isbnLabel + `\s*:?\s*([\dXХ][\dXХ\s—–-]{8,20}[\dXХ])`)
	// ББК: «63.3(0)62», «63.3 (0) 62», «63.3(4Фр)» — в скобках бывает буквенный код страны
	bbkRe = regexp.MustCompile(`ББК\s*([0-9][0-9.\-]*(?:\s?\([0-9А-ЯЁа-яё.\-]+\))?(?:\s?[0-9][0-9.,\-]*[0-9])?)`)
	udkRe     = regexp.MustCompile(`УДК\s*([0-9][0-9.:\s()"-]*[0-9)])`)
	// признаки карточки: каждый — очко; два и больше — это карточка
	cardMarks = []*regexp.Regexp{
		regexp.MustCompile(`(?:[.,]\s*[—–]|[—–-]{2,})\s*[А-ЯЁ][а-яё-]*\.?\s*:`), // «.— М.:», «--- М.:»
		regexp.MustCompile(`\d+\s*[сеc]\.\s*(?::|$|[;.—])`),                     // «352 с.:»
		regexp.MustCompile(`ББК|УДК`),
		regexp.MustCompile(`(?i)/\s*(?:пер|общ|под|ред|сост|авт)`), // «/Общ. ред.»
		regexp.MustCompile(`(?i)ISBN|` + isbnLabel + `\s*:?\s*[\dXХ]`), // искажённая подпись — только с номером за ней
		regexp.MustCompile(`(?m)^(?:[А-ЯЁ]\s?|[а-яё])\d{1,3}\s+\S`), // «Х 24 Операция…», «т20 Талейран…»
	}
)

// IsCard — похожа ли страница на каталожную карточку.
func IsCard(lines []string) bool {
	text := strings.Join(lines, "\n")
	n := 0
	for _, m := range cardMarks {
		if m.MatchString(text) {
			n++
		}
	}
	return n >= 2
}

// parseCard раскладывает каталожную карточку.
func parseCard(lines []Line, d *Draft) {
	// описание начинается строкой с авторским знаком и текстом после него; знак без
	// текста стоит отдельно под ББК — это не начало
	start := -1
	for i, l := range lines {
		if m := signRe.FindStringSubmatch(l.Text); m != nil && len([]rune(l.Text)) > len([]rune(m[0]))+10 {
			start = i
			letter, num := m[1], m[2]
			if letter == "" { // строчная: распознавание уменьшило заглавную
				letter, num = strings.ToUpper(m[3]), m[4]
			}
			d.AuthorSign = letter + " " + num
			break
		}
	}
	if start < 0 { // знака нет — от строки с городом и издательством вверх, пока строки идут подряд
		for i, l := range lines {
			if cardMarks[0].MatchString(l.Text) {
				start = i
				for start > 0 && adjacent(lines[start-1], lines[start], lines[start].Height) {
					start--
				}
				break
			}
		}
	}
	if start < 0 {
		return
	}
	end := start
	for seen := false; end < len(lines); end++ {
		seen = seen || cardMarks[0].MatchString(lines[end].Text)
		if seen && pagesRe.MatchString(lines[end].Text) || end+1 < len(lines) && !adjacent(lines[end], lines[end+1], lines[end].Height) {
			break
		}
	}
	end = min(end, len(lines)-1)
	var desc []string
	for _, l := range lines[start : end+1] {
		desc = append(desc, l.Text)
	}
	text := joinWrapped(desc)
	if m := signRe.FindString(text); m != "" {
		text = strings.TrimLeft(strings.TrimPrefix(text, m), " —–-")
	}

	// автор — заголовок над описанием: «Хастингс М.»
	if start > 0 {
		if names := parseNames(strings.TrimRight(lines[start-1].Text, ".") + "."); len(names) > 0 && !strings.ContainsAny(lines[start-1].Text, "0123456789") {
			d.Authors = names
		}
	}

	areas := areaRe.Split(text, -1)
	titleArea, resp, _ := strings.Cut(areas[0], "/")
	parts := regexp.MustCompile(`\s*:\s+|\s*:\s*$`).Split(strings.TrimSpace(titleArea), -1)
	var subs []string
	for i, p := range parts {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
		case i == 0:
			d.Title = p
		case langRe.MatchString(p):
			d.OrigLanguage = language(p)
		default:
			subs = append(subs, p)
		}
	}
	d.Subtitle = strings.Join(subs, ": ")
	people, authors := roleNames(resp)
	d.People = append(d.People, people...)
	if len(d.Authors) == 0 {
		d.Authors = append(d.Authors, authors...)
	}
	if d.OrigLanguage == "" {
		if m := langRe.FindString(resp); m != "" {
			d.OrigLanguage = language(m)
		}
	}
	for _, a := range areas[1:] {
		if m := imprintRe.FindStringSubmatch(a); m != nil && d.Year == 0 {
			if c, ok := cities[fold(strings.TrimSpace(m[1]))]; ok {
				d.City = c
			} else {
				d.City = strings.TrimSpace(m[1])
			}
			d.Publisher = strings.Trim(strings.TrimSpace(m[2]), "«»\"")
			d.Year, _ = strconv.Atoi(m[3])
		} else if m := pagesRe.FindStringSubmatch(a); m != nil && d.Pages == 0 {
			d.Pages, _ = strconv.Atoi(m[1]) // «000 с.» — заглушка до вёрстки: Atoi даст 0, и поле останется пустым
		}
	}

	// аннотация — следующий абзац, до шифров и выходных цифр
	var ann []string
	for i := end + 1; i < len(lines); i++ {
		l := lines[i]
		if bbkRe.MatchString(l.Text) || udkRe.MatchString(l.Text) || isbnRe.MatchString(l.Text) || digitShare(l.Text) > 0.4 {
			if len(ann) == 0 {
				continue // шифры и номер бывают и между описанием и аннотацией (оборот Тарле, 1993)
			}
			break
		}
		if len(ann) > 0 && !adjacent(lines[i-1], l, l.Height*2) {
			break
		}
		// аннотация начинается абзацем; короткая строка или «© Издательство, 1986» после
		// шифров — уже выходные сведения, а не аннотация
		if len(ann) == 0 && (len([]rune(l.Text)) < 50 || strings.HasPrefix(strings.TrimSpace(l.Text), "©")) {
			break
		}
		ann = append(ann, l.Text)
	}
	d.Annotation = joinWrapped(ann)
}

// extras — то, что ищется по всей странице в любом режиме: ISBN и шифры.
func extras(lines []string, d *Draft) {
	text := strings.Join(lines, "\n")
	for _, m := range isbnRe.FindAllStringSubmatch(text, -1) {
		n := strings.NewReplacer("Х", "X", " ", "", "—", "-", "–", "-").Replace(m[1])
		d.ISBN = append(d.ISBN, n)
	}
	if m := bbkRe.FindStringSubmatch(text); m != nil {
		d.BBK = strings.Join(strings.Fields(m[1]), "")
	}
	if m := udkRe.FindStringSubmatch(text); m != nil {
		d.UDK = strings.Join(strings.Fields(m[1]), "")
	}
}

func digitShare(s string) float64 {
	n, all := 0, 0
	for _, r := range s {
		if r == ' ' {
			continue
		}
		all++
		if r >= '0' && r <= '9' {
			n++
		}
	}
	if all == 0 {
		return 0
	}
	return float64(n) / float64(all)
}

// --- участники: «Пер. с англ. Е. М. Федотова», «Общ. ред. и предисл. О. А. Ржешевского» ---

// Person — участник издания не-автор: переводчик, редактор, составитель, художник.
type Person struct {
	Name string `json:"name"` // «Федотов Е. М.» — как в каталоге: фамилия, инициалы
	Role string `json:"role"` // translator, editor, compiler, illustrator
}

var (
	// роли; предисловие, вступительная статья, комментарии — тоже слова-разделители, но без своей роли
	roleWordRe = regexp.MustCompile(`(?i)(перевод\S*|пер\.|общая\s+редакция|общ\.\s*ред\.|под\s+(?:общей\s+)?ред\S*|отв\.\s*ред\.|редакция|редактор\S*|ред\.|составлени\S*|составител\S*|сост\.|художник\S*|худож\.|иллюстрации|рисунки|рис\.|предисл\S*|вступ\S*|коммент\S*|примеч\S*|послесл\S*)`)
	nameRe     = regexp.MustCompile(`((?:[А-ЯЁ]\.\s?){1,2})\s?([А-ЯЁ][а-яё]+(?:-[А-ЯЁ][а-яё]+)?)`)
	// обратный порядок, как на титулах: «генерал-майора МИЛЬШТЕЙНА М. А.», «Петрова И. С.» —
	// фамилия (бывает капсом) перед инициалами
	surnameFirstRe = regexp.MustCompile(`([А-ЯЁ](?:[а-яё]+|[А-ЯЁ]+)(?:-[А-ЯЁ](?:[а-яё]+|[А-ЯЁ]+))?)\s+((?:[А-ЯЁ]\.\s?){1,2})`)
	langRe         = regexp.MustCompile(`(?i)(?:перевод|пер\.)\s*с\s*[а-яё]+\.?`)
)

// roleNames разбирает текст об участниках на людей по ролям. Имена в начале, до первого
// слова-роли и до «;» (после косой черты: «/ И. Ильф, Е. Петров»), — авторы.
// «Общ. ред. и предисл. О. А. Ржешевского»: у предисловия своей роли нет, и имя после
// него достаётся редакции, если у той имени не было.
func roleNames(s string) (people []Person, authors []string) {
	locs := roleWordRe.FindAllStringIndex(s, -1)
	head := s
	if len(locs) > 0 {
		head = s[:locs[0][0]]
	}
	head, _, _ = strings.Cut(head, ";")
	for _, m := range nameRe.FindAllStringSubmatch(head, -1) {
		authors = append(authors, person(m, false))
	}
	prev, prevGen, prevEmpty := "", false, false
	for i, loc := range locs {
		to := len(s)
		if i+1 < len(locs) {
			to = locs[i+1][0]
		}
		role, genitive := roleOf(strings.ToLower(s[loc[0]:loc[1]]))
		seg := s[loc[1]:to]
		if role == "" {
			// «и предисл.» сразу за ролью без имени — имя этой роли; через «;» — уже другое
			if !prevEmpty || strings.Contains(s[locs[i-1][1]:loc[0]], ";") {
				prev, prevEmpty = "", false
				continue
			}
			role, genitive = prev, prevGen
		}
		names := segmentNames(seg)
		for _, m := range names {
			people = append(people, Person{Name: person(m, genitive), Role: role})
		}
		prev, prevGen, prevEmpty = role, genitive, len(names) == 0
	}
	return people, authors
}

// roleOf: роль и падеж имени после слова. «Перевод Федотова», «под ред. Ржешевского» — родительный;
// «Редактор Забелок», «Сост. И. Петров», «Отв. ред. И. Петров» — именительный.
func roleOf(w string) (role string, genitive bool) {
	switch {
	case strings.HasPrefix(w, "предисл"), strings.HasPrefix(w, "вступ"), strings.HasPrefix(w, "коммент"),
		strings.HasPrefix(w, "примеч"), strings.HasPrefix(w, "послесл"):
		return "", false
	case strings.HasPrefix(w, "пер"):
		return "translator", true
	case strings.HasPrefix(w, "сост"):
		return "compiler", strings.HasPrefix(w, "составлени")
	case strings.HasPrefix(w, "худож"):
		return "illustrator", false
	case strings.HasPrefix(w, "иллюстр"), strings.HasPrefix(w, "рис"):
		return "illustrator", true
	case strings.HasPrefix(w, "редактор"), strings.HasPrefix(w, "отв"):
		return "editor", false
	default: // общая редакция, общ. ред., под ред., редакция
		return "editor", true
	}
}

// segmentNames — имена в куске после слова-роли: сначала обычный порядок «И. О. Фамилия»;
// нет ни одного — обратный «ФАМИЛИЯ И. О.». Оба порядка в одном куске не смешиваем: в «Е. М.
// Федотова, И. С. Петрова» обратный порядок нашёл бы ложное «Федотова, И. С.».
// Возвращает пары как у nameRe: [1] — инициалы, [2] — фамилия.
func segmentNames(seg string) [][]string {
	if names := nameRe.FindAllStringSubmatch(seg, -1); len(names) > 0 {
		return names
	}
	var out [][]string
	for _, m := range surnameFirstRe.FindAllStringSubmatch(seg, -1) {
		out = append(out, []string{m[0], m[2], titleCase(m[1])})
	}
	return out
}

// titleCase — фамилия, набранная капсом: «МИЛЬШТЕЙНА» → «Мильштейна», «РИМСКИЙ-КОРСАКОВ» →
// «Римский-Корсаков». Обычную («Петрова») не трогает.
func titleCase(s string) string {
	if s != strings.ToUpper(s) {
		return s
	}
	parts := strings.Split(strings.ToLower(s), "-")
	for i, p := range parts {
		r := []rune(p)
		if len(r) > 0 {
			r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		}
		parts[i] = string(r)
	}
	return strings.Join(parts, "-")
}

func person(m []string, genitive bool) string {
	surname := m[2]
	if genitive {
		surname = nominative(surname)
	}
	return surname + " " + strings.TrimSpace(spaceInitials(strings.ReplaceAll(m[1], " ", "")))
}

// nominative — фамилия из родительного падежа в именительный по самым частым окончаниям.
// Остальное оставляет как есть: неясный случай человек поправит, а неверно склонённую
// фамилию не отличить от верной.
func nominative(s string) string {
	for _, r := range []struct{ from, to string }{
		{"ского", "ский"}, {"цкого", "цкий"},
		{"овой", "ова"}, {"евой", "ева"}, {"иной", "ина"}, {"ыной", "ына"},
		{"ова", "ов"}, {"ева", "ев"}, {"ёва", "ёв"}, {"ина", "ин"}, {"ына", "ын"},
	} {
		if strings.HasSuffix(s, r.from) && len([]rune(s)) > len([]rune(r.from))+1 {
			return strings.TrimSuffix(s, r.from) + r.to
		}
	}
	// мужская фамилия на согласную: «Мильштейна» → «Мильштейн», «Шварца» → «Шварц». Женская в
	// родительном кончается на «-ой», несклоняемая «а» после согласной — редкость у коротких
	// («Дюма»): правило — только для фамилий от пяти букв
	if r := []rune(s); len(r) >= 5 && r[len(r)-1] == 'а' && !strings.ContainsRune("аеёиоуыэюяйь", r[len(r)-2]) {
		return string(r[:len(r)-1])
	}
	return s
}

// language — «Пер. с англ.», «Перевод с английского» → «английский».
func language(s string) string {
	w := strings.ToLower(s)
	if i := strings.LastIndex(w, " с "); i >= 0 {
		w = w[i+len(" с "):]
	} else if i := strings.LastIndex(w, "с"); i >= 0 {
		w = w[i+len("с"):]
	}
	w = strings.TrimSpace(strings.TrimRight(w, ". "))
	for _, l := range []struct{ prefix, name string }{
		{"англ", "английский"}, {"нем", "немецкий"}, {"фр", "французский"}, {"ит", "итальянский"},
		{"исп", "испанский"}, {"польск", "польский"}, {"чеш", "чешский"}, {"яп", "японский"},
		{"кит", "китайский"}, {"латыш", "латышский"}, {"лат", "латинский"}, {"лит", "литовский"},
		{"греч", "греческий"}, {"др.-греч", "древнегреческий"}, {"швед", "шведский"}, {"норв", "норвежский"},
		{"венг", "венгерский"}, {"болг", "болгарский"}, {"рум", "румынский"}, {"укр", "украинский"},
		{"бел", "белорусский"}, {"араб", "арабский"}, {"перс", "персидский"}, {"тур", "турецкий"},
		{"дат", "датский"}, {"голл", "нидерландский"}, {"нидерл", "нидерландский"}, {"фин", "финский"},
		{"эст", "эстонский"}, {"серб", "сербскохорватский"}, {"порт", "португальский"},
	} {
		if strings.HasPrefix(w, l.prefix) {
			return l.name
		}
	}
	return ""
}
