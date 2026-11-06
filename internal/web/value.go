package web

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// rub показывает сумму с пробелами между разрядами: 12 500 ₽.
func rub(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(rune(0x202F)) // узкий неразрывный пробел — сумма не рвётся на две строки
		}
		b.WriteRune(c)
	}
	return b.String() + string(rune(0x00A0)) + "₽"
}

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
	amount, err := parseRub(r.FormValue("amount"))
	if err != nil {
		retry("сумма — число рублей, например 1500")
		return
	}
	v := catalog.Valuation{CopyID: id, Amount: amount, ValuedOn: r.FormValue("valued_on"),
		Basis: r.FormValue("basis"), SourceURL: r.FormValue("source_url")}
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
	s.render(w, "value", p)
}
