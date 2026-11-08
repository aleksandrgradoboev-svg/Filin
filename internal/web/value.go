package web

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// digits — число с узкими неразрывными пробелами между разрядами: 12 500.
func digits(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(rune(0x202F)) // узкий неразрывный пробел — сумма не рвётся на две строки
		}
		b.WriteRune(c)
	}
	return b.String()
}

const nbsp = " "

// gradeStep — ступень шкалы состояния для формы.
type gradeStep struct {
	N          int
	Name, Hint string
}

// gradeScale — шкала сверху вниз, как её пишет alib: от «идеального» к «плохому».
func gradeScale() []gradeStep {
	var out []gradeStep
	for n := len(catalog.Grades) - 1; n >= 1; n-- {
		out = append(out, gradeStep{n, catalog.Grades[n], catalog.GradeHints[n]})
	}
	return out
}

// rub показывает сумму: 12 500 ₽.
func rub(n int) string { return digits(n) + nbsp + "₽" }

var (
	reRubKop   = regexp.MustCompile(`^(\d+)(?:[.,]\d{1,2})?$`)        // 1500, 1500,50 — копейки отбрасываем
	reRubGroup = regexp.MustCompile(`^\d{1,3}(?:[.,]\d{3})+$`)        // 1.500, 12,500 — разделитель разрядов
	reRubJunk  = regexp.MustCompile(`[\s\x{00A0}\x{202F}₽рубРУБ.]*$`) // «₽», «руб.» в конце
)

// parseRub понимает сумму, как её пишут руками: «1500», «1 500 ₽», «1.500 руб.», «1500,50».
// Точка и запятая — то копейки, то разряды: решает число цифр после них.
func parseRub(s string) (int, error) {
	s = reRubJunk.ReplaceAllString(strings.TrimSpace(s), "")
	s = strings.NewReplacer(" ", "", string(rune(0x00A0)), "", string(rune(0x202F)), "").Replace(s)
	switch {
	case reRubGroup.MatchString(s):
		s = strings.NewReplacer(".", "", ",", "").Replace(s)
	case reRubKop.MatchString(s):
		s = reRubKop.FindStringSubmatch(s)[1]
	default:
		return 0, fmt.Errorf("не сумма: %q", s)
	}
	return strconv.Atoi(s)
}

// price — оценка для показа: «1 500 ₽» или диапазон «170 – 450 ₽».
func price(from, to int) string {
	if to <= from {
		return rub(from)
	}
	return digits(from) + nbsp + "–" + nbsp + rub(to)
}

// valuationForm — оценка из формы: сумма «от» обязательна, «до» — по желанию. Вторым
// значением — что сказать человеку, если форма заполнена не так.
func valuationForm(r *http.Request, copyID int64) (catalog.Valuation, string) {
	amount, err := parseRub(r.FormValue("amount"))
	if err != nil {
		return catalog.Valuation{}, "сумма — число рублей, например 1500"
	}
	var to int
	if s := strings.TrimSpace(r.FormValue("amount_to")); s != "" {
		if to, err = parseRub(s); err != nil {
			return catalog.Valuation{}, "«до» — число рублей, например 2500, или пусто"
		}
	}
	return catalog.Valuation{CopyID: copyID, Amount: amount, AmountTo: to, ValuedOn: r.FormValue("valued_on"),
		Basis: r.FormValue("basis"), SourceURL: r.FormValue("source_url")}, ""
}

func (s *server) addValuation(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.st.CopyByID(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	retry := func(msg string) {
		s.renderBook(w, bookPage{E: catalog.Edition{ID: c.EditionID}, ValueErr: msg, ValueCopy: id})
	}
	v, msg := valuationForm(r, id)
	if msg != "" {
		retry(msg)
		return
	}
	if _, err := s.st.AddValuation(&v); err != nil {
		if catalog.IsInput(err) {
			retry(err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/book/%d", c.EditionID), http.StatusSeeOther)
}

func (s *server) deleteValuation(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	copyID, err := s.st.DeleteValuation(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	c, err := s.st.CopyByID(copyID)
	if err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/book/%d", c.EditionID), http.StatusSeeOther)
}

type valuePage struct {
	Title     string
	Sum       catalog.ValueSummary
	Top       []catalog.ValueRow
	Cherished []catalog.ValueRow
	Sets      []setValue
}

func (s *server) value(w http.ResponseWriter, r *http.Request) {
	p := valuePage{Title: "Ценность"}
	var err error
	if p.Sum, err = s.st.ValueSummary(); err != nil {
		s.fail(w, err)
		return
	}
	if p.Top, err = s.st.MostValuable(50, false); err != nil {
		s.fail(w, err)
		return
	}
	if p.Cherished, err = s.st.MostValuable(200, true); err != nil {
		s.fail(w, err)
		return
	}
	if p.Sets, err = s.setValues(); err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "value", p)
}
