package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aleksandrgradoboev-svg/filin/internal/catalog"
	"github.com/aleksandrgradoboev-svg/filin/internal/export"
)

// exportCatalog — filin export -csv файл -json файл: то же, что кнопка «Выгрузка», из терминала.
// Филин при этом может работать: выгрузка только читает базу.
func exportCatalog(st *catalog.Store, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	csvPath := fs.String("csv", "", "куда записать таблицу CSV")
	jsonPath := fs.String("json", "", "куда записать каталог целиком в JSON")
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil // справку уже напечатал сам разбор флагов
	} else if err != nil {
		return err
	}
	if *csvPath == "" && *jsonPath == "" || fs.NArg() > 0 {
		return errors.New("укажите, куда выгрузить: filin export -csv каталог.csv -json каталог.json (можно один из двух)")
	}
	lib, err := export.Build(st, version, time.Now())
	if err != nil {
		return err
	}
	for _, f := range []struct {
		path  string
		write func(io.Writer, *export.Library) error
	}{{*csvPath, export.CSV}, {*jsonPath, export.JSON}} {
		if f.path == "" {
			continue
		}
		if err := writeFile(f.path, func(w io.Writer) error { return f.write(w, lib) }); err != nil {
			return fmt.Errorf("%s: %w", f.path, err)
		}
		fmt.Printf("%s: изданий %d\n", f.path, len(lib.Books))
	}
	return nil
}

// writeFile пишет во временный файл рядом и переименовывает: оборванная запись не
// оставит под нужным именем половину каталога.
func writeFile(path string, write func(io.Writer) error) error {
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil { // например, файл открыт в Excel
		os.Remove(tmp)
		return err
	}
	return nil
}
