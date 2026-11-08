package paddle

import "testing"

// Строки — настоящий вывод модели на снимках корпуса.
func TestCyrillic(t *testing.T) {
	for in, want := range map[string]string{
		"MOCKBA-1958":            "МОСКВА-1958",
		"COJДAT":                 "СОJДАТ", // J двойника нет — остаётся, остальное русское
		"BBK 63.3(0)62":          "ББК 63.3(0)62",
		"YDK 94:355:359":         "УДК 94:355:359",
		"X 24":                   "Х 24",
		"Tom; E. B. Tаpле":       "Tom; Е. В. Тарле",
		"ISBN 978-5-600-01715-3": "ISBN 978-5-600-01715-3",
		"Operation Overlord":     "Operation Overlord",
		"Москва 1965 Ленинград":  "Москва 1965 Ленинград",
		"ВальтерGкотm":           "ВальтерGкотm",
		"PykA":                   "РукА",
	} {
		if got := cyrillic(in); got != want {
			t.Errorf("cyrillic(%q) = %q, ждали %q", in, got, want)
		}
	}
}
