package web

import (
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
)

// Удаление книги из карточки: кнопка → страница с тем, что уйдёт → удаление → каталог
// с отметкой; файл обложки убран. Книга на руках — страница не даёт кнопки, запрос — отказ.
func TestDeleteBookFlow(t *testing.T) {
	a := newNetApp(t)
	get := func(path string) (int, string) {
		code, b := a.do(local, "GET", path, "", "", nil)
		return code, html.UnescapeString(string(b))
	}
	post := func(path string) int {
		code, _ := a.do(local, "POST", path, "", "application/x-www-form-urlencoded", nil)
		return code
	}

	ed, _ := a.st.AddEdition(&catalog.Edition{Title: "Очерки русской смуты", Volume: "Т. 2"})
	cp, err := a.st.AddCopy(&catalog.Copy{EditionID: ed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.AddValuation(&catalog.Valuation{CopyID: cp, Amount: 900}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(a.data, "covers"), 0o755); err != nil {
		t.Fatal(err)
	}
	coverName := strconv.FormatInt(ed, 10) + "-1.jpg"
	coverFile := filepath.Join(a.data, "covers", coverName)
	if err := os.WriteFile(coverFile, []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.SetCover(ed, coverName); err != nil {
		t.Fatal(err)
	}
	book := "/book/" + strconv.FormatInt(ed, 10)

	if _, page := get(book); !strings.Contains(page, `href="`+book+`/delete"`) {
		t.Fatal("в карточке нет кнопки «Удалить книгу»")
	}

	// на руках: объяснение вместо кнопки, а прямой запрос отклонён
	loan, err := a.st.Lend(&catalog.Loan{CopyID: cp, Borrower: "Сосед"})
	if err != nil {
		t.Fatal(err)
	}
	if _, page := get(book + "/delete"); !strings.Contains(page, "на руках") || strings.Contains(page, "Удалить насовсем") {
		t.Error("книга на руках: страница предлагает удалить")
	}
	if code := post(book + "/delete"); code != http.StatusBadRequest {
		t.Errorf("удаление книги на руках: %d", code)
	}
	if _, err := a.st.Return(loan, ""); err != nil {
		t.Fatal(err)
	}

	code, page := get(book + "/delete")
	if code != http.StatusOK || !strings.Contains(page, "Удалить насовсем") || !strings.Contains(page, "оценок: <b>1</b>") ||
		!strings.Contains(page, "«кто брал раньше»: <b>1</b>") {
		t.Fatalf("страница подтверждения: %d", code)
	}
	if code := post(book + "/delete"); code != http.StatusSeeOther {
		t.Fatalf("удаление: %d", code)
	}
	if code, _ := get(book); code != http.StatusNotFound {
		t.Errorf("карточка удалённой книги: %d", code)
	}
	// «назад» в приложении после удаления — на страницу подтверждения; оттуда в каталог
	if code, _ := a.do(local, "GET", book+"/delete", "", "", nil); code != http.StatusSeeOther {
		t.Errorf("подтверждение для удалённой книги: %d, ждали переход в каталог", code)
	}
	if _, err := os.Stat(coverFile); !os.IsNotExist(err) {
		t.Errorf("файл обложки остался: %v", err)
	}
	if _, page := get("/?deleted=" + url.QueryEscape("Очерки русской смуты Т. 2")); !strings.Contains(page, "«Очерки русской смуты Т. 2» удалена") {
		t.Error("в каталоге нет отметки об удалении")
	}
	if code := post("/book/999/delete"); code != http.StatusNotFound {
		t.Errorf("несуществующая книга: %d", code)
	}
}
