package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func utf16le(s string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

const distroListText = "  NAME            STATE           VERSION\r\n" +
	"* Ubuntu-26.04    Running         2\r\n" +
	"  Debian          Stopped         2\r\n" +
	"  legacy          Running         1\r\n" +
	"  docker-desktop  Running         2\r\n"

func TestParseDistroList(t *testing.T) {
	want := []distroInfo{
		{Name: "Ubuntu-26.04", Running: true, Version: 2},
		{Name: "Debian", Running: false, Version: 2},
		{Name: "legacy", Running: true, Version: 1},
		{Name: "docker-desktop", Running: true, Version: 2},
	}
	// wsl.exe 預設輸出 UTF-16LE，設了 WSL_UTF8=1 才是 UTF-8，兩種都要能解。
	for name, raw := range map[string][]byte{"utf8": []byte(distroListText), "utf16le": utf16le(distroListText)} {
		if got := parseDistroList(decodeWSLText(raw)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v", name, got)
		}
	}
}

// 標題列被在地化、或 wsl.exe 只印出一句說明時，不可以誤認成 distro。
func TestParseDistroListIgnoresText(t *testing.T) {
	text := "  名稱            狀態            版本\n沒有已安裝的發佈版本。\n"
	if got := parseDistroList(text); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestSplitLocal(t *testing.T) {
	cases := []struct {
		in   string
		addr string
		port int
	}{
		{"0.0.0.0:3000", "0.0.0.0", 3000},
		{"127.0.0.53%lo:53", "127.0.0.53", 53},
		{"*:8080", "*", 8080},
		{"[::]:3000", "::", 3000},
		{"[::1]:5173", "::1", 5173},
		{":::3000", "::", 3000},
		{"[fe80::1]%eth0:22", "fe80::1", 22},
	}
	for _, c := range cases {
		addr, port, ok := splitLocal(c.in)
		if !ok || addr != c.addr || port != c.port {
			t.Errorf("splitLocal(%q) = %q, %d, %v; want %q, %d", c.in, addr, port, ok, c.addr, c.port)
		}
	}
	if _, _, ok := splitLocal("Address:Port"); ok {
		t.Error("header text must not parse as an address")
	}
}

func TestParseSockLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want wslSock
	}{
		{
			name: "ss with one owner",
			line: `LISTEN 0      4096    127.0.0.53%lo:53        0.0.0.0:*    users:(("systemd-resolve",pid=79,fd=17))`,
			want: wslSock{Local: "127.0.0.53%lo:53", Addr: "127.0.0.53", Port: 53, PIDs: []int{79}},
		},
		{
			name: "ss without owner belongs to another PID namespace",
			line: `LISTEN 0      1000   10.255.255.254:53        0.0.0.0:*`,
			want: wslSock{Local: "10.255.255.254:53", Addr: "10.255.255.254", Port: 53},
		},
		{
			name: "ss with master and workers sharing the socket",
			line: `LISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:(("nginx",pid=412,fd=6),("nginx",pid=411,fd=6),("nginx",pid=410,fd=6))`,
			want: wslSock{Local: "0.0.0.0:80", Addr: "0.0.0.0", Port: 80, PIDs: []int{410, 411, 412}},
		},
		{
			name: "netstat",
			line: `tcp        0      0 127.0.0.1:3000          0.0.0.0:*               LISTEN      1234/node`,
			want: wslSock{Local: "127.0.0.1:3000", Addr: "127.0.0.1", Port: 3000, PIDs: []int{1234}},
		},
		{
			name: "netstat tcp6 without permission",
			line: `tcp6       0      0 :::3000                 :::*                    LISTEN      -`,
			want: wslSock{Local: ":::3000", Addr: "::", Port: 3000},
		},
	}
	for _, c := range cases {
		got, ok := parseSockLine(c.line)
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v (ok=%v), want %+v", c.name, got, ok, c.want)
		}
	}
}

