// wslport 查出是誰佔用了某個 TCP port：Windows 的哪個程式，或哪個 WSL distro 裡的哪個行程，
// 並在確認後把它關掉。
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// version 在建置時由 -ldflags "-X main.version=…" 注入。
var version = "dev"

type options struct {
	port    int
	noKill  bool
	kill    bool
	force   bool
	help    bool
	version bool
	debug   bool
}

// parseArgs 解析參數；旗標可以放在 port 前面或後面，短旗標可以合併（-kf）。
// --lang 的值在這裡只檢查對不對，實際套用由 pickLanguage 在更早的時候完成。
func parseArgs(args []string) (options, error) {
	var o options
	havePort := false
	short := func(c rune) bool {
		switch c {
		case 'n':
			o.noKill = true
		case 'k':
			o.kill = true
		case 'f':
			o.force = true
		case 'h':
			o.help = true
		case 'v':
			o.version = true
		default:
			return false
		}
		return true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--no-kill":
			o.noKill = true
		case a == "--kill":
			o.kill = true
		case a == "--force":
			o.force = true
		case a == "--help" || a == "/?":
			o.help = true
		case a == "--version":
			o.version = true
		case a == "--debug":
			o.debug = true
		case a == "--lang":
			if i+1 >= len(args) {
				return o, errors.New(T.NeedLangValue)
			}
			i++
			if _, ok := parseLanguage(args[i]); !ok {
				return o, fmt.Errorf(T.BadLang, args[i])
			}
		case strings.HasPrefix(a, "--lang="):
			if value := strings.TrimPrefix(a, "--lang="); value == "" {
				return o, errors.New(T.NeedLangValue)
			} else if _, ok := parseLanguage(value); !ok {
				return o, fmt.Errorf(T.BadLang, value)
			}
		case strings.HasPrefix(a, "--"):
			return o, fmt.Errorf(T.UnknownFlag, a)
		case len(a) > 1 && a[0] == '-':
			for _, c := range a[1:] {
				if !short(c) {
					return o, fmt.Errorf(T.UnknownFlag, a)
				}
			}
		default:
			if havePort {
				return o, errors.New(T.OnePort)
			}
			p, err := strconv.Atoi(strings.TrimPrefix(a, ":"))
			if err != nil || p < 1 || p > 65535 {
				return o, fmt.Errorf(T.BadPort, a)
			}
			o.port, havePort = p, true
		}
	}
	if o.help || o.version {
		return o, nil
	}
	if o.noKill && (o.kill || o.force) {
		return o, errors.New(T.FlagConflict)
	}
	if !havePort {
		switch {
		case o.noKill:
			return o, fmt.Errorf(T.NeedPort, "-n")
		case o.kill:
			return o, fmt.Errorf(T.NeedPort, "-k")
		case o.force:
			return o, fmt.Errorf(T.NeedPort, "-f")
		}
	}
	return o, nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	// 語言要在解析參數之前決定，參數錯誤的訊息才會用對語言。
	system := systemLanguageTags()
	setLanguage(pickLanguage(args, os.Getenv(envLang), system))

	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, T.ErrorWithHint, err)
		return 2
	}
	initConsole()
	debugEnabled = opts.debug
	if debugEnabled {
		// 從 Windows 建立行程到程式開始執行的時間：這段若很長，延遲發生在程式之外（例如防毒軟體掃描執行檔）。
		var created, exit, kernel, user windows.Filetime
		if windows.GetProcessTimes(windows.CurrentProcess(), &created, &exit, &kernel, &user) == nil {
			debugf("process creation to program start: %v", time.Since(time.Unix(0, created.Nanoseconds())).Round(time.Millisecond))
		}
		debugf("version %s, system UI languages %v, %s=%q", version, system, envLang, os.Getenv(envLang))
	}
	switch {
	case opts.help:
		fmt.Print(T.Usage)
		return 0
	case opts.version:
		fmt.Println("wslport " + version)
		return 0
	case opts.port == 0:
		return runList()
	}
	return runPort(opts)
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, red(fmt.Sprintf(T.ErrorLine, err)))
	return 2
}

