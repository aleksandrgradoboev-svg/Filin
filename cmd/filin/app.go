package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/backup"
	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/desktop"
	"github.com/aleksandrgradoboev-svg/filin/internal/web"
)

// app — Филин как программа: сервер на этом компьютере и в домашней сети, каталог в
// браузере, сова в трее. Закрыли — сервер остановлен, телефон Филина не видит.
func app(d *sql.DB, path, covers string) error {
	logTo(filepath.Join(filepath.Dir(path), "filin.log"))

	// 127.0.0.1 обязателен: с него открываются «Телефоны» и резервная копия
	local, err := net.Listen("tcp", "127.0.0.1:"+*port)
	if err != nil {
		// двойной щелчок по ярлыку: первый Филин успел занять порт, пока второй проверял
		time.Sleep(2 * time.Second)
		if desktop.Running(*port, web.PingBody) {
			return desktop.OpenBrowser("http://127.0.0.1:" + *port + "/")
		}
		return fmt.Errorf("порт %s занят другой программой: %w", *port, err)
	}
	ls := []net.Listener{local}
	var phone []string
	if ip := desktop.HomeIP(); ip != nil {
		a := net.JoinHostPort(ip.String(), *port)
		if l, err := net.Listen("tcp", a); err != nil { // домашняя сеть не далась — компьютер всё равно работает
			log.Printf("не слушаю %s: %v", a, err)
		} else {
			ls = append(ls, l)
			phone = append(phone, "http://"+a)
		}
	} else {
		log.Print("домашняя сеть не найдена — Филин виден только на этом компьютере")
	}

	opts := []web.Option{web.OCR(startOCR(filepath.Dir(path))), web.PhoneURLs(phone)}
	// «Дополнить из РГБ» в браузере на этом компьютере — поиск и список «Это она», как на телефоне
	opts = append(opts, rslSearch()...)
	h, err := web.New(catalog.New(d), covers, opts...)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}
	for _, l := range ls {
		log.Printf("Филин %s слушает http://%s", version, l.Addr())
		go func() {
			if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("%s: %v", l.Addr(), err)
			}
		}()
	}

	home := "http://127.0.0.1:" + *port + "/"
	offerShortcuts(path) // до браузера: вопрос не должен оказаться под ним
	if restarted() {
		log.Print("запущен после обновления: каталог в браузере уже открыт")
	} else if err := desktop.OpenBrowser(home); err != nil {
		log.Printf("браузер не открылся: %v", err)
	}
	items := []desktop.TrayItem{{Title: "Открыть каталог", Do: func() { desktop.OpenBrowser(home) }}}
	if len(phone) > 0 {
		items = append(items, desktop.TrayItem{Title: "Для телефона: " + phone[0], Disabled: true})
	} else {
		items = append(items, desktop.TrayItem{Title: "Домашняя сеть не найдена — телефон не увидит", Disabled: true})
	}
	u := &updater{d: d, path: path, covers: covers, quit: desktop.Quit}
	if supported() {
		items = append(items, desktop.TrayItem{Title: "Проверить обновления", Do: func() { u.check(true) }})
		go func() {
			cleanupOld()                // прежняя версия после обновления
			time.Sleep(5 * time.Second) // каталог сначала открывается, сеть — потом
			u.check(false)
		}()
	}
	desktop.Tray("Филин "+version, items, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		log.Print("Филин закрыт")
	})
	u.close() // начатая замена программы доходит до конца, пока база ещё открыта
	return nil
}

// shortcutsAsked — пометка рядом с базой: про ярлыки уже спросили, второй раз не спрашивать.
const shortcutsAsked = ".ярлыки-спрошены"

// offerShortcuts — первый запуск двойным щелчком: человек без командной строки получает
// ярлыки одним «Да». Есть ярлыки или уже спрашивали — молчит.
func offerShortcuts(path string) {
	lnks, err := desktop.Shortcuts()
	if err != nil {
		return // не Windows
	}
	have := true
	for _, l := range lnks {
		if _, err := os.Stat(l); err != nil {
			have = false
		}
	}
	marker := filepath.Join(filepath.Dir(path), shortcutsAsked)
	if _, err := os.Stat(marker); have || err == nil {
		return
	}
	dir, _ := filepath.Abs(filepath.Dir(path))
	yes := desktop.Ask("Филин", "Создать ярлык Филина в меню «Пуск» и на рабочем столе?\n\n"+
		"Библиотека хранится в этой папке:\n"+dir+"\n\nПеренести Филина — значит перенести папку целиком.")
	if err := os.WriteFile(marker, []byte("спрошено при первом запуске; удалить — спросит снова\n"), 0o644); err != nil {
		log.Printf("пометка о ярлыках: %v", err)
	}
	if !yes {
		return
	}
	if err := install(path); err != nil {
		log.Printf("ярлыки: %v", err)
		desktop.Message("Филин", "Ярлыки не создались: "+err.Error())
	}
}

