package ocr

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Draft — черновик карточки со страницы книги. Пустое поле — не нашлось, а не «нет у книги».
type Draft struct {
	Kind         string   `json:"kind"` // «title» — титул, «card» — каталожная карточка
	Title        string   `json:"title"`
	Subtitle     string   `json:"subtitle,omitempty"` // «роман», «Как был открыт второй фронт»
	Authors      []string `json:"authors"`
	People       []Person `json:"people"` // переводчики, редакторы, составители, художники
	Publisher    string   `json:"publisher"`
	City         string   `json:"city"`
	Year         int      `json:"year,omitempty"`
	Pages        int      `json:"pages,omitempty"`
	ISBN         []string `json:"isbn"` // как напечатано; номер проверяет сервер при сохранении
	BBK          string   `json:"bbk,omitempty"`
	UDK          string   `json:"udk,omitempty"`
	AuthorSign   string   `json:"author_sign,omitempty"`
	OrigLanguage string   `json:"orig_language,omitempty"`
	Annotation   string   `json:"annotation,omitempty"`
	Others       []string `json:"others"` // «Академия наук СССР» и прочее, что не разложилось, — подсказка человеку
	Lines        []string `json:"lines"`  // весь распознанный текст сверху вниз: свериться и дописать
}

const (
	KindTitle = "title"
	KindCard  = "card"
)

// Где начинаются выходные данные: ниже этой доли высоты страницы. Год ищется ниже середины.
const (
	imprintFrom = 0.72
	yearFrom    = 0.5
)

var (
	yearRe  = regexp.MustCompile(`(?:^|[^\p{L}\d])(1[5-9]\d\d|20[0-4]\d)(?:$|[^\p{L}\d])`)
	quoteRe = regexp.MustCompile(`[«"„“]([^»"”“]+)[»"”“]`)
	// слова, после которых строка — организация над названием, а не автор
	orgRe = regexp.MustCompile(`(?i)академи|институт|министерств|университет|библиотек|серия|союз|общество|комитет|ордена|народн|комиссариат|управлени|музей|архив|центр`)
	// строки под названием про участников и перевод — не подзаголовок
	roleRe = regexp.MustCompile(`(?i)^(перевод|пер\.|составител|сост\.|под\s+(общей\s+)?ред|редак|предислов|вступит|послеслов|коммент|примечан|иллюстр|рисунк|художник|оформлен)`)
	pubRe  = regexp.MustCompile(`(?i)издательств\S*|изд-во|издат\S*`)
)

// Города советских титулов, в том числе сокращения «М.» и «Л.». Сверка — по началу слова,
// без учёта регистра и «ё».
var cities = map[string]string{
	"москва": "Москва", "м.": "Москва", "ленинград": "Ленинград", "л.": "Ленинград",
	"санкт-петербург": "Санкт-Петербург", "петербург": "Санкт-Петербург", "спб": "Санкт-Петербург", "петроград": "Петроград",
	"киев": "Киев", "минск": "Минск", "харьков": "Харьков", "одесса": "Одесса", "львов": "Львов",
	"тбилиси": "Тбилиси", "ереван": "Ереван", "баку": "Баку", "ташкент": "Ташкент", "алма-ата": "Алма-Ата",
	"рига": "Рига", "таллин": "Таллин", "вильнюс": "Вильнюс", "кишинев": "Кишинёв", "фрунзе": "Фрунзе",
	"душанбе": "Душанбе", "ашхабад": "Ашхабад", "свердловск": "Свердловск", "екатеринбург": "Екатеринбург",
	"новосибирск": "Новосибирск", "горький": "Горький", "нижний": "Нижний Новгород", "куйбышев": "Куйбышев",
	"самара": "Самара", "казань": "Казань", "пермь": "Пермь", "уфа": "Уфа", "челябинск": "Челябинск",
	"омск": "Омск", "томск": "Томск", "иркутск": "Иркутск", "красноярск": "Красноярск", "барнаул": "Барнаул",
	"хабаровск": "Хабаровск", "владивосток": "Владивосток", "воронеж": "Воронеж", "саратов": "Саратов",
	"волгоград": "Волгоград", "сталинград": "Сталинград", "ростов-на-дону": "Ростов-на-Дону", "ростов": "Ростов-на-Дону",
	"краснодар": "Краснодар", "ярославль": "Ярославль", "тула": "Тула", "калинин": "Калинин", "тверь": "Тверь",
	"калининград": "Калининград", "архангельск": "Архангельск", "мурманск": "Мурманск", "петрозаводск": "Петрозаводск",
	"вологда": "Вологда", "смоленск": "Смоленск", "брянск": "Брянск", "курск": "Курск", "орел": "Орёл",
	"рязань": "Рязань", "иваново": "Иваново", "пенза": "Пенза", "ульяновск": "Ульяновск", "оренбург": "Оренбург",
	"чкалов": "Чкалов", "ижевск": "Ижевск", "киров": "Киров", "днепропетровск": "Днепропетровск", "донецк": "Донецк",
	"симферополь": "Симферополь", "севастополь": "Севастополь", "махачкала": "Махачкала", "грозный": "Грозный",
}