const probeSample = "wsl: 偵測到 localhost Proxy 設定，但未鏡像到 WSL。\r\n" +
	"@wslport\tmode\tnat\r\n" +
	"@wslport\tuptime\t1000.50\r\n" +
	"@wslport\tclk\t100\r\n" +
	"@wslport\ttool\tss\r\n" +
	"@wslport\tsock\tLISTEN 0 511 *:3000 *:* users:((\"node\",pid=4321,fd=19))\r\n" +
	"@wslport\tproc\t4321\tcomm\tnode\r\n" +
	"@wslport\tproc\t4321\tuser\tuser\r\n" +
	"@wslport\tproc\t4321\tppid\t4300\r\n" +
	"@wslport\tproc\t4321\tstart\t90000\r\n" +
	"@wslport\tproc\t4321\tcwd\t/home/user/my app\r\n" +
	"@wslport\tproc\t4321\texe\t/usr/bin/node\r\n" +
	"@wslport\tproc\t4321\tcgroup\t0::/user.slice/user-1000.slice/session-3.scope;\r\n" +
	"@wslport\tproc\t4321\tcmd\tnext-server (v15.1.0) \r\n" +
	"@wslport\tproc\t4321\tanc\t4300\tsh\tsh -c next dev \r\n" +
	"@wslport\tproc\t4321\tanc\t4290\tnpm run dev\tnpm run dev \r\n" +
	"@wslport\tproc\t4321\tanc\t500\tbash\t-bash \r\n" +
	"@wslport\tproc\t4321\tanc\t499\tRelay(500)\t/init \r\n" +
	"@wslport\tproc\t4321\tanc\t7\tkthread\t\r\n"

func TestParseProbe(t *testing.T) {
	pr := parseProbe("Ubuntu", probeSample)
	if pr.Err != nil || pr.NoTool {
		t.Fatalf("unexpected failure: %+v", pr)
	}
	if pr.Mode != "nat" || pr.Uptime != 1000.5 || pr.ClkTck != 100 {
		t.Errorf("header = %q %v %v", pr.Mode, pr.Uptime, pr.ClkTck)
	}
	if len(pr.Socks) != 1 || pr.Socks[0].Port != 3000 || pr.Socks[0].Addr != "*" {
		t.Fatalf("socks = %+v", pr.Socks)
	}
	want := &wslProc{
		PID: 4321, PPID: 4300, Comm: "node", User: "user",
		Cwd: "/home/user/my app", Exe: "/usr/bin/node", Cmd: "next-server (v15.1.0)",
		Cgroup: "0::/user.slice/user-1000.slice/session-3.scope;", StartTicks: "90000",
		Ancestors: []procRef{
			{PID: 4300, Name: "sh", Cmd: "sh -c next dev"},
			{PID: 4290, Name: "npm run dev", Cmd: "npm run dev"},
			{PID: 500, Name: "bash", Cmd: "-bash"},
			{PID: 499, Name: "Relay(500)", Cmd: "/init"},
			{PID: 7, Name: "kthread"}, // 核心執行緒沒有指令列
		},
	}
	if got := pr.Procs[4321]; !reflect.DeepEqual(got, want) {
		t.Errorf("proc = %+v", got)
	}

	// uptime 1000.5 秒、starttime 90000 ticks（900 秒）→ 已執行 100.5 秒。
	now := time.Unix(1_800_000_000, 0)
	if got := now.Sub(pr.started(pr.Procs[4321], now)); got != 100500*time.Millisecond {
		t.Errorf("elapsed = %v", got)
	}
}

