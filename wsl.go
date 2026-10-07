package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
)

//go:embed probe.sh
var probeScript string

//go:embed kill.sh
var killScript string

const probePrefix = "@wslport\t"

// createNoWindow 讓子行程不要另開主控台視窗（從 WSL 經管線啟動時我們自己沒有主控台）。
const createNoWindow = 0x08000000

type distroInfo struct {
	Name    string
	Running bool
	Version int
}

// wslSock 是 distro 內 ss／netstat 的一列監聽者。
type wslSock struct {
	Local string // 原樣的「位址:port」，例如 127.0.0.53%lo:53、*:3000、[::]:3000
	Addr  string
	Port  int
	PIDs  []int // 空的表示這個 distro 看不到擁有者（屬於別的 PID 命名空間）
}

type wslProc struct {
	PID        int
	PPID       int
	Comm       string
	User       string
	Cwd        string
	Exe        string
	Cmd        string
	Cgroup     string
	StartTicks string
	Ancestors  []procRef // 由近到遠，不含 PID 1
}

// procRef 是祖先行程的簡要資訊。
type procRef struct {
	PID  int
	Name string
	Cmd  string
}

// Text 回傳顯示用的文字：有指令列就用指令列，否則用名稱。
func (r procRef) Text() string {
	if r.Cmd != "" {
		return r.Cmd
	}
	return r.Name
}

type probeResult struct {
	Distro string
	Mode   string
	NoTool bool
	Socks  []wslSock
	Procs  map[int]*wslProc
	Uptime float64
	ClkTck float64
	Err    error
}

func systemRoot() string {
	if r := os.Getenv("SystemRoot"); r != "" {
		return r
	}
	return `C:\Windows`
}

func system32(name string) string {
	return filepath.Join(systemRoot(), "System32", name)
}

// decodeWSLText 解碼 wsl.exe 自己印出的文字：預設是 UTF-16LE，設了 WSL_UTF8=1 才是 UTF-8。
func decodeWSLText(b []byte) string {
	if bytes.IndexByte(b, 0) < 0 {
		return strings.TrimPrefix(string(b), "\ufeff")
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return strings.TrimPrefix(string(utf16.Decode(u)), "\ufeff")
}

// parseDistroList 解析 `wsl.exe -l -v`。標題列可能被在地化，所以只認「最後一欄是數字」的列。
func parseDistroList(text string) []distroInfo {
	var list []distroInfo
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if len(f) < 3 {
			continue
		}
		ver, err := strconv.Atoi(f[len(f)-1])
		if err != nil {
			continue
		}
		list = append(list, distroInfo{
			Name:    strings.Join(f[:len(f)-2], " "),
			Running: f[len(f)-2] == "Running",
			Version: ver,
		})
	}
	return list
}

