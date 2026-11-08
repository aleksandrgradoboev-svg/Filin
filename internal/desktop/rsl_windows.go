package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	webview2 "github.com/jchv/go-webview2"
)

// Поиск в РГБ на компьютере — так же, как на телефоне (RslActivity): сайт РГБ открывается
// во встроенном браузере Windows (WebView2), только невидимо; выдача читается со страницы
// тем же скриптом и показывается человеку списком на странице Филина — выбирает он.
// Программам поиск РГБ закрыт (robots.txt и проверка браузерной сессии), а браузер,
// запущенный по нажатию человека, — не программа.

// ErrRSLBusy — поиск уже идёт: второй не запускаем, чтобы выдачи не перепутались.
var ErrRSLBusy = errors.New("поиск в РГБ уже идёт — дождитесь его")

var rslBusy atomic.Bool

// RSLFound — запись из выдачи РГБ: номер, автор, описание издания, как на странице поиска.
type RSLFound struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	Info   string `json:"info"`
}

// RSLResults — итог поиска: по какому запросу нашлось (основному или запасному «все книги
// автора»), строка РГБ «Результатов: …» и сами записи.
type RSLResults struct {
	Query string     `json:"query"`
	Info  string     `json:"info"`
	Items []RSLFound `json:"items"`
}

// rslReader — тот же разбор выдачи, что у телефона (RslActivity.READ_RESULTS) и тот же порядок:
// каждые полсекунды до 20 секунд; пусто по основному запросу — запасной (только автор), при этом
// прежняя строка «Результатов: …» ещё висит и не считается.
const rslReader = `(function(){
  var fallback = %s, stale = null, polls = 0, current = decodeURIComponent(location.hash.replace(/^#q=/, ''));
  function read(){ var info=document.querySelector('.rsl-search-info');
    var items=[].slice.call(document.querySelectorAll('.search-item')).map(function(e){
      var m=e.querySelector('.js-item-maininfo'), a=e.querySelector('.js-item-authorinfo');
      return {id:m?m.getAttribute('data-id'):'', author:a?a.innerText.trim():'', info:m?m.innerText.trim():''};
    }).filter(function(x){return x.id});
    return {done:!!info, info:info?info.innerText.trim():'', items:items}; }
  function tick(){ var r=read();
    if (r.done && r.info !== stale) {
      if (r.items.length===0 && fallback && current!==fallback) { stale=r.info; current=fallback; polls=0;
        location.hash='#q='+encodeURIComponent(fallback); setTimeout(tick,500); return; }
      r.query=current; window.filinResults(JSON.stringify(r)); return; }
    if (++polls < 40) setTimeout(tick, 500); else window.filinResults('{"timeout":true}');
  }
  if (document.readyState==='loading') document.addEventListener('DOMContentLoaded', function(){ setTimeout(tick,500); });
  else setTimeout(tick,500);
})();`

var (
	showWindow = syscall.NewLazyDLL("user32.dll").NewProc("ShowWindow")
	isWindow   = syscall.NewLazyDLL("user32.dll").NewProc("IsWindow")
)

// rslDataDir — где браузер поиска держит куки и кэш РГБ (не в папке библиотеки).
// Переменная — для проверки: живой прогон берёт свой каталог, чтобы не мешать запущенному Филину.
var rslDataDir = func() string {
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir() // иначе путь стал бы относительным — кэш браузера в папке библиотеки
	}
	return filepath.Join(cache, "Filin", "webview2")
}

// rslTimeout — предел на весь поиск, от запуска браузера до выдачи. Внутри браузерного
// потока есть свой предохранитель (40 с на страницу), но он взводится только после запуска
// браузера, а запуск в go-webview2 ждёт без предела. Без общего предела страница «Ищу в РГБ…»
// крутилась бы вечно, а флаг «поиск идёт» не снимался бы до перезапуска Филина.
var rslTimeout = 60 * time.Second

