package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/sys/windows"
)

// 這個檔案負責排版與輸出；所有給使用者看的文字都在 i18n.go 的 catalog 裡。

var (
	useColor bool
	stdin    = bufio.NewReader(os.Stdin)
	// 提示印到 stderr，讓 stdout 可以乾淨地接到管線；測試時會換掉。
	promptOut io.Writer = os.Stderr
	// 東亞寬度只算全形字，「→」這類寬度不明確的字元一律當半形。
	widths = func() *runewidth.Condition {
		c := runewidth.NewCondition()
		c.EastAsianWidth = false
		return c
	}()
)

// initConsole 只有在 stdout 是真正的主控台時才輸出顏色。
func initConsole() {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil || os.Getenv("NO_COLOR") != "" {
		return
	}
	if windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil {
		useColor = true
	}
}

func termWidth() int {
	var info windows.ConsoleScreenBufferInfo
	if windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info) != nil {
		return 0
	}
	return int(info.Window.Right-info.Window.Left) + 1
}

// debugEnabled 由 --debug 開啟；debugf 把診斷訊息印到 stderr。
// 診斷訊息是給回報問題用的，固定用英文。
var debugEnabled bool

func debugf(format string, args ...any) {
	if debugEnabled {
		fmt.Fprintln(os.Stderr, dim("[debug] "+fmt.Sprintf(format, args...)))
	}
}

