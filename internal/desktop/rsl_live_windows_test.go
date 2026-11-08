package desktop

import (
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var (
	enumWindows              = syscall.NewLazyDLL("user32.dll").NewProc("EnumWindows")
	getWindowThreadProcessID = syscall.NewLazyDLL("user32.dll").NewProc("GetWindowThreadProcessId")
	getWindowTextW           = syscall.NewLazyDLL("user32.dll").NewProc("InternalGetWindowText") // не шлёт окну WM_GETTEXT: брошенное окно на него не ответило бы
)

// searchWindows — сколько окон «Филин — поиск в РГБ» у этого процесса.
func searchWindows() int {
	n, me := 0, uint32(os.Getpid())
	cb := syscall.NewCallback(func(h, _ uintptr) uintptr {
		var pid uint32
		getWindowThreadProcessID.Call(h, uintptr(unsafe.Pointer(&pid)))
		if pid == me {
			buf := make([]uint16, 64)
			getWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 64)
			if syscall.UTF16ToString(buf) == "Филин — поиск в РГБ" {
				n++
			}
		}
		return 1
	})
	enumWindows.Call(cb, 0)
	return n
}

// Живой прогон: несколько поисков в настоящей РГБ подряд, как человек нажимает «Дополнить».
// После каждого окно поиска должно исчезнуть, а следующий поиск — пройти. Раньше окна
// копились, и на четвёртом поиске запуск браузера зависал без предела.
// Запуск: FILIN_LIVE_RSL=6 go test ./internal/desktop -run LiveRSL -v (число — сколько поисков).
func TestLiveRSLSearchesInARow(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("FILIN_LIVE_RSL"))
	if n <= 0 {
		t.Skip("живой прогон против РГБ — только по FILIN_LIVE_RSL=<число поисков>")
	}
	dir := t.TempDir()
	was := rslDataDir
	rslDataDir = func() string { return dir } // не мешать запущенному Филину: свой каталог браузера
	t.Cleanup(func() { rslDataDir = was })
	page := "https://search.rsl.ru/ru/search#q=isbn%3A9789851840881"
	for i := 1; i <= n; i++ {
		start := time.Now()
		res, err := SearchRSL(page, nil)
		if err != nil {
			t.Fatalf("поиск %d: %v", i, err)
		}
		time.Sleep(time.Second) // окно закрывается в потоке браузера сразу после выдачи
		w := searchWindows()
		t.Logf("поиск %d: %d записей за %v, окон поиска осталось: %d", i, len(res.Items), time.Since(start).Round(100*time.Millisecond), w)
		if len(res.Items) == 0 {
			t.Errorf("поиск %d: пусто (%q)", i, res.Info)
		}
		if w != 0 {
			t.Errorf("поиск %d: окно поиска не закрылось (%d)", i, w)
		}
		time.Sleep(2 * time.Second) // как человек, а не робот
	}
}