// runSystem 執行 System32 底下的工具，不開視窗，工作目錄固定在 Windows 目錄
// （從 WSL 啟動時目前目錄可能是 \\wsl.localhost\… 的 UNC 路徑）。
func runSystem(ctx context.Context, stdin string, exe string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = systemRoot()
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	cmd.Stdin = strings.NewReader(stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return out.Bytes(), err
}

// listDistros 回傳所有 distro；沒有安裝 WSL 時回傳 nil, nil。
func listDistros() ([]distroInfo, error) {
	wsl := system32("wsl.exe")
	if _, err := os.Stat(wsl); err != nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := runSystem(ctx, "", wsl, "-l", "-v")
	list := parseDistroList(decodeWSLText(out))
	if err != nil && len(list) == 0 {
		// 沒有任何 distro 時 wsl.exe 會以非零結束碼印出說明，這不算錯誤。
		return nil, nil
	}
	return list, nil
}

// lfOnly 去掉 CR：腳本若帶 CRLF，sh 會把 \r 當成指令的一部分。
func lfOnly(s string) string {
	return strings.ReplaceAll(s, "\r", "")
}

func runInDistro(distro, script string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// -e 會把後面的參數原樣交給程式；腳本走 stdin，避開 wsl.exe 的引號處理。
	full := append([]string{"-d", distro, "-u", "root", "-e", "sh", "-s", "--"}, args...)
	return runSystem(ctx, lfOnly(script), system32("wsl.exe"), full...)
}

var ssPIDRe = regexp.MustCompile(`pid=(\d+)`)

// splitLocal 把「位址:port」拆開，去掉方括號與 %介面 後綴。
func splitLocal(local string) (addr string, port int, ok bool) {
	i := strings.LastIndex(local, ":")
	if i < 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(local[i+1:])
	if err != nil {
		return "", 0, false
	}
	// netstat 把 IPv6 萬用位址印成 ":::3000"，拆開後剩下 "::"。
	addr = local[:i]
	if j := strings.Index(addr, "%"); j >= 0 {
		addr = addr[:j]
	}
	return strings.Trim(addr, "[]"), port, true
}

// parseSockLine 解析 ss 或 netstat 的一列監聽者。
func parseSockLine(line string) (wslSock, bool) {
	f := strings.Fields(line)
	if len(f) < 5 {
		return wslSock{}, false
	}
	addr, port, ok := splitLocal(f[3])
	if !ok {
		return wslSock{}, false
	}
	s := wslSock{Local: f[3], Addr: addr, Port: port}
	seen := map[int]bool{}
	add := func(v string) {
		if pid, err := strconv.Atoi(v); err == nil && pid > 0 && !seen[pid] {
			seen[pid] = true
			s.PIDs = append(s.PIDs, pid)
		}
	}
	if strings.HasPrefix(f[0], "tcp") {
		// netstat：第 7 欄是 "PID/程式名"，沒有權限時是 "-"。
		if len(f) >= 7 {
			if i := strings.Index(f[6], "/"); i > 0 {
				add(f[6][:i])
			}
		}
	} else {
		for _, m := range ssPIDRe.FindAllStringSubmatch(line, -1) {
			add(m[1])
		}
	}
	sort.Ints(s.PIDs)
	return s, true
}

// parseProbe 解析 probe.sh 的輸出，沒有前綴的行（例如 wsl.exe 的警告）一律忽略。
func parseProbe(distro, out string) probeResult {
	pr := probeResult{Distro: distro, Procs: map[int]*wslProc{}, ClkTck: 100}
	seen := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, probePrefix) {
			continue
		}
		seen = true
		rest := line[len(probePrefix):]
		kind, val, _ := strings.Cut(rest, "\t")
		switch kind {
		case "mode":
			pr.Mode = strings.TrimSpace(val)
		case "uptime":
			pr.Uptime, _ = strconv.ParseFloat(strings.TrimSpace(val), 64)
		case "clk":
			if v, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil && v > 0 {
				pr.ClkTck = v
			}
		case "notool":
			pr.NoTool = true
		case "sock":
			if s, ok := parseSockLine(val); ok {
				pr.Socks = append(pr.Socks, s)
			}
		case "proc":
			f := strings.SplitN(val, "\t", 3)
			if len(f) < 3 {
				continue
			}
			pid, err := strconv.Atoi(f[0])
			if err != nil {
				continue
			}
			p := pr.Procs[pid]
			if p == nil {
				p = &wslProc{PID: pid}
				pr.Procs[pid] = p
			}
			v := strings.TrimSpace(f[2])
			switch f[1] {
			case "comm":
				p.Comm = v
			case "user":
				p.User = v
			case "ppid":
				p.PPID, _ = strconv.Atoi(v)
			case "start":
				p.StartTicks = v
			case "cwd":
				p.Cwd = v
			case "exe":
				p.Exe = v
			case "cgroup":
				p.Cgroup = v
			case "cmd":
				p.Cmd = v
			case "anc":
				a := strings.SplitN(v, "\t", 3)
				if apid, err := strconv.Atoi(a[0]); err == nil && len(a) >= 2 {
					ref := procRef{PID: apid, Name: strings.TrimSpace(a[1])}
					if len(a) == 3 {
						ref.Cmd = strings.TrimSpace(a[2])
					}
					p.Ancestors = append(p.Ancestors, ref)
				}
			}
		}
	}
	if !seen {
		pr.Err = errors.New(firstLine(decodeWSLText([]byte(out))))
	}
	return pr
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return msgNoOutput
}

// started 由 uptime 與 starttime 推算啟動時間，不受 WSL 時鐘漂移影響。
func (pr *probeResult) started(p *wslProc, now time.Time) time.Time {
	ticks, err := strconv.ParseFloat(p.StartTicks, 64)
	if err != nil || pr.Uptime <= 0 {
		return time.Time{}
	}
	elapsed := pr.Uptime - ticks/pr.ClkTck
	if elapsed < 0 {
		elapsed = 0
	}
	return now.Add(-time.Duration(elapsed * float64(time.Second)))
}

