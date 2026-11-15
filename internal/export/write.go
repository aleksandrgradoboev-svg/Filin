package export

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
)

// JSON пишет каталог целиком, с отступами: файл человек тоже может открыть и прочитать.
func JSON(w io.Writer, lib *Library) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // «<» в аннотации — это «<», а не <
	return enc.Encode(lib)
}

// FileName — имя файла выгрузки: filin-catalog-2026-11-15.csv. Латиницей — заголовок
// Content-Disposition с кириллицей браузеры понимают по-разному.
func FileName(now time.Time, ext string) string {
	return "filin-catalog-" + now.Format("2006-01-02") + "." + ext
}

// csvHeader — колонки таблицы. Строка таблицы — экземпляр: так считают книги на полке.
// Издание без экземпляров — одна строка с пустыми колонками экземпляра.
var csvHeader = []string{
	"Издание №", "Экземпляр №", "Авторы", "Название", "Том", "Издательство", "Город", "Год", "ISBN",
	"Серия", "Номер в серии", "Собрания", "Жанры", "Темы",
	"Редакторы", "Переводчики", "Художники", "Составители",
	"Страниц", "Обложка", "Формат", "Тираж", "Язык", "Переведено с", "Название оригинала",
	"Полка", "Состояние", "Состояние подробно", "Переплёт", "Прочитана", "Дорожу", "Не выдавать",
	"На руках у", "Выдана", "Срок возврата",
	"Оценка, ₽", "Оценка до, ₽", "Дата оценки",
	"Экслибрис", "Надписи", "История экземпляра", "Заметки к изданию", "Заметки к экземпляру", "Аннотация",
}

// utf8BOM — метка порядка байтов UTF-8; байтами, а не знаком: знак в исходнике Go не пропускает.
const utf8BOM = "\xEF\xBB\xBF"

// CSV пишет таблицу для Excel: UTF-8 с меткой BOM и разделитель «;» — так русский Excel
// открывает файл двойным щелчком без кракозябр и без мастера импорта.
func CSV(w io.Writer, lib *Library) error {
	if _, err := io.WriteString(w, utf8BOM); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.UseCRLF = true
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	for _, b := range lib.Books {
		copies := b.Copies
		if len(copies) == 0 {
			copies = []Copy{{}} // издание без экземпляров — тоже строка
		}
		for _, c := range copies {
			if err := cw.Write(cells(row(b, c))); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}

func row(b Book, c Copy) []string {
	byRole := map[string][]string{}
	for _, p := range b.People {
		byRole[p.Role] = append(byRole[p.Role], p.Name)
	}
	var isbns []string
	for _, n := range b.ISBNs {
		isbns = append(isbns, n.ISBN)
	}
	var series, seriesNo string
	if b.Series != nil {
		series, seriesNo = b.Series.Title, b.Series.Number
	}
	var sets []string
	for _, s := range b.Sets {
		sets = append(sets, s.Set+", "+s.Place)
	}
	r := []string{
		num(int(b.ID)), num(int(c.ID)), strings.Join(byRole["author"], ", "), b.Title, b.Volume, b.Publisher,
		b.City, num(b.Year), strings.Join(isbns, ", "),
		series, seriesNo, strings.Join(sets, "; "), strings.Join(b.Genres, "; "), strings.Join(b.Subjects, "; "),
		strings.Join(byRole["editor"], ", "), strings.Join(byRole["translator"], ", "),
		strings.Join(byRole["illustrator"], ", "), strings.Join(byRole["compiler"], ", "),
		num(b.Pages), b.CoverType, b.Format, num(b.Circulation), b.Language, b.OrigLanguage, b.OrigTitle,
	}
	var shelf string
	if c.Shelf != nil {
		shelf = joinNonEmpty(" / ", c.Shelf.Room, c.Shelf.Bookcase, c.Shelf.Shelf)
	}
	var borrower, lentOn, dueOn string
	// текущая — невозвращённая (она одна: индекс loan_active), а не первая по дате: выдачу
	// можно записать задним числом раньше уже вернувшейся
	for _, l := range c.Loans {
		if l.ReturnedOn == "" {
			borrower, lentOn, dueOn = l.Borrower, l.LentOn, l.DueOn
			break
		}
	}
	var amount, amountTo, valuedOn string
	if len(c.Valuations) > 0 { // действует последняя по дате — она первая (тот же порядок, что currentValuation в catalog)
		v := c.Valuations[0]
		amount, amountTo, valuedOn = strconv.Itoa(v.Amount), num(v.AmountTo), v.ValuedOn
	}
	return append(r,
		shelf, c.GradeName, c.Condition, c.Binding, yes(c.Read), yes(c.Cherished), yes(c.NoLend),
		borrower, lentOn, dueOn,
		amount, amountTo, valuedOn,
		c.Bookplate, c.Inscriptions, c.History, b.Notes, c.Notes, b.Annotation,
	)
}

// cells обезвреживает ячейки, которые Excel принял бы за формулу: «=», «+», «-», «@»
// в начале. Данные приходят и с чужих сайтов (РГБ, alib), а формула в ячейке исполняется
// при открытии. Апостроф впереди делает ячейку текстом; в таблице он останется виден —
// это цена: заметка «-- см. т. 2» лучше с апострофом, чем исполненная формула.
func cells(r []string) []string {
	for i, s := range r {
		if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
			r[i] = "'" + s
		}
	}
	return r
}

func num(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func yes(b bool) string {
	if b {
		return "да"
	}
	return ""
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