func runList() int {
	rows, notes, err := listAll()
	if err != nil {
		return fail(err)
	}
	printList(os.Stdout, rows, notes)
	return 0
}

func runPort(opts options) int {
	rep, err := lookupPort(opts.port, true)
	if err != nil {
		return fail(err)
	}
	printReport(os.Stdout, rep, time.Now())
	if !rep.Occupied() {
		return 1
	}
	if opts.noKill {
		return 0
	}
	targets := chooseTargets(rep, opts)
	if len(targets) == 0 {
		if opts.kill {
			// 指定了 -k 卻什麼都沒關：讓腳本能從結束碼看出 port 沒有被釋放。
			fmt.Fprintln(os.Stderr, yellow(T.KillNothing))
			return 2
		}
		return 0
	}
	allClosed := true
	var closed []*Owner
	began := time.Now()
	for _, o := range targets {
		if closeOwner(o, opts) {
			closed = append(closed, o)
		} else {
			allClosed = false
		}
	}
	if len(closed) > 0 {
		recheck(opts.port, closed, began)
	}
	if !allClosed {
		return 2
	}
	return 0
}

// chooseTargets 決定要關閉哪些佔用者：唯一時問 y/N（或 -k 直接關），多個時必須用編號選。
func chooseTargets(rep *Report, opts options) []*Owner {
	var killable []*Owner
	byNumber := map[string]*Owner{}
	var numbers []string
	for i, o := range rep.Owners {
		if o.Killable() {
			killable = append(killable, o)
			n := strconv.Itoa(i + 1)
			numbers = append(numbers, n)
			byNumber[n] = o
		}
	}
	if len(killable) == 0 {
		return nil
	}
	fmt.Fprintln(promptOut)
	if len(killable) == 1 {
		if opts.kill {
			return killable
		}
		if ans, _ := ask(fmt.Sprintf(T.AskKill, targetLabel(killable[0]))); isYes(ans) {
			return killable
		}
		return nil
	}
	if opts.kill {
		fmt.Fprintln(promptOut, yellow(T.MultiNoK))
	}
	ans, ok := ask(fmt.Sprintf(T.AskWhich, strings.Join(numbers, T.ListSep)))
	if !ok || ans == "" {
		return nil
	}
	if strings.EqualFold(ans, "a") {
		return killable
	}
	var chosen []*Owner
	for _, tok := range strings.FieldsFunc(ans, func(r rune) bool { return r == ',' || r == ' ' || r == listComma }) {
		o := byNumber[tok]
		if o == nil {
			fmt.Fprintf(promptOut, T.NoSuchNumber+"\n", tok)
			return nil
		}
		chosen = append(chosen, o)
	}
	return chosen
}

func closeOwner(o *Owner, opts options) bool {
	switch {
	case len(o.Containers) > 0:
		ok := true
		for _, c := range o.Containers {
			if err := stopContainer(o.Distro, c.ID); err != nil {
				fmt.Fprintln(os.Stderr, red(fmt.Sprintf(T.StopContainerFailed, c.Name, err)))
				ok = false
				continue
			}
			fmt.Println(green(fmt.Sprintf(T.ContainerStopped, c.Name)))
		}
		return ok
	case o.Where == whereWindows:
		return closeWindows(o)
	default:
		return closeWSL(o, opts)
	}
}

func closeWindows(o *Owner) bool {
	label := o.Label()
	err := killWinProcess(uint32(o.PID), o.winCreated)
	switch {
	case err == nil:
		fmt.Println(green(fmt.Sprintf(T.Killed, label)))
		return true
	case errors.Is(err, errGone):
		fmt.Printf(T.AlreadyGone+"\n", label)
		return true
	case errors.Is(err, errPIDReused):
		fmt.Fprintln(os.Stderr, red(fmt.Sprintf(T.PIDReused, o.PID)))
	case errors.Is(err, errStillAlive):
		fmt.Fprintln(os.Stderr, red(fmt.Sprintf(T.WinStillAlive, label)))
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		fmt.Fprintln(os.Stderr, red(fmt.Sprintf(T.AccessDenied, label)))
	default:
		fmt.Fprintln(os.Stderr, red(fmt.Sprintf(T.KillFailed, label, err)))
	}
	return false
}