// 祖先鏈遇到 WSL 自己的 init 行程就停，而且最多顯示三層。
func TestUserAncestors(t *testing.T) {
	pr := parseProbe("Ubuntu", probeSample)
	got := userAncestors(pr.Procs[4321].Ancestors)
	want := []procRef{
		{PID: 4300, Name: "sh", Cmd: "sh -c next dev"},
		{PID: 4290, Name: "npm run dev", Cmd: "npm run dev"},
		{PID: 500, Name: "bash", Cmd: "-bash"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	// 背景工作被 /init 收養：沒有任何值得顯示的祖先。
	if got := userAncestors([]procRef{{PID: 14476, Name: "SessionLeader", Cmd: "/init"}, {PID: 2, Name: "init"}}); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
	long := []procRef{{PID: 5, Name: "a"}, {PID: 4, Name: "b"}, {PID: 3, Name: "c"}, {PID: 2, Name: "d"}}
	if got := userAncestors(long); len(got) != maxAncestors {
		t.Errorf("got %d ancestors, want %d", len(got), maxAncestors)
	}
	if (procRef{Name: "kthread"}).Text() != "kthread" || (procRef{Name: "sh", Cmd: "sh -c x"}).Text() != "sh -c x" {
		t.Error("Text must prefer the command line and fall back to the name")
	}
}

func TestDisplayName(t *testing.T) {
	cases := []struct {
		comm, exe, cmd string
		want           string
	}{
		// Node 24 把主執行緒命名為 MainThread，/proc/PID/comm 與 ss 都只看得到這個名字。
		{"MainThread", "/home/user/.local/share/fnm/node-versions/v24.21.0/installation/bin/node", "node server.js", "node"},
		// process.title 被改掉（Next.js）：comm 是截斷的標題，名稱用執行檔，標題留在指令欄。
		{"next-server (v1", "/usr/bin/node", "next-server (v15.1.0)", "node"},
		{"python3", "/usr/bin/python3.14", "python3 -m http.server 3000", "python3"},
		// 直譯器執行的腳本：comm 是腳本名稱，比 python3.14 更認得出來。
		{"gunicorn", "/usr/bin/python3.14", "/usr/bin/python3 /usr/bin/gunicorn app:app", "gunicorn"},
		// comm 最長 15 個字元：補回完整名稱。
		{"systemd-resolve", "/usr/lib/systemd/systemd-resolved", "/usr/lib/systemd/systemd-resolved", "systemd-resolved"},
		{"nginx", "/usr/sbin/nginx", "nginx: master process /usr/sbin/nginx -g daemon on;", "nginx"},
		{"node", "/usr/bin/node (deleted)", "node server.js", "node"},
		// 讀不到執行檔（核心執行緒、權限不足）時只能用 comm。
		{"kworker/0:1", "", "", "kworker/0:1"},
		{"", "/usr/bin/redis-server", "", "redis-server"},
		{"", "", "", "?"},
	}
	for _, c := range cases {
		if got := displayName(c.comm, c.exe, c.cmd); got != c.want {
			t.Errorf("displayName(%q, %q, %q) = %q, want %q", c.comm, c.exe, c.cmd, got, c.want)
		}
	}
}

func TestParseProbeFailures(t *testing.T) {
	pr := parseProbe("Ubuntu", "@wslport\tmode\tnat\n@wslport\tnotool\n")
	if !pr.NoTool || pr.Err != nil {
		t.Errorf("notool: %+v", pr)
	}
	// 沒有任何帶前綴的行，表示腳本根本沒跑起來：把 wsl.exe 的訊息當成錯誤。
	pr = parseProbe("Ubuntu", string(utf16le("找不到具有所提供名稱的發佈版本。\r\n")))
	if pr.Err == nil || !strings.Contains(pr.Err.Error(), "找不到") {
		t.Errorf("err = %v", pr.Err)
	}
}

func TestUnitFromCgroup(t *testing.T) {
	cases := map[string]string{
		"0::/system.slice/nginx.service;":                                         "nginx.service",
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/web.service;": "web.service",
		// 在使用者管理員底下的 scope 裡：不是由某個服務直接管理。
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/run-1.scope;": "",
		"0::/user.slice/user-1000.slice/session-3.scope;":                         "",
		"": "",
	}
	for in, want := range cases {
		if got := unitFromCgroup(in); got != want {
			t.Errorf("unitFromCgroup(%q) = %q, want %q", in, got, want)
		}
	}
	if !inContainerCgroup("0::/system.slice/docker-abc123.scope;") || inContainerCgroup("0::/system.slice/nginx.service;") {
		t.Error("inContainerCgroup misclassified")
	}
}

func TestLFOnly(t *testing.T) {
	if got := lfOnly("a\r\nthen\r\n"); got != "a\nthen\n" {
		t.Errorf("lfOnly = %q", got)
	}
	// 內嵌的腳本送進 sh 之前不可以帶 CR。
	for name, s := range map[string]string{"probe": probeScript, "kill": killScript} {
		if strings.Contains(lfOnly(s), "\r") {
			t.Errorf("%s script still contains CR", name)
		}
		if !strings.Contains(s, `main "$@" </dev/null`) {
			t.Errorf("%s script must run inside main with stdin closed", name)
		}
	}
}
