package history

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out)
}

func add(t *testing.T, dir string, e Entry) {
	t.Helper()
	if err := Add(dir, e); err != nil {
		t.Fatal(err)
	}
}

func TestShowNoHistory(t *testing.T) {
	out := captureStdout(t, func() { Show(t.TempDir(), 20) })
	if out != "no deploys yet\n" {
		t.Fatalf("got %q", out)
	}
}

func TestShowGroupsByDayNewestFirst(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	add(t, dir, Entry{Time: time.Date(2020, 3, 4, 22, 47, 0, 0, time.Local), Repo: "/srv/my-portfolio", Commit: "77ab120", Message: "update list (hope61)", Commits: 1, Took: 9 * time.Second})
	add(t, dir, Entry{Time: now.AddDate(0, 0, -1), Repo: "/srv/tugo", Commit: "3cd01fb", Message: strings.Repeat("ж", 60) + " (hope61)", Commits: 1, Took: 41 * time.Second})
	add(t, dir, Entry{Time: now, Repo: "/srv/dipbot", Commit: "9f8e7d1", Message: "new alert (hope61)", Commits: 3, Error: "command failed with exit code 1: npm run build", Took: 2600 * time.Millisecond})

	out := captureStdout(t, func() { Show(dir, 20) })

	for _, want := range []string{
		"  " + now.Format("15:04") + "  ✗  dipbot        9f8e7d1  new alert (hope61) +2",
		"\n            command failed with exit code 1: npm run build\n",
		"  ✓  tugo          3cd01fb  " + strings.Repeat("ж", 37) + "...",
		"  22:47  ✓  my-portfolio  77ab120  update list (hope61)",
		"\n3 deploys, 1 failed\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}

	today, yesterday, old := strings.Index(out, "Today\n"), strings.Index(out, "\n\nYesterday\n"), strings.Index(out, "\n\nWed, Mar 4 2020\n")
	if today != 0 || yesterday < today || old < yesterday {
		t.Fatalf("days in the wrong order or missing:\n%s", out)
	}

	var width []int
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "  ✓  ") || strings.Contains(line, "  ✗  ") {
			width = append(width, utf8.RuneCountInString(line))
		}
	}
	if len(width) != 3 || width[0] != width[1] || width[1] != width[2] {
		t.Fatalf("columns do not line up, row widths %v:\n%s", width, out)
	}
	if !strings.HasSuffix(strings.Split(out, "\n")[1], "   3s") {
		t.Fatalf("took should be rounded to seconds and right aligned:\n%s", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatal("no colors when stdout is not a terminal")
	}
}

func TestShowLimit(t *testing.T) {
	dir := t.TempDir()
	add(t, dir, Entry{Time: time.Now(), Repo: "/srv/a", Commit: "old1111", Commits: 1})
	add(t, dir, Entry{Time: time.Now(), Repo: "/srv/a", Commit: "new2222", Commits: 1})

	out := captureStdout(t, func() { Show(dir, 1) })
	if !strings.Contains(out, "new2222") || strings.Contains(out, "old1111") || !strings.Contains(out, "1 deploy, 0 failed") {
		t.Fatalf("got:\n%s", out)
	}

	out = captureStdout(t, func() { Show(dir, 100) })
	if !strings.Contains(out, "2 deploys, 0 failed") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestShowSkipsBrokenLines(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte("garbage\n{\"repo\":\n"), 0644)
	add(t, dir, Entry{Time: time.Now(), Repo: "/srv/a", Commit: "abc1234", Commits: 1})

	out := captureStdout(t, func() { Show(dir, 20) })
	if !strings.Contains(out, "abc1234") || !strings.Contains(out, "1 deploy, 0 failed") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestAddKeepsLast500(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 505; i++ {
		add(t, dir, Entry{Time: time.Now(), Repo: "/srv/a", Commit: fmt.Sprintf("c%d", i), Commits: 1})
	}

	data, _ := os.ReadFile(filepath.Join(dir, "history.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 500 || !strings.Contains(lines[0], `"commit":"c5"`) || !strings.Contains(lines[499], `"commit":"c504"`) {
		t.Fatalf("expected c5..c504, got %d lines, first %s", len(lines), lines[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "history.jsonl.tmp")); err == nil {
		t.Fatal("temp file left behind")
	}
}

func TestAddFailsOnReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0555)
	t.Cleanup(func() { os.Chmod(dir, 0755) })

	if err := Add(dir, Entry{Time: time.Now()}); err == nil {
		t.Fatal("expected an error")
	}
}

func pendingExists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "deploying.json"))
	return err == nil
}

func TestStartThenAddClearsPending(t *testing.T) {
	dir := t.TempDir()
	e := Entry{Time: time.Now(), Repo: "/srv/app", Commit: "abc1234", Commits: 1}
	if err := Start(dir, e); err != nil || !pendingExists(dir) {
		t.Fatalf("pending deploy not saved: %v", err)
	}
	add(t, dir, e)
	if pendingExists(dir) {
		t.Fatal("pending deploy left behind after it finished")
	}
	if got, err := Recover(dir, "v0.1.0-0-gabc1234"); got != nil || err != nil {
		t.Fatalf("nothing should be recovered, got %+v %v", got, err)
	}
}

func TestRecoverSelfUpdate(t *testing.T) {
	dir := t.TempDir()
	Start(dir, Entry{Time: time.Now().Add(-3 * time.Second), Repo: "/home/ubuntu/tugo", Commit: "c8e98ef", Message: "fix (hope61)", Commits: 1})

	e, err := Recover(dir, "v0.1.0-1-gc8e98ef")
	if err != nil || e == nil || e.Interrupted || e.Error != "" || e.Took < 3*time.Second {
		t.Fatalf("self-update should be saved as a normal deploy, got %+v %v", e, err)
	}
	if pendingExists(dir) {
		t.Fatal("pending deploy left behind")
	}
	out := captureStdout(t, func() { Show(dir, 20) })
	if !strings.Contains(out, "  ✓  tugo  c8e98ef  fix (hope61)") || !strings.Contains(out, "1 deploy, 0 failed\n") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestRecoverInterrupted(t *testing.T) {
	dir := t.TempDir()
	add(t, dir, Entry{Time: time.Now(), Repo: "/srv/api", Commit: "1111111", Commits: 1, Error: "command failed with exit code 1: false"})
	Start(dir, Entry{Time: time.Now(), Repo: "/srv/app", Commit: "abc1234", Message: "new page (hope61)", Commits: 1})

	e, err := Recover(dir, "v0.1.0-1-gc8e98ef")
	if err != nil || e == nil || !e.Interrupted || e.Took != 0 {
		t.Fatalf("expected an interrupted deploy, got %+v %v", e, err)
	}

	out := captureStdout(t, func() { Show(dir, 20) })
	for _, want := range []string{
		"  ↻  app  abc1234  new page (hope61)",
		"\n            interrupted, tugo stopped before the commands finished\n",
		"\n2 deploys, 1 failed, 1 interrupted\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "0s\n            interrupted") {
		t.Fatalf("an interrupted deploy has no duration:\n%s", out)
	}
}

func TestRecoverBrokenPending(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "deploying.json"), []byte("{not json"), 0644)

	if e, err := Recover(dir, "dev"); e != nil || err == nil {
		t.Fatalf("got %+v %v", e, err)
	}
	if pendingExists(dir) {
		t.Fatal("a broken pending file should be removed so it isn't reported on every start")
	}
}