func paint(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func bold(s string) string   { return paint("1", s) }
func dim(s string) string    { return paint("2", s) }
func cyan(s string) string   { return paint("1;36", s) }
func yellow(s string) string { return paint("33", s) }
func red(s string) string    { return paint("31", s) }
func green(s string) string  { return paint("32", s) }

func pad(s string, w int) string { return widths.FillRight(s, w) }

// clipMiddle 把過長的指令列從中間截掉：開頭是程式、結尾是參數（例如 npm 的 run dev），兩頭都要留。
func clipMiddle(s string, w int) string {
	if widths.StringWidth(s) <= w {
		return s
	}
	head := w * 2 / 5
	return widths.Truncate(s, head, "") + "…" + widths.TruncateLeft(s, widths.StringWidth(s)-(w-head-1), "")
}

func portLabel(port int) string { return fmt.Sprintf("Port %d", port) }

// humanSince 把啟動時間換成「多久以前」。
func humanSince(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 5*time.Second:
		return T.JustNow
	case d < time.Minute:
		return fmt.Sprintf(T.SecondsAgo, int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf(T.MinutesAgo, int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf(T.HoursAgo, int(d.Hours()))
	default:
		return fmt.Sprintf(T.DaysAgo, int(d.Hours()/24))
	}
}

// who 描述一個行程與它所在的位置；label 可以先上色再傳進來。
func who(o *Owner, label string) string {
	if o.Where == whereWSL {
		return fmt.Sprintf(T.WhoWSL, o.Distro, label)
	}
	return fmt.Sprintf(T.WhoWindows, label)
}

// labelWidth 是明細欄位名稱那一欄的寬度，依目前語言最長的名稱決定。
func labelWidth() int {
	w := 0
	for _, l := range []string{
		T.LabelAddress, T.LabelPath, T.LabelCommand, T.LabelDir, T.LabelUser, T.LabelStarted,
		T.LabelParent, T.LabelWorkers, T.LabelService, T.LabelContainer, T.LabelMaybe,
	} {
		w = max(w, widths.StringWidth(l))
	}
	return w + 2
}

func field(w io.Writer, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(w, "  %s %s\n", dim(pad(label, labelWidth())), value)
}

func note(w io.Writer, text string) {
	fmt.Fprintf(w, "  %s\n", yellow(text))
}

// targetLabel 是關閉對象的稱呼：容器或行程。
func targetLabel(o *Owner) string {
	if len(o.Containers) > 0 {
		names := make([]string, len(o.Containers))
		for i, c := range o.Containers {
			names[i] = c.Name
		}
		return fmt.Sprintf(T.TargetCont, strings.Join(names, T.ListSep))
	}
	return o.Label()
}

func printOwner(w io.Writer, rep *Report, o *Owner, index int, now time.Time) {
	head := fmt.Sprintf("%s → %s", cyan(portLabel(rep.Port)), who(o, bold(o.Label())))
	if len(rep.Owners) > 1 {
		head = fmt.Sprintf("[%d] %s", index+1, head)
	}
	fmt.Fprintln(w, head)

	field(w, T.LabelAddress, strings.Join(o.Addrs, T.ListSep))
	if o.Where == whereWindows {
		field(w, T.LabelPath, o.Exe)
	}
	field(w, T.LabelCommand, clipMiddle(o.Cmdline, 300))
	field(w, T.LabelDir, o.Cwd)
	field(w, T.LabelUser, o.User)
	if !o.Started.IsZero() {
		field(w, T.LabelStarted, humanSince(o.Started, now))
	}
	// 祖先鏈由近到遠，一層一行：sh -c node server.js ← npm run dev ← bash
	for i, a := range o.Ancestors {
		text := fmt.Sprintf("%s (PID %d)", clipMiddle(a.Text(), 90), a.PID)
		if i == 0 {
			field(w, T.LabelParent, text)
		} else {
			fmt.Fprintf(w, "  %s ← %s\n", pad("", labelWidth()), text)
		}
	}
	if len(o.Workers) > 0 {
		field(w, T.LabelWorkers, fmt.Sprintf(T.WorkersText, len(o.Workers), joinInts(o.Workers)))
	}
	field(w, T.LabelService, o.Unit)
	for _, c := range o.Containers {
		field(w, T.LabelContainer, fmt.Sprintf(T.Container, c.Name, c.Image))
	}
	if len(o.Heirs) > 0 {
		field(w, T.LabelMaybe, strings.Join(o.Heirs, T.ListSep))
	}

	if o.InContainer && len(o.Containers) == 0 {
		note(w, T.NoteInContainer)
	}
	if o.NotForwarded {
		note(w, T.NoteNotForwarded)
	}
	if o.Where == whereWSL && rep.Mode == "nat" && !o.NotForwarded && !rep.WinListening {
		if rep.Reserved.Blocking() {
			note(w, fmt.Sprintf(T.NoteRelayBlocked, rep.Reserved.Range.Start, rep.Reserved.Range.End, rep.Port))
		} else {
			note(w, T.NoteNoRelayYet)
		}
	}
	if o.Limited {
		note(w, T.NoteLimited)
	}
	if o.Service && o.Protected == "" {
		note(w, T.NoteService)
	}
	if o.Protected != "" && len(o.Containers) == 0 {
		note(w, o.Protected)
	}
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, T.ListSep)
}

func dynamicAdvice(w io.Writer, d *reservedDiag) {
	if !d.LowDynamicRange() {
		return
	}
	fmt.Fprintf(w, T.DynamicAdvice, d.DynamicStart, defaultDynamicStart)
	fmt.Fprintln(w, bold("    netsh int ipv4 set dynamicport tcp start=49152 num=16384"))
	fmt.Fprintln(w, bold("    netsh int ipv6 set dynamicport tcp start=49152 num=16384"))
}

// relayBlocked 回報是否有 WSL 行程因為保留範圍而無法轉送到 Windows。
func relayBlocked(rep *Report) bool {
	if !rep.Reserved.Blocking() || rep.Mode != "nat" || rep.WinListening {
		return false
	}
	for _, o := range rep.Owners {
		if o.Where == whereWSL && !o.NotForwarded {
			return true
		}
	}
	return false
}

// printReservedFix 印出保留範圍的成因與解法。
func printReservedFix(w io.Writer, d *reservedDiag) {
	if d.Range.Start == d.Range.End {
		fmt.Fprintln(w, T.ReservedSingleFix)
	} else {
		fmt.Fprintln(w, T.ReservedRangeWhy)
		fmt.Fprintln(w, T.ReservedRangeFix)
		fmt.Fprintln(w, bold("    net stop winnat"))
		fmt.Fprintln(w, bold("    net start winnat"))
		fmt.Fprintln(w, T.ReservedOrChange)
	}
	dynamicAdvice(w, d)
}

// printReport 印出單一 port 的查詢結果。
func printReport(w io.Writer, rep *Report, now time.Time) {
	port := cyan(portLabel(rep.Port))
	for i, o := range rep.Owners {
		if i > 0 {
			fmt.Fprintln(w)
		}
		printOwner(w, rep, o, i, now)
	}
	if len(rep.Orphans) > 0 {
		if len(rep.Owners) > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, T.OrphanHead+"\n", port, strings.Join(rep.Orphans, T.ListSep))
		note(w, T.OrphanNote)
	}
	for _, r := range rep.Relays {
		fmt.Fprintf(w, "  %s\n", dim(fmt.Sprintf(T.RelayFootnote, r.Label())))
	}

	d := rep.Reserved
	switch {
	case len(rep.Owners) > 0 || len(rep.Orphans) > 0:
		// 佔用者底下已說明「Windows 無法轉送」，這裡補上解法。
		if relayBlocked(rep) {
			fmt.Fprintln(w)
			printReservedFix(w, d)
		}
	case d.Blocking():
		if d.Range.Start == d.Range.End {
			fmt.Fprintf(w, T.ReservedSingle+"\n", port, d.Family)
		} else {
			fmt.Fprintf(w, T.ReservedRange+"\n", port, bold(fmt.Sprintf("%d–%d", d.Range.Start, d.Range.End)), d.Family)
		}
		printReservedFix(w, d)
	case len(rep.Ephemeral) > 0:
		names := make([]string, len(rep.Ephemeral))
		for i, o := range rep.Ephemeral {
			names[i] = o.Label()
		}
		fmt.Fprintf(w, T.EphemeralHead+"\n", port, bold(strings.Join(names, T.ListSep)))
		fmt.Fprintln(w, T.EphemeralWhy)
		fmt.Fprintln(w, T.EphemeralWait)
		dynamicAdvice(w, rep.Reserved)
	default:
		fmt.Fprintf(w, T.Free+"\n", port)
		if d != nil && d.InRange && d.Range.Admin {
			fmt.Fprintf(w, "  %s\n", dim(fmt.Sprintf(T.AdminRangeInfo, d.Range.Start, d.Range.End)))
		}
		if rep.Mode == "mirrored" {
			note(w, T.MirroredNote)
		}
	}

	for _, n := range rep.Notes {
		note(w, n)
	}
	for _, name := range rep.WSL1 {
		fmt.Fprintf(w, "  %s\n", dim(fmt.Sprintf(T.WSL1Skipped, name)))
	}
}