// logTo пишет журнал в файл рядом с базой: у программы без консоли другого места нет.
// Больше мегабайта — прежний журнал откладывается в .1, чтобы файл не рос без предела.
func logTo(p string) {
	if st, err := os.Stat(p); err == nil && st.Size() > 1<<20 {
		os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
}

// restore восстанавливает библиотеку и возвращает отчёт для человека (он же печатается).
func restore(path, covers string, args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("укажите архив: filin restore filin-2026-11-06.zip")
	}
	// и на своём порту, и на обычном: Филин мог быть запущен ярлыком с другим -port.
	// Пропущенный здесь запущенный Филин не страшен — Restore тогда откатится, — но
	// честнее сказать сразу
	for _, p := range []string{*port, "8080"} {
		if desktop.Running(p, web.PingBody) {
			return "", fmt.Errorf("Филин запущен (порт %s) — закройте его (сова в трее → «Закрыть Филин») и повторите", p)
		}
	}
	aside, err := backup.Restore(args[0], path, covers, time.Now())
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Библиотека восстановлена из %s.\nПрежние данные отложены в %s — когда убедитесь, что всё на месте, папку можно удалить.",
		args[0], aside)
	fmt.Println(msg)
	return msg, nil
}

// install делает ярлыки «Филин» с теми же базой и настройками, с которыми запущена команда.
func install(path string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dbAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// базы может ещё не быть: она создаётся при первом запуске ярлыком; папка нужна сейчас — под значок
	if err := os.MkdirAll(filepath.Dir(dbAbs), 0o755); err != nil {
		return err
	}
	icon := filepath.Join(filepath.Dir(dbAbs), "filin.ico")
	if err := os.WriteFile(icon, desktop.Icon, 0o644); err != nil {
		return err
	}
	// ярлык помнит только то, что человек задал сам: база рядом с программой находится и так,
	// а зашитый путь сломался бы при переносе папки
	var args string
	if explicit["db"] {
		args += fmt.Sprintf(`-db "%s" `, dbAbs)
	}
	if *port != "8080" {
		args += "-port " + *port + " "
	}
	args += "app"
	lnks, err := desktop.Shortcuts()
	if err != nil {
		return err
	}
	for _, l := range lnks {
		if err := desktop.MakeShortcut(l, exe, args, filepath.Dir(exe), icon); err != nil {
			return err
		}
		fmt.Println("ярлык:", l)
	}
	fmt.Printf("Готово. Филин запускается ярлыком; база %s.\n", dbAbs)
	return nil
}

func uninstall() error {
	lnks, err := desktop.Shortcuts()
	if err != nil {
		return err
	}
	for _, l := range lnks {
		if err := os.Remove(l); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Println("убран ярлык:", l)
	}
	fmt.Println("База и фото не тронуты.")
	return nil
}

// rslSearch — поиск РГБ со списком: только в Windows, где есть встроенный браузер. Без него
// «Дополнить из РГБ» ведёт на форму со ссылкой, как раньше, а не на страницу с ошибкой.
func rslSearch() []web.Option {
	if runtime.GOOS != "windows" {
		return nil
	}
	return []web.Option{web.WithRSLSearcher(searchRSL)}
}

// searchRSL — поиск РГБ невидимым браузером (desktop) в виде, понятном веб-части.
func searchRSL(pageURL string, next []string) (web.RSLResults, error) {
	r, err := desktop.SearchRSL(pageURL, next)
	out := web.RSLResults{Query: r.Query, Info: r.Info}
	for _, f := range r.Items {
		out.Items = append(out.Items, web.RSLFound{ID: f.ID, Author: f.Author, Info: f.Info})
	}
	return out, err
}
