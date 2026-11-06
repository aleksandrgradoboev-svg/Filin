package livelib

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"mime/quotedprintable"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/cover"
	"github.com/aleksandrgradoboev-svg/filin/internal/db"
)

// Разметка — как на полке LiveLib в режиме плитки, книги выдуманные.
const shelf = `<html><body>
<div class="brow book-item-manage" data-is-user-booklist-status="1"><div class="brow-inner"><div class="cover-wrapper rback"><span class="bg-book-text ub"><span>Мои книги</span></span><a href="https://www.livelib.ru/book/1001-mirovoj"><img class="cover-rounded" src="https://s1.livelib.ru/boocover/1001/120x180/a/x.jpg"></a></div>
<div class="swiper-book-data"><a class="swiper-book-name" href="https://www.livelib.ru/book/1001-mirovoj" title="Уинстон Черчилль - Мировой кризис. Часть 3 (конец)"> Мировой кризис. Часть 3 (ко... </a><br>
<div class="swiper-author"><a href="https://www.livelib.ru/author/1" title="Уинстон Черчилль">Уинстон Черчилль</a></div></div></div></div>
<div class="brow book-item-manage" data-is-user-booklist-status="1"><div class="brow-inner"><div class="cover-wrapper rback"><a href="https://www.livelib.ru/book/1002-shahmaty"><img class="cover-rounded" src="https://s1.livelib.ru/boocover/1002/120x180/b/y.jpg"></a></div>
<div class="swiper-book-data"><a class="swiper-book-name" href="https://www.livelib.ru/book/1002-shahmaty" title="Шахматные дебюты. Полный курс"> Шахматные дебюты. Полный к... </a><br>
<div class="swiper-author"><a href="/author/2">Я. Б. Эстрин</a>, <a href="/author/3">Н. М. Калиниче...</a>, <a href="#">Ещё 1</a></div></div></div></div>
</body></html>`

func mhtml(t *testing.T) []byte { return mhtmlPage(t, shelf) }

// mhtmlPage — страница page, сохранённая браузером одним файлом, с одной картинкой.
func mhtmlPage(t *testing.T, page string) []byte {
	t.Helper()
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 120, 180))); err != nil {
		t.Fatal(err)
	}
	var qp bytes.Buffer
	w := quotedprintable.NewWriter(&qp)
	w.Write([]byte(page))
	w.Close()
	b64 := base64.StdEncoding.EncodeToString(img.Bytes())
	var lines []string
	for len(b64) > 76 {
		lines, b64 = append(lines, b64[:76]), b64[76:]
	}
	lines = append(lines, b64)
	crlf := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n") }
	return []byte(crlf(`From: <Saved by Blink>
MIME-Version: 1.0
Content-Type: multipart/related;
	type="text/html";
	boundary="----B----"

------B----
Content-Type: text/html
Content-Transfer-Encoding: quoted-printable
Content-Location: https://www.livelib.ru/reader/x/wish

` + qp.String() + `
------B----
Content-Type: image/png
Content-Transfer-Encoding: base64
Content-Location: https://s1.livelib.ru/boocover/1001/120x180/a/x.jpg

` + strings.Join(lines, "\n") + `
------B------
`))
}

func TestParseShelf(t *testing.T) {
	books, err := ParseMHTML(mhtml(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 {
		t.Fatalf("книг: %d", len(books))
	}
	a, b := books[0], books[1]
	if a.ID != "1001" || a.Title != "Мировой кризис. Часть 3 (конец)" || !a.Mine || a.Cover == nil {
		t.Errorf("первая книга: %+v", a)
	}
	if len(a.Authors) != 1 || a.Authors[0] != "Уинстон Черчилль" {
		t.Errorf("авторы первой: %q", a.Authors)
	}
	// обрезанные «Калиниче...» и «Ещё 1» не берём; без значка — не моя; обложки в файле нет
	if b.Title != "Шахматные дебюты. Полный курс" || b.Mine || b.Cover != nil ||
		len(b.Authors) != 1 || b.Authors[0] != "Я. Б. Эстрин" {
		t.Errorf("вторая книга: %+v", b)
	}
	if _, err := ParseMHTML([]byte("не mhtml")); err == nil {
		t.Error("мусор разобрался без ошибки")
	}
}

func TestImportIsRepeatable(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "filin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	st := catalog.New(d)
	covers := cover.Dir(filepath.Join(t.TempDir(), "covers"))
	books, err := ParseMHTML(mhtml(t))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Import(st, covers, books)
	if err != nil {
		t.Fatal(err)
	}
	if r.Added != 2 || r.Copies != 1 || r.Covers != 1 || len(r.Problems) != 0 {
		t.Fatalf("первый импорт: %+v", r)
	}
	if r, err = Import(st, covers, books); err != nil || r.Added != 0 || r.Skipped != 2 {
		t.Fatalf("повторный импорт: %+v, %v", r, err)
	}
	rows, _, _ := st.Search("черчилль", 10, 0)
	if len(rows) != 1 || rows[0].Cover == "" {
		t.Fatalf("поиск после импорта: %+v", rows)
	}
	e, _ := st.Edition(rows[0].ID)
	if e.Source != "livelib" || e.LiveLibID != "1001" {
		t.Errorf("происхождение: %q %q", e.Source, e.LiveLibID)
	}
}
