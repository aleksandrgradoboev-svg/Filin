package db

import (
	"database/sql/driver"
	"strings"

	"modernc.org/sqlite"
)

// Fold приводит текст к виду для поиска: нижний регистр и «ё» как «е».
// Встроенные lower() и LIKE в SQLite знают регистр только у латиницы, а каталог русский.
func Fold(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "ё", "е")
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