// сокращения, которые остаются заглавными при переводе строки из ВЕРХНЕГО регистра
var acronyms = map[string]bool{"СССР": true, "РСФСР": true, "УССР": true, "БССР": true, "АН": true, "КПСС": true,
	"ЦК": true, "ВЛКСМ": true, "ВКП(б)": true, "США": true, "ГДР": true, "ФРГ": true, "ОГИЗ": true, "ВЦСПС": true}

// Parse раскладывает распознанную страницу по полям: каталожную карточку — по её
// пунктуации, титул — по расположению строк.
func Parse(p Page) Draft {
	d := Draft{Kind: KindTitle, Authors: []string{}, People: []Person{}, ISBN: []string{}, Others: []string{}, Lines: []string{}}
	var lines []Line
	for _, l := range p.Lines {
		d.Lines = append(d.Lines, l.Text)
		if meaningful(l) {
			lines = append(lines, l)
		}
	}
	extras(d.Lines, &d)
	if len(lines) == 0 {
		return d
	}
	if IsCard(d.Lines) {
		d.Kind = KindCard
		parseCard(lines, &d)
		return d
	}
	parseTitle(p, lines, &d)
	return d
}

// parseTitle раскладывает титул: автор сверху, крупнее всего название, внизу город,
// издательство, год.
func parseTitle(p Page, lines []Line, d *Draft) {
	h := p.Height
	if h == 0 {
		last := lines[len(lines)-1]
		h = last.Top + last.Height
	}

	// выходные данные: снизу страницы и строка с годом, если она выше
	imprintTop := int(imprintFrom * float64(h))
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if l.Top < int(yearFrom*float64(h)) {
			break
		}
		if m := yearRe.FindStringSubmatch(l.Text); m != nil {
			d.Year, _ = strconv.Atoi(m[1])
			imprintTop = min(imprintTop, l.Top)
			break
		}
	}
	var body, imprint []Line
	for _, l := range lines {
		if l.Top >= imprintTop {
			imprint = append(imprint, l)
		} else {
			body = append(body, l)
		}
	}
	d.City, d.Publisher = parseImprint(imprint)

	if len(body) == 0 {
		return
	}
	// название — самые крупные буквы; продолжается соседними строками того же кегля
	ti := 0
	for i, l := range body {
		if l.Height > body[ti].Height {
			ti = i
		}
	}
	big := body[ti].Height
	from, to := ti, ti
	for from > 0 && sameSize(body[from-1], big) && adjacent(body[from-1], body[from], big) {
		from--
	}
	for to < len(body)-1 && sameSize(body[to+1], big) && adjacent(body[to], body[to+1], big) {
		to++
	}
	var title []string
	for _, l := range body[from : to+1] {
		title = append(title, l.Text)
	}
	d.Title = fixCase(joinWrapped(title))

	// над названием — авторы или организация
	for _, l := range body[:from] {
		if orgRe.MatchString(l.Text) {
			d.Others = append(d.Others, fixCase(l.Text))
			continue
		}
		if names := parseNames(l.Text); len(names) > 0 {
			d.Authors = append(d.Authors, names...)
		} else {
			d.Others = append(d.Others, fixCase(l.Text))
		}
	}
	// под названием — подзаголовок, затем участники: «Перевод с английского Е. М. Федотова»,
	// «Общая редакция и предисловие» + следующей строкой «доктора наук О. А. Ржешевского»
	var sub, roles []string
	var last Line
	inRoles := false
	for _, l := range body[to+1:] {
		inRoles = inRoles || roleRe.MatchString(l.Text)
		switch {
		case inRoles:
			roles = append(roles, l.Text)
		case len(sub) > 0 && !adjacent(last, l, l.Height):
			d.Others = append(d.Others, fixCase(l.Text))
		default:
			sub = append(sub, l.Text)
			last = l
		}
	}
	d.Subtitle = fixCase(joinWrapped(sub))
	text := strings.Join(roles, " ")
	people, _ := roleNames(text)
	d.People = append(d.People, people...)
	if m := langRe.FindString(text); m != "" {
		d.OrigLanguage = language(m)
	}
	if len(people) == 0 && text != "" {
		d.Others = append(d.Others, fixCase(text))
	}
}

// meaningful отсекает мусор распознавания: виньетки, полосы, пятна дают строки из
// одного-двух знаков или с низкой уверенностью.
func meaningful(l Line) bool {
	n := 0
	for _, r := range l.Text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n >= 2 && l.Conf >= 30
}

func sameSize(l Line, big int) bool { return float64(l.Height) >= 0.8*float64(big) }

// adjacent — строки идут подряд, без пустого места между ними больше полутора строк.
func adjacent(a, b Line, h int) bool { return b.Top-(a.Top+a.Height) < h*3/2 }