// printList 印出所有監聽中 port 的總表。
func printList(w io.Writer, rows []listRow, notes []string) {
	if len(rows) == 0 {
		fmt.Fprintln(w, T.ListEmpty)
	}
	headers := T.ListHeaders[:]
	limits := []int{5, 22, 7, 24, 30}
	cols := make([][]string, len(rows))
	relayed := false
	for i, r := range rows {
		where := r.Where
		if r.Relayed {
			where += " *"
			relayed = true
		}
		pid := "-"
		if r.PID > 0 || r.Where == "Windows" {
			pid = fmt.Sprint(r.PID)
		}
		cols[i] = []string{fmt.Sprint(r.Port), where, pid, r.Name, strings.Join(r.Addrs, ", "), r.Detail}
	}
	colWidths := make([]int, len(limits))
	for c := range limits {
		colWidths[c] = widths.StringWidth(headers[c])
		for _, row := range cols {
			if n := widths.StringWidth(row[c]); n > colWidths[c] {
				colWidths[c] = n
			}
		}
		if colWidths[c] > limits[c] {
			colWidths[c] = limits[c]
		}
	}
	used := 0
	for _, n := range colWidths {
		used += n + 2
	}
	// 指令欄截到終端機寬度；輸出到檔案或管線時沒有寬度可參考，固定截在 100 欄。
	detailWidth := 100
	if tw := termWidth(); tw > 0 {
		detailWidth = max(tw-used-1, 20)
	}

	line := func(row []string, style func(string) string) {
		var b strings.Builder
		for c, n := range colWidths {
			b.WriteString(pad(widths.Truncate(row[c], n, "…"), n))
			b.WriteString("  ")
		}
		b.WriteString(widths.Truncate(row[len(row)-1], detailWidth, "…"))
		fmt.Fprintln(w, style(strings.TrimRight(b.String(), " ")))
	}
	if len(rows) > 0 {
		line(headers, bold)
		for _, row := range cols {
			line(row, func(s string) string { return s })
		}
	}
	if relayed {
		fmt.Fprintf(w, "\n%s\n", dim(T.ListRelayFootnote))
	}
	for _, n := range notes {
		fmt.Fprintln(w, yellow(n))
	}
}

// ask 把提示印到 stderr 並讀一行；讀到 EOF（沒有可互動的輸入）時回傳 false。
func ask(prompt string) (string, bool) {
	fmt.Fprint(promptOut, prompt)
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(promptOut)
		return "", false
	}
	return strings.TrimSpace(line), true
}

// isYes 不分語言：英文介面輸入「是」、中文介面輸入 y 都算同意。
func isYes(s string) bool {
	s = strings.ToLower(s)
	for _, w := range yesWords {
		if s == w {
			return true
		}
	}
	return false
}
