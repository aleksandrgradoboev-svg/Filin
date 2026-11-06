//go:build !windows

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
)

// Вне Windows Филин — тот же сервер, только без значка и ярлыков: запускается из
// терминала и закрывается Ctrl+C.

func OpenBrowser(url string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}

func Message(title, text string) { fmt.Fprintln(os.Stderr, title+": "+text) }

func AttachConsole() {}

// Ask вне Windows не спрашивает: ярлыков там нет.
func Ask(title, text string) bool { return false }

type TrayItem struct {
	Title, Tip string
	Disabled   bool
	Do         func()
}

func Tray(tip string, items []TrayItem, onQuit func()) {
	for _, it := range items {
		fmt.Println(it.Title)
	}
	fmt.Println("Закрыть — Ctrl+C")
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	onQuit()
}

func Shortcuts() ([]string, error) {
	return nil, fmt.Errorf("ярлыки делаются только в Windows")
}

func MakeShortcut(lnk, target, args, workDir, icon string) error {
	return fmt.Errorf("ярлыки делаются только в Windows")
}
