package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hope61/tugo/internal/config"
	"github.com/hope61/tugo/internal/git"
	"github.com/hope61/tugo/internal/history"
)

// service is the state of the tugo systemd service, state is empty when systemctl isn't there (like on macOS)
type service struct {
	state string
	since time.Time
}

func serviceState() service {
	out, err := exec.Command("systemctl", "show", "tugo", "--timestamp=unix", "--property=LoadState,ActiveState,SubState,ActiveEnterTimestamp").Output()
	if err != nil {
		return service{}
	}
	return parseService(string(out))
}

func parseService(out string) service {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[key] = value
		}
	}

	switch {
	case props["LoadState"] == "not-found":
		return service{state: "missing"}
	case props["ActiveState"] == "active":
		s := service{state: "running"}
		if sec, err := strconv.ParseInt(strings.TrimPrefix(props["ActiveEnterTimestamp"], "@"), 10, 64); err == nil && sec > 0 {
			s.since = time.Unix(sec, 0)
		}
		return s
	case props["SubState"] == "auto-restart":
		return service{state: "crashing"}
	case props["ActiveState"] == "failed":
		return service{state: "failed"}
	case props["ActiveState"] == "":
		return service{}
	default:
		return service{state: "stopped"}
	}
}

type note struct{ color, text string }

type statusRow struct {
	kind                       string // ok, failed, paused, warning or new
	name, mode, branch, commit string
	mark, last, took           string
	notes                      []note
}

func showStatus(binDir string, cfg *config.Config, svc service, now time.Time) {
	paint := painter()
	latest := history.Latest(binDir)
	paused := pausedRepos(binDir)

	fmt.Printf(" %s  %s\n", paint("1", "TuGo"), paint("2", version))
	var line []string
	switch svc.state {
	case "running":
		s := paint("32", "●") + " running"
		if !svc.since.IsZero() {
			s += " for " + uptime(now.Sub(svc.since))
		}
		line = append(line, s)
	case "crashing":
		line = append(line, paint("31", "● crashing and restarting, see tugo -l"))
	case "failed":
		line = append(line, paint("31", "● stopped after a crash, see tugo -l"))
	case "stopped":
		line = append(line, paint("33", "● stopped")+paint("2", ", start it with sudo systemctl start tugo"))
	case "missing":
		line = append(line, paint("2", "○ not installed as a service, see sudo make service"))
	}
	var hooks, polls int
	for _, repo := range cfg.Repos {
		if repo.Mode == "webhook" {
			hooks++
		} else {
			polls++
		}
	}
	if hooks > 0 {
		line = append(line, "webhook on "+cfg.Listen)
	}
	if polls > 0 {
		line = append(line, "polling every "+shortDuration(cfg.Interval))
	}
	fmt.Printf(" %s\n\n", strings.Join(line, paint("2", "  ·  ")))

	var rows []statusRow
	for _, repo := range cfg.Repos {
		rows = append(rows, repoStatus(repo, latest, paused, now))
	}

	nameW, modeW, branchW, commitW, lastW := len("REPO"), len("MODE"), len("BRANCH"), len("COMMIT"), 0
	for _, r := range rows {
		nameW, modeW, branchW = max(nameW, width(r.name)), max(modeW, width(r.mode)), max(branchW, width(r.branch))
		commitW, lastW = max(commitW, width(r.commit)), max(lastW, width(r.lastText()))
	}

	fmt.Println(paint("2", fmt.Sprintf("    %s  %s  %s  %s  %s",
		padTo("REPO", nameW), padTo("MODE", modeW), padTo("BRANCH", branchW), padTo("COMMIT", commitW), "LAST DEPLOY")))

	colors := map[string]string{"ok": "32", "failed": "31", "paused": "33", "warning": "33", "new": "2"}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.kind]++
		dot := "●"
		if r.kind == "new" {
			dot = "○"
		}
		markColor := map[string]string{"✓": "32", "✗": "31", "↻": "33"}[r.mark]
		last := r.last
		if r.mark != "" {
			last = paint(markColor, r.mark) + " " + r.last
		}
		if r.took != "" {
			last += strings.Repeat(" ", lastW-width(r.lastText())) + paint("2", fmt.Sprintf("%5s", r.took))
		}
		fmt.Printf(" %s  %s  %s  %s  %s  %s\n",
			paint(colors[r.kind], dot),
			paint("1", padTo(r.name, nameW)),
			padTo(r.mode, modeW),
			padTo(r.branch, branchW),
			paint("2", padTo(r.commit, commitW)),
			last,
		)
		for _, n := range r.notes {
			fmt.Printf("    %s %s\n", paint("2", "└"), paint(n.color, n.text))
		}
	}

	summary := []string{fmt.Sprintf("%d repos", len(rows))}
	if len(rows) == 1 {
		summary[0] = "1 repo"
	}
	for _, kind := range []string{"ok", "failed", "paused", "warning", "new"} {
		if counts[kind] > 0 {
			label := map[string]string{"ok": "ok", "failed": "failed", "paused": "paused", "warning": "need a look", "new": "never deployed"}[kind]
			summary = append(summary, paint(colors[kind], fmt.Sprintf("%d %s", counts[kind], label)))
		}
	}
	fmt.Printf("\n %s\n", strings.Join(summary, paint("2", "  ·  ")))
	fmt.Println(paint("2", " tugo --history for every deploy, tugo -l for the logs"))
}

