package isbn

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		raw   string
		norm  string
		valid bool
	}{
		{"978-0-306-40615-7", "9780306406157", true},
		{"ISBN 0-306-40615-2", "9780306406157", true}, // тот же номер в 10 цифрах
		{"0-8044-2957-x", "9780804429573", true},      // X в контрольной цифре
		// из плана: контрольная сумма не сходится — номер сохраняется, но помечается
		{"978-5-87399-050-6", "9785873990506", false},
		// неверный ISBN-10 не переводится в 13, иначе пересчёт спрятал бы ошибку
		{"0-306-40615-3", "0306406153", false},
		// подписи «ISBN-13:» и «ISBN 10» — не часть номера
		{"ISBN-13: 978-0-306-40615-7", "9780306406157", true},
		{"ISBN 10 0-306-40615-2", "9780306406157", true},
		{"isbn13 9780306406157", "9780306406157", true},
		// ISBN-10, начинающийся с 13, без разделителя после — это номер, а не подпись
		{"ISBN 1302345674", "1302345674", false},
		// кириллическая «Х» и длинное тире из русских текстов
		{"0-8044-2957-Х", "9780804429573", true},
		{"0—8044—2957—х", "9780804429573", true},
	}
	for _, c := range cases {
		got, err := Parse(c.raw)
		if err != nil {
			t.Fatalf("%q: %v", c.raw, err)
		}
		if got.Norm != c.norm || got.Valid != c.valid {
			t.Errorf("%q: получено %s/%v, ждали %s/%v", c.raw, got.Norm, got.Valid, c.norm, c.valid)
		}
	}
}

func TestParseRejects(t *testing.T) {
	// «отсутствует» и «отсутсвует» FantLab пишет прямо в поле ISBN
	for _, raw := range []string{"", "отсутствует", "отсутсвует", "12345", "97X0306406157", "978-0-306-40615-7-1"} {
		if _, err := Parse(raw); !errors.Is(err, ErrNotISBN) {
			t.Errorf("%q: ждали ErrNotISBN, получено %v", raw, err)
		}
	}
}

func TestList(t *testing.T) {
	// два ISBN соиздателей, «отсутствует» и битый номер в одной строке
	good, bad := List("ISBN 978-0-306-40615-7; 0-8044-2957-X, отсутствует, 978-5-1234")
	if len(good) != 2 || good[1].Norm != "9780804429573" {
		t.Fatalf("ждали два номера, получено %+v", good)
	}
	if len(bad) != 1 || bad[0] != "978-5-1234" {
		t.Fatalf("битый номер должен вернуться в bad, получено %q", bad)
	}
	if g, _ := List("0-306-40615-2 / 0-8044-2957-X"); len(g) != 2 {
		t.Fatalf("номера через косую черту: %+v", g)
	}
	if g, b := List("отсутсвует"); g != nil || b != nil {
		t.Fatalf("«отсутсвует» — не номер и не ошибка: %v %v", g, b)
	}
}

func TestIsBookland(t *testing.T) {
	if !IsBookland("9780306406157") || !IsBookland("9790060115615") {
		t.Error("978/979 — книжные")
	}
	if IsBookland("4607001771111") {
		t.Error("магазинный EAN-13 не книжный")
	}
}
