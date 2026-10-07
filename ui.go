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

// 介面文字集中在這個檔案。

const (
	msgProtectKernel  = "這個 port 由 Windows 核心（System）持有，通常是 HTTP.sys、SMB 之類的系統元件，沒辦法用關閉行程的方式釋放。可以用 `netsh http show servicestate` 查是哪個服務註冊的。"
	msgProtectSystem  = "這是 Windows 的關鍵系統行程，關掉會讓系統不穩定，wslport 不提供關閉。"
	msgProtectWSL     = "這是 WSL 虛擬機本身的行程，wslport 不提供關閉。要關掉整個 WSL 請用 `wsl --shutdown`。"
	msgProtectRelay   = "這是 WSL 的 localhost 轉送程式。關掉它會讓所有 WSL port 的轉送失效，要 `wsl --shutdown` 才會恢復，所以 wslport 不提供關閉。"
	msgProtectSvchost = "這是 Windows 服務的宿主行程，裡面可能同時有好幾個服務，wslport 不提供關閉。如果是 port 轉送規則，可以用 `netsh interface portproxy show all` 查看。"
	msgProtectDocker  = "這是 Docker Desktop 的後端程式，請用 `docker stop <容器>` 關閉對應的容器，不要直接關掉它。"
	msgProtectDead    = "登記在這個 port 上的行程已經結束，socket 由它啟動的子行程繼承，無法確定是哪一個，所以 wslport 不提供關閉。"
	msgDeadOwner      = "已結束的行程"

	msgProbeFailed  = "無法探測 distro「%s」：%v"
	msgProbeTimeout = "探測逾時"
	msgNoOutput     = "沒有回應"
	msgNoTool       = "distro「%s」裡沒有 ss 也沒有 netstat，無法查詢（安裝 iproute2 即可）。"
	msgWSL1Skipped  = "distro「%s」是 WSL1，wslport 不會追進去；它的行程會直接出現在 Windows 這一側。"
	msgWhereWSLVM   = "WSL"
	msgOrphanShort  = "（看不到擁有者）"

	msgUnknownFlag  = "不認得的參數「%s」"
	msgBadPort      = "「%s」不是有效的 port（請輸入 1–65535 的整數）"
	msgOnePort      = "一次只能查一個 port"
	msgNeedPort     = "%s 需要搭配 port 使用"
	msgFlagConflict = "-n 不能和 -k、-f 一起使用"
)

const usageText = `wslport — 查出是誰佔用了 port，連 WSL 裡的行程都追得到

用法：
  wslport              列出 Windows 與各 distro 所有監聽中的 port
  wslport <port>       查這個 port 的佔用者，找到後詢問是否關閉

選項：
  -n, --no-kill        只查詢，不詢問是否關閉
  -k, --kill           不詢問，直接關閉（只在佔用者唯一時有效）
  -f, --force          WSL 裡的行程直接用 SIGKILL 強制終止
  -h, --help           顯示這份說明
  -v, --version        顯示版本
      --debug          顯示每個步驟的耗時與探測結果（回報問題時請附上）

結束碼：0 找到佔用者（或已關閉）、1 port 沒有人使用、2 發生錯誤或 -k 沒能關閉
`

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