// joinWrapped склеивает строки, переносы «ВОСПОМИНА-» + «НИЯ» — без дефиса.
func joinWrapped(parts []string) string {
	var b strings.Builder
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if i > 0 {
			prev := b.String()
			if strings.HasSuffix(prev, "-") && !strings.HasSuffix(prev, " -") {
				s := strings.TrimSuffix(prev, "-")
				b.Reset()
				b.WriteString(s)
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteString(p)
	}
	return b.String()
}

// parseImprint вынимает город и издательство из нижних строк титула.
func parseImprint(lines []Line) (city, publisher string) {
	var rest []string
	for _, l := range lines {
		text := l.Text
		if m := quoteRe.FindStringSubmatch(text); m != nil && publisher == "" {
			publisher = fixCase(strings.TrimSpace(m[1]))
			text = strings.Replace(text, m[0], " ", 1)
		}
		var keep []string
		for _, w := range strings.Fields(text) {
			if c, ok := cityOf(strings.Trim(w, ",;:·—–-")); ok && city == "" {
				city = c
				continue
			}
			if c, ok := cities[fold(strings.TrimRight(w, ",;:·—–"))]; ok && city == "" { // «М.» — с точкой
				city = c
				continue
			}
			keep = append(keep, w)
		}
		text = yearRe.ReplaceAllString(strings.Join(keep, " "), " ")
		text = strings.Trim(strings.Join(strings.Fields(text), " "), " ,;:·—–-")
		if text != "" {
			rest = append(rest, text)
		}
	}
	if publisher != "" {
		return city, publisher
	}
	// «ИЗДАТЕЛЬСТВО АКАДЕМИИ НАУК СССР», «ВОЕНИЗДАТ»: строка со словом «издат», иначе первая оставшаяся
	for _, r := range rest {
		if pubRe.MatchString(r) {
			return city, fixCase(r)
		}
	}
	if len(rest) > 0 {
		return city, fixCase(rest[0])
	}
	return city, ""
}

// parseNames — строка над названием как список имён: «Е. В. ТАРЛЕ», «И. ИЛЬФ и Е. ПЕТРОВ».
// Не похожа на имена — пусто.
func parseNames(s string) []string {
	s = strings.ReplaceAll(s, " и ", ",")
	s = strings.ReplaceAll(s, " И ", ",")
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		words := strings.Fields(spaceInitials(part))
		if len(words) == 0 || len(words) > 4 {
			return nil
		}
		for _, w := range words {
			for _, r := range strings.TrimRight(w, ".") {
				if !unicode.IsLetter(r) && r != '-' {
					return nil
				}
			}
		}
		for i, w := range words {
			words[i] = capitalize(w)
		}
		out = append(out, strings.Join(words, " "))
	}
	return out
}

// spaceInitials: «Е.В.Тарле» → «Е. В. Тарле».
func spaceInitials(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		b.WriteRune(r)
		if r == '.' && i+1 < len(rs) && rs[i+1] != ' ' {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func capitalize(w string) string {
	rs := []rune(strings.ToLower(w))
	for i := range rs {
		if i == 0 || rs[i-1] == '-' {
			rs[i] = unicode.ToUpper(rs[i])
		}
	}
	return string(rs)
}

// fixCase переводит строку из ВЕРХНЕГО регистра в обычный, оставляя заглавными
// первую букву и сокращения вроде СССР. Строку в обычном регистре не трогает.
func fixCase(s string) string {
	if !shouting(s) {
		return s
	}
	words := strings.Fields(s)
	for i, w := range words {
		if acronyms[strings.Trim(w, ",.;:«»\"()")] {
			continue
		}
		words[i] = strings.ToLower(w)
	}
	out := strings.Join(words, " ")
	rs := []rune(out)
	for i, r := range rs {
		if unicode.IsLetter(r) {
			rs[i] = unicode.ToUpper(r)
			break
		}
	}
	return string(rs)
}

func shouting(s string) bool {
	up, low := 0, 0
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			up++
		case unicode.IsLower(r):
			low++
		}
	}
	return up > 1 && up > 3*low
}

// cityOf — город по слову выходных данных; в названии от пяти букв прощается одна неверная
// буква на своём месте: «МООКВА» (С прочиталась как О) — Москва. Короче — только точно:
// у «Киев» и «Львов» соседей на одну букву слишком много.
func cityOf(w string) (string, bool) {
	f := fold(w)
	if c, ok := cities[f]; ok {
		return c, true
	}
	r := []rune(f)
	if len(r) < 5 {
		return "", false
	}
	found := ""
	for k, c := range cities {
		kr := []rune(k)
		if len(kr) != len(r) {
			continue
		}
		diff := 0
		for i := range kr {
			if kr[i] != r[i] {
				diff++
			}
		}
		if diff == 1 {
			if found != "" && found != c {
				return "", false // похоже на два города — не угадываем
			}
			found = c
		}
	}
	return found, found != ""
}

func fold(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "ё", "е")
}