// probeDistros 並行探測所有執行中的 WSL2 distro。port 為 0 表示全部監聽者。
// 絕不啟動已停止的 distro。
func probeDistros(port int) (results []probeResult, wsl1 []string) {
	began := time.Now()
	distros, _ := listDistros()
	debugf("wsl.exe -l -v：%d 個 distro，%v", len(distros), time.Since(began).Round(time.Millisecond))
	var targets []string
	for _, d := range distros {
		switch {
		case !d.Running:
		case strings.HasPrefix(d.Name, "docker-desktop"):
			// Docker Desktop 自己的 distro 沒有使用者的行程，容器另外用 docker 查。
		case d.Version == 1:
			wsl1 = append(wsl1, d.Name)
		default:
			targets = append(targets, d.Name)
		}
	}
	results = make([]probeResult, len(targets))
	var wg sync.WaitGroup
	for i, name := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			began := time.Now()
			out, err := runInDistro(name, probeScript, 5*time.Second, strconv.Itoa(port))
			pr := parseProbe(name, string(out))
			if errors.Is(err, context.DeadlineExceeded) {
				pr.Err = errors.New(msgProbeTimeout)
			}
			debugf("探測 %s：%v，模式 %q，%d 個監聽者，%d 個行程，錯誤 %v",
				name, time.Since(began).Round(time.Millisecond), pr.Mode, len(pr.Socks), len(pr.Procs), pr.Err)
			results[i] = pr
		}()
	}
	wg.Wait()
	return results, wsl1
}

// killInDistro 送出訊號並回報 killed／alive／gone／mismatch。
func killInDistro(distro string, pid int, startTicks, sig string) (string, error) {
	out, err := runInDistro(distro, killScript, 10*time.Second, strconv.Itoa(pid), startTicks, sig)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, probePrefix) {
			return strings.TrimSpace(line[len(probePrefix):]), nil
		}
	}
	if err == nil {
		err = errors.New(firstLine(decodeWSLText(out)))
	}
	return "", err
}

// displayName 挑一個認得出來的行程名稱。
// comm 其實是「主執行緒的名稱」，執行環境可以改掉它：Node 24 會把它設成 MainThread，
// 設了 process.title 的程式則會變成被截斷的標題。comm 對不上執行檔與指令列時，改用執行檔名稱。
func displayName(comm, exe, cmd string) string {
	exeBase := ""
	if exe != "" {
		exeBase = pathBase(strings.TrimSuffix(exe, " (deleted)"))
	}
	if comm == "" {
		if exeBase != "" {
			return exeBase
		}
		return "?"
	}
	if exeBase == "" {
		return comm
	}
	candidates := []string{exeBase}
	if f := strings.Fields(cmd); len(f) > 0 {
		// argv[0] 與 argv[1]：直譯器執行的腳本（gunicorn、uvicorn）名稱在 argv[1]。
		for _, arg := range f[:min(len(f), 2)] {
			candidates = append(candidates, pathBase(arg))
		}
	}
	for _, c := range candidates {
		// comm 最長 15 個字元，較長的名稱會被截斷。
		if c == comm || (len(comm) == 15 && strings.HasPrefix(c, comm)) {
			return c
		}
	}
	return exeBase
}

func pathBase(p string) string {
	return p[strings.LastIndex(p, "/")+1:]
}

// isWSLInit 回報這個祖先是不是 WSL 自己的 init 行程（/init、SessionLeader、Relay），
// 它們對使用者沒有意義，祖先鏈到這裡就停。
func isWSLInit(r procRef) bool {
	if r.Cmd == "/init" || strings.HasPrefix(r.Cmd, "/init ") {
		return true
	}
	return r.Name == "init" || r.Name == "SessionLeader" ||
		strings.HasPrefix(r.Name, "Relay(") || strings.HasPrefix(r.Name, "init(")
}

// maxAncestors 是顯示的祖先層數上限。
const maxAncestors = 3

// userAncestors 去掉 WSL 的 init 行程，並限制層數。
func userAncestors(chain []procRef) []procRef {
	var out []procRef
	for _, r := range chain {
		if isWSLInit(r) || len(out) == maxAncestors {
			break
		}
		out = append(out, r)
	}
	return out
}

// relayEligible 回報 NAT 模式下 wslrelay 會不會把這個位址轉送到 Windows 的 localhost。
func relayEligible(addr string) bool {
	switch addr {
	case "0.0.0.0", "127.0.0.1", "*", "::", "::1":
		return true
	}
	return false
}

// unitFromCgroup 從 cgroup 路徑取出 systemd 服務單位名稱。
// 只看最末一層：行程若在 user@1000.service 底下的 scope 裡，並不是由該服務直接管理。
func unitFromCgroup(cg string) string {
	for _, line := range strings.Split(cg, ";") {
		path := line[strings.LastIndex(line, ":")+1:]
		leaf := path[strings.LastIndex(path, "/")+1:]
		if strings.HasSuffix(leaf, ".service") {
			return leaf
		}
	}
	return ""
}

func inContainerCgroup(cg string) bool {
	for _, k := range []string{"docker", "containerd", "kubepods", "libpod"} {
		if strings.Contains(cg, k) {
			return true
		}
	}
	return false
}