// humanSince 把啟動時間換成「多久以前」。
func humanSince(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 5*time.Second:
		return "剛剛"
	case d < time.Minute:
		return fmt.Sprintf("%d 秒前", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d 分鐘前", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d 小時前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}

// whereText 描述行程所在的位置。
func whereText(o *Owner) string {
	if o.Where == whereWSL {
		return fmt.Sprintf("WSL「%s」裡的", o.Distro)
	}
	return "Windows 的"
}

func field(w io.Writer, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(w, "  %s %s\n", dim(pad(label, 8)), value)
}

func note(w io.Writer, text string) {
	fmt.Fprintf(w, "  %s\n", yellow(text))
}

func containerText(c container) string {
	return fmt.Sprintf("%s（%s）", c.Name, c.Image)
}

// targetLabel 是關閉對象的稱呼：容器或行程。
func targetLabel(o *Owner) string {
	if len(o.Containers) > 0 {
		names := make([]string, len(o.Containers))
		for i, c := range o.Containers {
			names[i] = c.Name
		}
		return "容器 " + strings.Join(names, "、")
	}
	return o.Label()
}

func printOwner(w io.Writer, rep *Report, o *Owner, index int, now time.Time) {
	head := fmt.Sprintf("%s → %s %s", cyan(fmt.Sprintf("Port %d", rep.Port)), whereText(o), bold(o.Label()))
	if len(rep.Owners) > 1 {
		head = fmt.Sprintf("[%d] %s", index+1, head)
	}
	fmt.Fprintln(w, head)

	field(w, "位址", strings.Join(o.Addrs, "、"))
	if o.Where == whereWindows {
		field(w, "路徑", o.Exe)
	}
	field(w, "指令", clipMiddle(o.Cmdline, 300))
	field(w, "目錄", o.Cwd)
	field(w, "使用者", o.User)
	if !o.Started.IsZero() {
		field(w, "啟動", humanSince(o.Started, now))
	}
	// 祖先鏈由近到遠，一層一行：sh -c node server.js ← npm run dev ← bash
	for i, a := range o.Ancestors {
		text := fmt.Sprintf("%s (PID %d)", clipMiddle(a.Text(), 90), a.PID)
		if i == 0 {
			field(w, "父行程", text)
		} else {
			fmt.Fprintf(w, "  %s ← %s\n", pad("", 8), text)
		}
	}
	if len(o.Workers) > 0 {
		field(w, "子行程", fmt.Sprintf("另有 %d 個子行程共用這個 port（PID %s）", len(o.Workers), joinInts(o.Workers)))
	}
	field(w, "服務", o.Unit)
	for _, c := range o.Containers {
		field(w, "容器", containerText(c))
	}
	if len(o.Heirs) > 0 {
		field(w, "可能是", strings.Join(o.Heirs, "、"))
	}

	if o.InContainer && len(o.Containers) == 0 {
		note(w, "這個行程跑在容器裡，建議用容器工具（例如 docker stop）關閉。")
	}
	if o.NotForwarded {
		note(w, "它綁定的位址不會被轉送到 Windows，所以不會和 Windows 上的程式搶這個 port。")
	}
	if o.Where == whereWSL && rep.Mode == "nat" && !o.NotForwarded && !rep.WinListening {
		if rep.Reserved.Blocking() {
			note(w, fmt.Sprintf("Windows 沒辦法替它轉送：這個 port 落在 Windows 的保留範圍 %d–%d 內，所以從 Windows 連 localhost:%d 會失敗。",
				rep.Reserved.Range.Start, rep.Reserved.Range.End, rep.Port))
		} else {
			note(w, "Windows 這一側目前沒有對應的轉送；行程剛啟動的話，大約 1 秒內會出現。")
		}
	}
	if o.Limited {
		note(w, "權限不足，讀不到路徑與指令。用「以系統管理員身分執行」開啟終端機可以看到更多。")
	}
	if o.Service && o.Protected == "" {
		note(w, "這是 Windows 服務，強制結束後可能會自動重新啟動；建議改用「服務」管理員或 `sc stop` 停止。")
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
	return strings.Join(s, "、")
}

func dynamicAdvice(w io.Writer, d *reservedDiag) {
	if !d.LowDynamicRange() {
		return
	}
	fmt.Fprintf(w, "\n  這台電腦的動態埠範圍從 %d 開始（Windows 預設是 %d），所以系統保留和對外連線\n  會落在開發常用的 port 上。永久解法（需要系統管理員權限，設定後重新開機）：\n", d.DynamicStart, defaultDynamicStart)
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
		fmt.Fprintln(w, "  單一 port 的保留通常由系統元件持有，重新啟動 winnat 也不會釋放，建議換一個 port。")
	} else {
		fmt.Fprintln(w, "  這類範圍通常是 Hyper-V／WinNAT（WSL、Docker 會用到）開機時動態保留的。")
		fmt.Fprintln(w, "  暫時解法（需要系統管理員權限；重啟後範圍會重新分配，不保證避開）：")
		fmt.Fprintln(w, bold("    net stop winnat"))
		fmt.Fprintln(w, bold("    net start winnat"))
		fmt.Fprintln(w, "  或者直接換一個 port。")
	}
	dynamicAdvice(w, d)
}

// printReport 印出單一 port 的查詢結果。
func printReport(w io.Writer, rep *Report, now time.Time) {
	port := cyan(fmt.Sprintf("Port %d", rep.Port))
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
		fmt.Fprintf(w, "%s → 在 WSL 虛擬機內有人監聽（%s），但看不到擁有者\n", port, strings.Join(rep.Orphans, "、"))
		note(w, "沒有任何執行中的 distro 認領它；可能屬於 WSL 本身、Docker Desktop，或上面探測失敗的 distro。")
	}
	for _, r := range rep.Relays {
		fmt.Fprintf(w, "  %s\n", dim(fmt.Sprintf("Windows 端的 %s 只是轉送，真正的佔用者在 WSL 裡。", r.Label())))
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
			fmt.Fprintf(w, "%s 沒有任何行程在監聽，但它被 Windows 單獨保留了（%s），程式綁定時會被拒絕。\n", port, d.Family)
		} else {
			fmt.Fprintf(w, "%s 沒有任何行程在監聽，但它落在 Windows 的保留埠範圍 %s 內（%s），\n程式綁定時會被拒絕（存取被拒，錯誤 10013），看起來就像被佔用。\n",
				port, bold(fmt.Sprintf("%d–%d", d.Range.Start, d.Range.End)), d.Family)
		}
		printReservedFix(w, d)
	case len(rep.Ephemeral) > 0:
		names := make([]string, len(rep.Ephemeral))
		for i, o := range rep.Ephemeral {
			names[i] = o.Label()
		}
		fmt.Fprintf(w, "%s 沒有任何行程在監聽，但目前被 %s 的連線當作本機埠使用中。\n", port, bold(strings.Join(names, "、")))
		fmt.Fprintln(w, "  這是系統隨機分配給對外連線的 port，連線結束就會釋放，wslport 不會去關它。")
		fmt.Fprintln(w, "  稍等一下再試，或換一個 port。")
		dynamicAdvice(w, rep.Reserved)
	default:
		fmt.Fprintf(w, "%s 目前沒有人使用。\n", port)
		if d != nil && d.InRange && d.Range.Admin {
			fmt.Fprintf(w, "  %s\n", dim(fmt.Sprintf("（它在使用者自訂的保留範圍 %d–%d 內；這種範圍不會擋住程式綁定。）", d.Range.Start, d.Range.End)))
		}
		if rep.Mode == "mirrored" {
			note(w, "WSL 目前是 mirrored 網路模式，這個模式會另外保留一段 port，而且不會顯示在任何清單裡；如果還是綁不上，可能就是落在那一段。")
		}
	}

	for _, n := range rep.Notes {
		note(w, n)
	}
	for _, name := range rep.WSL1 {
		fmt.Fprintf(w, "  %s\n", dim(fmt.Sprintf(msgWSL1Skipped, name)))
	}
}

// printList 印出所有監聽中 port 的總表。
func printList(w io.Writer, rows []listRow, notes []string) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "目前沒有任何監聽中的 port。")
	}
	headers := []string{"PORT", "位置", "PID", "行程", "位址", "指令"}
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
		fmt.Fprintf(w, "\n%s\n", dim("* 這個 port 由 wslrelay 轉送到 Windows 的 localhost。"))
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

func isYes(s string) bool {
	switch strings.ToLower(s) {
	case "y", "yes", "是", "好":
		return true
	}
	return false
}
