package web

import (
	"encoding/base64"
	"html/template"
	"net/url"

	"rsc.io/qr"
)

// QR-коды страницы «Телефоны»: человек ставит приложение и привязывает телефон камерой,
// не набирая адрес вида http://192.168.1.10:8080 руками.

// PairScheme — начало текста QR привязки; приложение узнаёт свой код по нему.
const PairScheme = "filin:pair"

// pairText — что зашито в QR привязки: адрес Филина и одноразовый код.
func pairText(server, code string) string {
	return PairScheme + "?" + url.Values{"server": {server}, "code": {code}}.Encode()
}

// qrImage — QR-картинка для <img src>: PNG прямо в странице, без отдельного адреса и без
// хранения. Ошибка кодирования — пустая строка: страница покажет адрес и код текстом.
func qrImage(text string) template.URL {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	c.Scale = 6
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(c.PNG()))
}
