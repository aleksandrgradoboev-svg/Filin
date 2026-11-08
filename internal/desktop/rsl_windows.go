package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"

	webview2 "github.com/jchv/go-webview2"
)

// Поиск в РГБ на компьютере — так же, как на телефоне: сайт РГБ открывается во встроенном
// браузере (WebView2 есть в каждой Windows 10/11), ищет и выбирает человек, Филин только
// узнаёт, на какую запись он нажал. Программам поиск РГБ закрыт (robots.txt и проверка
// браузерной сессии), а встроенный браузер, которым пользуется человек, — не программа.

// ErrRSLBusy — окно поиска уже открыто: второе не открываем, чтобы выбор не перепутался.
var ErrRSLBusy = errors.New("окно поиска в РГБ уже открыто")

var rslOpen atomic.Bool

var reRecordHref = regexp.MustCompile(`record/(\d{8,14})`)

// rslHook — скрипт в каждой странице окна: нажатие на запись (в выдаче, в списке «похожих»)
// не уходит на страницу записи, а отдаёт её номер Филину; если человек всё же открыл запись
// другим путём — номер берётся из адреса.
const rslHook = `(function(){
  function pick(href){ var m = String(href).match(/record\/(\d{8,14})/); if (m) { window.filinPick(m[1]); return true; } return false; }
  document.addEventListener('click', function(e){
    var a = e.target.closest && e.target.closest('a[href*="/record/"]');
    if (a && pick(a.getAttribute('href'))) { e.preventDefault(); e.stopPropagation(); }
  }, true);
  if (/\/record\/\d{8,14}/.test(location.pathname)) pick(location.pathname);
})();`

// PickRSL открывает окно со страницей поиска РГБ pageURL и ждёт, пока человек нажмёт на
// запись или закроет окно. ok=false — закрыл, ничего не выбрав. Блокирует до закрытия окна.
//
// Известный риск: при сбое настроек WebView2 библиотека go-webview2 зовёт log.Fatal и
// завершает весь процесс. Снаружи это не перехватить; на исправной Windows 10/11 не случается.
func PickRSL(pageURL string) (record string, ok bool, err error) {
	if !strings.HasPrefix(pageURL, "https://search.rsl.ru/") {
		return "", false, errors.New("окно поиска открывает только сайт РГБ")
	}
	if !rslOpen.CompareAndSwap(false, true) {
		return "", false, ErrRSLBusy
	}
	defer rslOpen.Store(false)

	type result struct {
		id  string
		err error
	}
	done := make(chan result, 1)
	go func() {
		// у окна свой поток и свой цикл сообщений: основной поток занят значком в трее,
		// а Terminate гасит цикл того потока, из которого вызван
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		var picked string
		cache, err := os.UserCacheDir()
		if err != nil {
			cache = os.TempDir() // иначе путь стал бы относительным — кэш браузера в папке библиотеки
		}
		w := webview2.NewWithOptions(webview2.WebViewOptions{
			DataPath: filepath.Join(cache, "Filin", "webview2"), // куки и кэш РГБ — не в папке библиотеки
			WindowOptions: webview2.WindowOptions{
				Title: "Филин — найдите своё издание в РГБ и нажмите на него",
				Width: 1100, Height: 800, Center: true,
			},
		})
		if w == nil {
			done <- result{err: errors.New("встроенный браузер Windows (WebView2) не открылся — найдите запись в своём браузере и вставьте ссылку")}
			return
		}
		defer w.Destroy()
		if err := w.Bind("filinPick", func(id string) {
			if reRecordHref.MatchString("record/"+id) && picked == "" {
				picked = id
				w.Terminate() // вызывается в потоке окна
			}
		}); err != nil {
			done <- result{err: err}
			return
		}
		w.Init(rslHook)
		w.Navigate(pageURL)
		w.Run()
		done <- result{id: picked}
	}()
	r := <-done
	return r.id, r.id != "", r.err
}
