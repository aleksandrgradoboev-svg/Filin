package catalog

import (
	"errors"
	"testing"
)

func TestValuationsAndSummary(t *testing.T) {
	s := newStore(t)
	eid, a := lentCopy(t, s, "Собрание Дюма, т. 1")
	_, b := lentCopy(t, s, "Макулатурный Дюма")
	_, _ = lentCopy(t, s, "Без оценки")

	for _, v := range []Valuation{
		{CopyID: a, Amount: 800, ValuedOn: "2026-01-10", Basis: "старая оценка"},
		{CopyID: a, Amount: 1500, ValuedOn: "2026-10-30", Basis: "три предложения на alib.ru",
			SourceURL: "https://www.alib.ru/"},
		{CopyID: b, Amount: 100, ValuedOn: "2026-10-01"},
	} {
		if _, err := s.AddValuation(&v); err != nil {
			t.Fatal(err)
		}
	}
	cs, err := s.Copies(eid)
	if err != nil {
		t.Fatal(err)
	}
	if cs[0].Value == nil || cs[0].Value.Amount != 1500 {
		t.Fatalf("действующая оценка — последняя по дате: %+v", cs[0].Value)
	}
	if hist, _ := s.CopyValuations(a); len(hist) != 2 || hist[1].Amount != 800 {
		t.Errorf("прежняя оценка должна сохраниться: %+v", hist)
	}

	c := cs[0].Copy
	c.Grade, c.Cherished = 4, true
	if err := s.UpdateCopy(&c); err != nil {
		t.Fatal(err)
	}
	sum, err := s.ValueSummary()
	if err != nil {
		t.Fatal(err)
	}
	if sum != (ValueSummary{Total: 1600, TotalTo: 1600, Valued: 2, Copies: 3, Cherished: 1}) {
		t.Errorf("итог: %+v", sum)
	}
	top, err := s.MostValuable(10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].Amount != 1500 || top[0].Grade != 4 || !top[0].Cherished {
		t.Errorf("самое ценное: %+v", top)
	}
	if ch, _ := s.MostValuable(10, true); len(ch) != 1 || ch[0].CopyID != a {
		t.Errorf("«дорожу»: %+v", ch)
	}

	for _, bad := range []Valuation{
		{CopyID: b, Amount: -1},
		{CopyID: b, Amount: 1, ValuedOn: "2999-01-01"},
		{CopyID: b, Amount: 1, SourceURL: "javascript:alert(1)"},
	} {
		if _, err := s.AddValuation(&bad); !IsInput(err) {
			t.Errorf("%+v: ждали ошибку ввода, получено %v", bad, err)
		}
	}
	// ошибочную оценку можно убрать — действующей становится предыдущая
	if _, err := s.DeleteValuation(cs[0].Value.ID); err != nil {
		t.Fatal(err)
	}
	if cs, _ = s.Copies(eid); cs[0].Value.Amount != 800 {
		t.Errorf("после удаления действует %+v", cs[0].Value)
	}
	if _, err := s.DeleteValuation(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("удаление несуществующей: %v", err)
	}
	if GradeName(5) != "отличное" || GradeName(0) != "" || GradeName(9) != "" {
		t.Error("шкала состояния")
	}
}

// Оценка диапазоном «от – до»: сумма по библиотеке считается по обеим границам,
// «от 500 до 500» хранится точной суммой, перевёрнутый диапазон не принимается.
func TestValuationRange(t *testing.T) {
	s := newStore(t)
	eid, a := lentCopy(t, s, "Оверлорд")
	_, b := lentCopy(t, s, "Точная сумма")
	_, c := lentCopy(t, s, "Ровный диапазон")
	for _, v := range []Valuation{
		{CopyID: a, Amount: 170, AmountTo: 450, Basis: "по трём предложениям alib.ru"},
		{CopyID: b, Amount: 1000},
		{CopyID: c, Amount: 500, AmountTo: 500},
	} {
		if _, err := s.AddValuation(&v); err != nil {
			t.Fatal(err)
		}
	}
	cs, err := s.Copies(eid)
	if err != nil {
		t.Fatal(err)
	}
	if v := cs[0].Value; v == nil || v.Amount != 170 || v.AmountTo != 450 {
		t.Fatalf("диапазон в карточке: %+v", v)
	}
	if hist, _ := s.CopyValuations(c); len(hist) != 1 || hist[0].AmountTo != 0 {
		t.Errorf("«от 500 до 500» должно стать точной суммой: %+v", hist)
	}
	sum, err := s.ValueSummary()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 170+1000+500 || sum.TotalTo != 450+1000+500 {
		t.Errorf("итог: от %d до %d", sum.Total, sum.TotalTo)
	}
	top, err := s.MostValuable(10, false)
	if err != nil {
		t.Fatal(err)
	}
	// по середине: 1000 > 500 > (170+450)/2 = 310
	if len(top) != 3 || top[0].Amount != 1000 || top[1].Amount != 500 || top[2].AmountTo != 450 {
		t.Errorf("порядок ценного: %+v", top)
	}
	if _, err := s.AddValuation(&Valuation{CopyID: a, Amount: 450, AmountTo: 170}); !IsInput(err) {
		t.Errorf("перевёрнутый диапазон принят: %v", err)
	}
	// база держит то же правило сама: в обход проверки Go не записать
	if _, err := s.db.Exec(`INSERT INTO valuation (copy_id, amount, amount_to, valued_on) VALUES (?, 450, 170, '2026-11-08')`, a); err == nil {
		t.Error("база приняла перевёрнутый диапазон")
	}
}

// Шкала и пояснения к ней — один список ступеней: пояснение без ступени или ступень без
// пояснения уронили бы форму экземпляра.
func TestGradeHintsMatchGrades(t *testing.T) {
	if len(GradeHints) != len(Grades) {
		t.Fatalf("ступеней %d, пояснений %d", len(Grades), len(GradeHints))
	}
	for i := 1; i < len(Grades); i++ {
		if Grades[i] == "" || GradeHints[i] == "" {
			t.Errorf("ступень %d без названия или пояснения", i)
		}
	}
	if GradeName(6) != "идеальное" || GradeName(1) != "плохое" || GradeName(7) != "" {
		t.Error("шкала alib: 1 — плохое, 6 — идеальное, 7 — вне шкалы")
	}
}