func closeWSL(o *Owner, opts options) bool {
	label := o.Label()
	sig := "TERM"
	if opts.force {
		sig = "KILL"
	}
	for {
		status, err := killInDistro(o.Distro, o.PID, o.startTicks, sig)
		switch {
		case err != nil:
			fmt.Fprintln(os.Stderr, red(fmt.Sprintf(T.KillFailed, label, err)))
			return false
		case status == "killed":
			fmt.Println(green(fmt.Sprintf(T.Killed, label)))
			return true
		case status == "gone":
			fmt.Printf(T.AlreadyGone+"\n", label)
			return true
		case status == "mismatch":
			fmt.Fprintln(os.Stderr, red(fmt.Sprintf(T.PIDReused, o.PID)))
			return false
		case sig == "KILL":
			fmt.Fprintln(os.Stderr, red(fmt.Sprintf(T.SigkillFailed, label)))
			return false
		}
		// 送了 SIGTERM 卻還活著。
		fmt.Fprintln(os.Stderr, yellow(fmt.Sprintf(T.NoSigterm, label)))
		if opts.kill {
			fmt.Fprintln(os.Stderr, T.UseForce)
			return false
		}
		if ans, _ := ask(T.AskSigkill); !isYes(ans) {
			return false
		}
		sig = "KILL"
	}
}

// aftermath 是關閉之後、port 上還在的佔用者屬於哪一種情況。
type aftermath int

const (
	stillThere     aftermath = iota // 原本就在的另一個佔用者
	leftoverWorker                  // 被關掉的主行程留下的子行程
	respawned                       // 關閉之後才啟動的同名行程：有監督程式在重啟它
)

// classifyAfter 判斷關閉後仍佔用 port 的行程 o 是怎麼來的。began 是開始關閉的時間。
func classifyAfter(o *Owner, closed []*Owner, began time.Time) aftermath {
	for _, c := range closed {
		if o.Where != c.Where || o.Distro != c.Distro {
			continue
		}
		for _, pid := range c.Workers {
			if pid == o.PID {
				return leftoverWorker
			}
		}
		if o.Name == c.Name && o.PID != c.PID && o.Started.After(began) {
			return respawned
		}
	}
	return stillThere
}

func managedByPM2(o *Owner) bool {
	for _, a := range o.Ancestors {
		if strings.Contains(strings.ToLower(a.Name+" "+a.Cmd), "pm2") {
			return true
		}
	}
	return false
}

// recheck 在關閉後再查一次，回報 port 是否真的釋放了。
func recheck(port int, closed []*Owner, began time.Time) {
	time.Sleep(700 * time.Millisecond)
	rep, err := lookupPort(port, false)
	if err != nil {
		return
	}
	relayLingers := false
	var again []*Owner
	for _, o := range rep.Owners {
		if o.Relay {
			relayLingers = true
			continue
		}
		again = append(again, o)
	}
	if len(again) == 0 && len(rep.Orphans) == 0 {
		msg := fmt.Sprintf(T.Released, port)
		if relayLingers {
			msg += T.RelayLingers
		}
		fmt.Println(green(msg))
		return
	}
	for _, o := range again {
		switch classifyAfter(o, closed, began) {
		case leftoverWorker:
			fmt.Println(yellow(fmt.Sprintf(T.LeftoverWorker, o.Label(), port)))
		case respawned:
			fmt.Println(yellow(fmt.Sprintf(T.Respawned, port, who(o, o.Label()))))
			switch {
			case o.Unit != "":
				fmt.Printf(T.HintSystemd+"\n", o.Unit)
			case o.Service:
				fmt.Println(T.HintWinService)
			case managedByPM2(o):
				fmt.Println(T.HintPM2)
			case len(o.Ancestors) > 0:
				parent := o.Ancestors[0]
				fmt.Printf(T.HintParent+"\n", clipMiddle(parent.Text(), 90), parent.PID)
			}
		default:
			fmt.Println(yellow(fmt.Sprintf(T.StillHeld, port, who(o, o.Label()))))
		}
	}
}