// lastText is the LAST DEPLOY cell without colors, used to line up the columns
func (r statusRow) lastText() string {
	if r.mark == "" {
		return r.last
	}
	return r.mark + " " + r.last
}

func repoStatus(repo config.Repo, latest map[string]history.Entry, paused []string, now time.Time) statusRow {
	r := statusRow{kind: "ok", name: filepath.Base(repo.Dir), mode: repo.Mode, branch: "-", commit: "-", last: "never"}
	warn := func(kind, color, text string) {
		r.notes = append(r.notes, note{color, text})
		// the first and worst problem decides the dot
		if r.kind == "ok" || r.kind == "new" || (kind == "failed" && r.kind != "failed") {
			r.kind = kind
		}
	}

	if _, err := os.Stat(repo.Dir); err != nil {
		warn("failed", "31", "the folder "+repo.Dir+" doesn't exist")
		return r
	}
	commit, subject, dirty, err := git.Info(repo.Dir)
	if err != nil {
		warn("failed", "31", err.Error())
		return r
	}
	r.commit = commit + " " + cutTo(subject, 32)
	if branch, err := git.CurrentBranch(repo.Dir); err == nil {
		r.branch = branch
	}

	e, deployed := latest[repo.Dir]
	if deployed {
		r.last = ago(now.Sub(e.Time))
		switch {
		case e.Error != "":
			r.mark, r.took = "✗", e.Took.Round(time.Second).String()
			warn("failed", "31", e.Error)
		case e.Interrupted:
			r.mark = "↻"
			warn("warning", "33", "the last deploy was interrupted, tugo stopped before the commands finished")
		default:
			r.mark, r.took = "✓", e.Took.Round(time.Second).String()
		}
	} else if r.kind == "ok" {
		r.kind = "new"
	}

	if slices.Contains(paused, repo.Dir) {
		warn("paused", "33", "paused, tugo --resume "+r.name+" to deploy it again")
	}
	if repo.Branch != "" && r.branch != repo.Branch {
		warn("warning", "33", fmt.Sprintf("on '%s' but the config expects '%s', not deploying", r.branch, repo.Branch))
	}
	if dirty {
		warn("warning", "33", "uncommitted changes in the folder, a force push can't be fixed automatically")
	}
	return r
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func uptime(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours()/24), int(d.Hours())%24)
	}
}

func painter() func(code, s string) string {
	stat, err := os.Stdout.Stat()
	color := err == nil && stat.Mode()&os.ModeCharDevice != 0 && os.Getenv("NO_COLOR") == ""
	return func(code, s string) string {
		if !color {
			return s
		}
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
}

func width(s string) int { return utf8.RuneCountInString(s) }

func padTo(s string, w int) string { return s + strings.Repeat(" ", max(0, w-width(s))) }

func cutTo(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-3]) + "..."
	}
	return s
}

// shortDuration prints 5m instead of 5m0s and 1h instead of 1h0m0s
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
