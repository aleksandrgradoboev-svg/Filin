package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRoute(t *testing.T) {
	for _, c := range []struct {
		in   []string
		cmd  string
		args []string
	}{
		{nil, "app", nil},                    // двойной щелчок
		{[]string{"app"}, "app", []string{}}, // ярлык
		{[]string{`D:\копия.ZIP`}, "restore-drop", []string{`D:\копия.ZIP`}},                  // архив на filin.exe
		{[]string{"app", `D:\filin-2026.zip`}, "restore-drop", []string{`D:\filin-2026.zip`}}, // архив на ярлык
		{[]string{"restore", "a.zip"}, "restore", []string{"a.zip"}},                          // из терминала — без окна
		{[]string{"import-livelib", "a.zip"}, "import-livelib", []string{"a.zip"}},
		{[]string{"stats"}, "stats", []string{}},
	} {
		cmd, args := route(c.in)
		if cmd != c.cmd || !slices.Equal(args, c.args) && !(len(args) == 0 && len(c.args) == 0) {
			t.Errorf("%q: %s %q", c.in, cmd, args)
		}
	}
}

func TestWritable(t *testing.T) {
	if err := writable(t.TempDir()); err != nil {
		t.Errorf("своя папка: %v", err)
	}
	err := writable(filepath.Join(t.TempDir(), "нет-такой"))
	if err == nil || !strings.Contains(err.Error(), "не в Program Files") {
		t.Errorf("папка без записи: %v", err)
	}
}