// SearchRSL ищет в РГБ невидимым браузером: pageURL — страница поиска с запросом, fallback —
// запасной запрос, если по основному пусто. Возвращает выдачу для списка «Это она».
//
// Известный риск: при сбое настроек WebView2 библиотека go-webview2 зовёт log.Fatal и
// завершает весь процесс. Снаружи это не перехватить; на исправной Windows 10/11 не случается.
func SearchRSL(pageURL, fallback string) (RSLResults, error) {
	if !strings.HasPrefix(pageURL, "https://search.rsl.ru/") {
		return RSLResults{}, errors.New("поиск открывает только сайт РГБ")
	}
	if !rslBusy.CompareAndSwap(false, true) {
		return RSLResults{}, ErrRSLBusy
	}
	defer rslBusy.Store(false)

	type result struct {
		raw string
		err error
	}
	done := make(chan result, 1)
	go func() {
		// у браузера свой поток и свой цикл сообщений: основной поток занят значком в трее,
		// а Terminate гасит цикл того потока, из которого вызван
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		w := webview2.NewWithOptions(webview2.WebViewOptions{
			DataPath:      rslDataDir(),
			WindowOptions: webview2.WindowOptions{Title: "Филин — поиск в РГБ", Width: 1100, Height: 800},
		})
		if w == nil {
			done <- result{err: errors.New("встроенный браузер Windows (WebView2) не запустился")}
			return
		}
		defer closeWindow(w)
		showWindow.Call(uintptr(w.Window()), 0) // SW_HIDE: человек видит список на странице Филина, а не сайт
		var got string
		if err := w.Bind("filinResults", func(js string) {
			if got == "" {
				got = js
				w.Terminate() // вызывается в потоке браузера
			}
		}); err != nil {
			done <- result{err: err}
			return
		}
		fb, _ := json.Marshal(fallback)
		w.Init(fmt.Sprintf(rslReader, fb))
		w.Navigate(pageURL)
		// страховка: страница не загрузилась и скрипт так и не ответил — не висеть вечно
		stop := time.AfterFunc(40*time.Second, func() { w.Dispatch(func() { w.Terminate() }) })
		w.Run()
		stop.Stop()
		done <- result{raw: got}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(rslTimeout):
		// поток браузера завис (так зависал запуск WebView2 после брошенных окон): ждать его
		// дальше незачем — поиск снимается, следующий начнётся с нового браузера
		return RSLResults{}, errors.New("РГБ не ответила за минуту — попробуйте ещё раз; если повторится, перезапустите Филина")
	}
	if r.err != nil {
		return RSLResults{}, r.err
	}
	return parseRSLResults(r.raw)
}

// closeWindow закрывает окно поиска вместе с его браузером. Destroy в go-webview2 только
// ставит окну WM_CLOSE в очередь, а цикл сообщений к этому времени уже кончился: окно и
// браузер оставались жить до выхода Филина, и после нескольких поисков запуск следующего
// браузера зависал. Поэтому цикл крутится ещё раз — до WM_DESTROY, который и завершает его
// (wndproc библиотеки: WM_CLOSE → DestroyWindow → WM_DESTROY → Terminate). Вызывается
// в потоке браузера.
func closeWindow(w webview2.WebView) {
	hwnd := uintptr(w.Window())
	alive := func() bool { ok, _, _ := isWindow.Call(hwnd); return ok != 0 }
	w.Destroy()
	// Цикл крутится, пока окно живо: запоздалый Terminate от предохранителя страницы мог уже
	// стоять в очереди раньше WM_CLOSE и выйти из цикла до закрытия. Каждый круг — со своей
	// страховкой: WM_CLOSE потерялся — цикл не ждёт вечно.
	for i := 0; i < 3 && alive(); i++ {
		stop := time.AfterFunc(5*time.Second, func() { w.Dispatch(func() { w.Terminate() }) })
		w.Run()
		stop.Stop()
	}
}

// parseRSLResults разбирает ответ скрипта чтения выдачи.
func parseRSLResults(raw string) (RSLResults, error) {
	if raw == "" {
		return RSLResults{}, errors.New("РГБ не открылась — проверьте связь с интернетом")
	}
	var r struct {
		RSLResults
		Timeout bool `json:"timeout"`
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return RSLResults{}, fmt.Errorf("выдача РГБ не разобрана: %w", err)
	}
	if r.Timeout {
		return RSLResults{}, errors.New("РГБ не ответила за 20 секунд")
	}
	return r.RSLResults, nil
}
