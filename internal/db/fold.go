package db

import (
	"database/sql/driver"
	"strings"
	"unicode"

	"modernc.org/sqlite"
)

// oldLetters — буквы дореформенной орфографии (до 1918 г.) и их современная замена.
// «ё» — не старая орфография, но в поиске её так же не различают.
var oldLetters = map[rune]rune{'ё': 'е', 'ѣ': 'е', 'і': 'и', 'ѳ': 'ф', 'ѵ': 'и'}

// Fold приводит текст к виду для поиска: нижний регистр, «ё» как «е» и старая орфография
// как современная — ѣ→е, і→и, ѳ→ф, ѵ→и, твёрдый знак на конце слова отбрасывается
// («Повѣсти Бѣлкина», «Миръ» находятся по «повести белкина», «мир»; ъ внутри слова —
// «объём» — остаётся). Окончания «-аго/-яго» не трогаются: «Живаго» — фамилия, а не
// старая форма «живого».
// Встроенные lower() и LIKE в SQLite знают регистр только у латиницы, а каталог русский.
// Индексов на fold() нет сознательно (миграция 0005): правило можно менять, база не портится.
func Fold(s string) string {
	rs := []rune(strings.ToLower(s))
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range rs {
		if m, ok := oldLetters[r]; ok {
			r = m
		}
		if r == 'ъ' && (i+1 == len(rs) || !unicode.IsLetter(rs[i+1])) {
			continue // «миръ», «онъ.» — конечный ъ ничего не значит
		}
		b.WriteRune(r)
	}
	return b.String()
}

// В SQL та же функция доступна как fold(текст). Регистрируется на драйвере один раз
// и действует на все соединения, открытые после этого.
func init() {
	err := sqlite.RegisterDeterministicScalarFunction("fold", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			switch v := args[0].(type) {
			case string:
				return Fold(v), nil
			case []byte:
				return Fold(string(v)), nil
			default:
				return v, nil
			}
		})
	if err != nil {
		panic(err)
	}
}
