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
	if sum != (ValueSummary{Total: 1600, Valued: 2, Copies: 3, Cherished: 1}) {
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
