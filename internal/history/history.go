package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	keep    = 500
	pending = "deploying.json"
)

type Entry struct {
	Time        time.Time     `json:"time"`
	Repo        string        `json:"repo"`
	Commit      string        `json:"commit"`
	Message     string        `json:"message"`
	Commits     int           `json:"commits"`
	Error       string        `json:"error,omitempty"`
	Interrupted bool          `json:"interrupted,omitempty"`
	Took        time.Duration `json:"took"`
}

// Start saves the deploy that is about to run, so it isn't lost if tugo is stopped in the middle of it.
// shortcut: one pending file, so a tugo -c deploying at the same moment as the service can overwrite it, upgrade to one file per repo if that matters
func Start(dir string, e Entry) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, pending), data, 0644)
}

// Recover saves the deploy that was still running when tugo stopped, if there is one. If the running
// tugo's version has the deploy's commit in it, tugo was updating itself and the update worked.
func Recover(dir, version string) (*Entry, error) {
	path := filepath.Join(dir, pending)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("broken %s: %w", pending, err)
	}

	if len(e.Commit) >= 7 && strings.Contains(version, e.Commit[:7]) {
		e.Took = time.Since(e.Time)
	} else {
		e.Interrupted = true
	}
	return &e, Add(dir, e)
}

func Add(dir string, e Entry) error {
	defer os.Remove(filepath.Join(dir, pending))
	path := filepath.Join(dir, "history.jsonl")

	line, err := json.Marshal(e)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) <= keep {
		return nil
	}

	// write to a temp file and rename so a crash never leaves a half written history
	tmp := path + ".tmp"
	err = os.WriteFile(tmp, []byte(strings.Join(lines[len(lines)-keep:], "\n")+"\n"), 0644)
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func Show(dir string, n int) {
	data, err := os.ReadFile(filepath.Join(dir, "history.jsonl"))
	if err != nil && !os.IsNotExist(err) {
		fmt.Printf("failed to read history: %v\n", err)
		return
	}

	var entries []Entry
	for _, line := range strings.Split(string(data), "\n") {
		var e Entry
		if json.Unmarshal([]byte(line), &e) == nil {
			entries = append(entries, e)
		}
	}

	if len(entries) == 0 {
		fmt.Println("no deploys yet")
		return
	}

	entries = entries[max(0, len(entries)-n):]
	slices.Reverse(entries)

	stat, err := os.Stdout.Stat()
	color := err == nil && stat.Mode()&os.ModeCharDevice != 0 && os.Getenv("NO_COLOR") == ""
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}

	var messages []string
	repoWidth, msgWidth := 0, 0
	for _, e := range entries {
		msg := cut(e.Message, 40)
		if e.Commits > 1 {
			msg += fmt.Sprintf(" +%d", e.Commits-1)
		}
		messages = append(messages, msg)
		repoWidth = max(repoWidth, utf8.RuneCountInString(filepath.Base(e.Repo)))
		msgWidth = max(msgWidth, utf8.RuneCountInString(msg))
	}

	day, failed, interrupted := "", 0, 0
	for i, e := range entries {
		t := e.Time.Local()
		if d := dayName(t); d != day {
			if day != "" {
				fmt.Println()
			}
			day = d
			fmt.Println(paint("1", d))
		}

		mark, took := paint("32", "✓"), e.Took.Round(time.Second).String()
		switch {
		case e.Error != "":
			mark = paint("31", "✗")
			failed++
		case e.Interrupted:
			mark, took = paint("33", "↻"), ""
			interrupted++
		}

		fmt.Printf("  %s  %s  %s  %s  %s  %s\n",
			paint("2", t.Format("15:04")),
			mark,
			paint("1", pad(filepath.Base(e.Repo), repoWidth)),
			paint("2", e.Commit),
			pad(messages[i], msgWidth),
			paint("2", fmt.Sprintf("%5s", took)),
		)
		if e.Error != "" {
			fmt.Printf("            %s\n", paint("31", e.Error))
		}
		if e.Interrupted {
			fmt.Printf("            %s\n", paint("33", "interrupted, tugo stopped before the commands finished"))
		}
	}

	word := "deploys"
	if len(entries) == 1 {
		word = "deploy"
	}
	fmt.Printf("\n%d %s, %d failed", len(entries), word, failed)
	if interrupted > 0 {
		fmt.Printf(", %d interrupted", interrupted)
	}
	fmt.Println()
}

func dayName(t time.Time) string {
	now := time.Now()
	switch t.Format("2006-01-02") {
	case now.Format("2006-01-02"):
		return "Today"
	case now.AddDate(0, 0, -1).Format("2006-01-02"):
		return "Yesterday"
	}
	if t.Year() == now.Year() {
		return t.Format("Mon, Jan 2")
	}
	return t.Format("Mon, Jan 2 2006")
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-utf8.RuneCountInString(s)))
}
