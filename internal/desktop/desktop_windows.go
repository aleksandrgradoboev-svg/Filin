package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"fyne.io/systray"
)

// OpenBrowser открывает адрес в браузере по умолчанию.
func OpenBrowser(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// Message показывает окно с сообщением: у программы без консоли других способов
// сказать человеку, что запуск не удался, нет.
func Message(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	const mbIconWarning = 0x30
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").
		Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconWarning|mbOnTop)
}

// mbOnTop — окно поверх остальных и с фокусом: иначе вопрос первого запуска прячется за
// только что открытым браузером, а программа ждёт ответа (MB_SETFOREGROUND | MB_TOPMOST).
const mbOnTop = 0x10000 | 0x40000

// Ask — вопрос окном «Да / Нет»; true — «Да».
func Ask(title, text string) bool {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	const mbYesNo, mbIconQuestion, idYes = 0x4, 0x20, 6
	r, _, _ := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").
		Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbYesNo|mbIconQuestion|mbOnTop)
	return r == idYes
}

// AttachConsole подключает вывод к окну терминала, из которого программу запустили:
// filin.exe собран без консоли (иначе при запуске с ярлыка висело бы чёрное окно),
// а команды stats, restore, install должны печатать в терминал. С ярлыка консоли нет —
// тогда ничего не делает.
func AttachConsole() {
	if _, err := os.Stdout.Stat(); err == nil {
		return // вывод уже куда-то идёт (перенаправлен в файл или канал) — его не трогаем
	}
	const attachParentProcess = ^uint32(0)
	r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole").Call(uintptr(attachParentProcess))
	if r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}

// TrayItem — пункт меню значка.
type TrayItem struct {
	Title, Tip string
	Disabled   bool // подпись, а не действие: например, адрес для телефона
	Do         func()
}

// Tray показывает значок в трее и держит программу, пока человек не выберет «Закрыть Филин»;
// после этого вызывается onQuit. items — пункты меню над «Закрыть Филин».
func Tray(tip string, items []TrayItem, onQuit func()) {
	baseTip.Store(tip)
	systray.Run(func() {
		systray.SetIcon(Icon)
		systray.SetTooltip(tip)
		for _, it := range items {
			m := systray.AddMenuItem(it.Title, it.Tip)
			if it.Disabled {
				m.Disable()
				continue
			}
			do := it.Do
			go func() {
				for range m.ClickedCh {
					do()
				}
			}()
		}
		systray.AddSeparator()
		quit := systray.AddMenuItem("Закрыть Филин", "Остановить Филина: телефон перестанет его видеть")
		go func() {
			<-quit.ClickedCh
			systray.Quit()
		}()
	}, onQuit)
}

// Quit закрывает программу так же, как «Закрыть Филин» в меню значка: с вызовом onQuit.
func Quit() { systray.Quit() }

var baseTip atomic.Value // подпись значка, заданная Tray

// SetTip — подпись значка в трее (видна при наведении): ход долгого дела, например
// скачивания обновления. Пусто — вернуть обычную.
func SetTip(s string) {
	if s == "" {
		s, _ = baseTip.Load().(string)
	}
	systray.SetTooltip(s)
}

// Shortcuts — где лежат ярлыки: меню «Пуск» и рабочий стол текущего пользователя.
func Shortcuts() ([]string, error) {
	appData, home := os.Getenv("APPDATA"), os.Getenv("USERPROFILE")
	if appData == "" || home == "" {
		return nil, fmt.Errorf("не найдены папки пользователя (APPDATA, USERPROFILE)")
	}
	return []string{
		filepath.Join(appData, `Microsoft\Windows\Start Menu\Programs\Филин.lnk`),
		filepath.Join(home, "Desktop", "Филин.lnk"),
	}, nil
}

// powershell — полный путь к Windows PowerShell. По короткому имени его находит не всякий
// процесс: у запущенного проводником (двойной щелчок по .cmd) в PATH его не оказалось —
// install падал «executable file not found» (06.11, живая установка на E:).
func powershell() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		p := filepath.Join(root, `System32\WindowsPowerShell\v1.0\powershell.exe`)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "powershell"
}

// MakeShortcut создаёт ярлык через WScript.Shell — так умеет любая Windows без сторонних
// программ. Аргументы ярлыка — как у командной строки filin.
func MakeShortcut(lnk, target, args, workDir, icon string) error {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	script := "$s = (New-Object -ComObject WScript.Shell).CreateShortcut(" + q(lnk) + ");" +
		"$s.TargetPath = " + q(target) + ";" +
		"$s.Arguments = " + q(args) + ";" +
		"$s.WorkingDirectory = " + q(workDir) + ";" +
		"$s.IconLocation = " + q(icon) + ";" +
		"$s.Description = 'Филин — каталог домашней библиотеки';" +
		"$s.Save()"
	cmd := exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ярлык %s: %v: %s", lnk, err, strings.TrimSpace(string(out)))
	}
	return nil
}
