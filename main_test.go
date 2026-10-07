package main

import (
	"bufio"
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseArgs(t *testing.T) {
	ok := []struct {
		args []string
		want options
	}{
		{nil, options{}},
		{[]string{"3000"}, options{port: 3000}},
		{[]string{":3000"}, options{port: 3000}},
		// 旗標可以放在 port 後面。
		{[]string{"3000", "-n"}, options{port: 3000, noKill: true}},
		{[]string{"--kill", "8080"}, options{port: 8080, kill: true}},
		{[]string{"3000", "-kf"}, options{port: 3000, kill: true, force: true}},
		{[]string{"3000", "--force"}, options{port: 3000, force: true}},
		{[]string{"-h"}, options{help: true}},
		{[]string{"--version"}, options{version: true}},
		{[]string{"3000", "--debug", "-n"}, options{port: 3000, debug: true, noKill: true}},
		{[]string{"65535"}, options{port: 65535}},
	}
	for _, c := range ok {
		got, err := parseArgs(c.args)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseArgs(%v) = %+v, %v; want %+v", c.args, got, err, c.want)
		}
	}
	bad := [][]string{
		{"0"}, {"65536"}, {"abc"}, {"-3000"}, {"3000", "3001"},
		{"3000", "--nope"}, {"3000", "-x"}, {"3000", "-n", "-k"}, {"-k"}, {"-n"}, {"-f"},
	}
	for _, args := range bad {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%v) should fail", args)
		}
	}
}

func TestClassifyAfter(t *testing.T) {
	began := testNow
	master := &Owner{Where: whereWSL, Distro: "Ubuntu", PID: 410, Name: "nginx", Workers: []int{411, 412}}
	closed := []*Owner{master}
	cases := []struct {
		name string
		o    *Owner
		want aftermath
	}{
		{"worker left behind by the killed master", &Owner{Where: whereWSL, Distro: "Ubuntu", PID: 411, Name: "nginx", Started: began.Add(-time.Hour)}, leftoverWorker},
		{"same name started after the kill", &Owner{Where: whereWSL, Distro: "Ubuntu", PID: 900, Name: "nginx", Started: began.Add(time.Second)}, respawned},
		// 同名但早就在跑的另一個行程（SO_REUSEPORT），不是被重新啟動的。
		{"same name already running before", &Owner{Where: whereWSL, Distro: "Ubuntu", PID: 300, Name: "nginx", Started: began.Add(-time.Hour)}, stillThere},
		{"same name in another distro", &Owner{Where: whereWSL, Distro: "Debian", PID: 900, Name: "nginx", Started: began.Add(time.Second)}, stillThere},
		{"different program", &Owner{Where: whereWindows, PID: 5, Name: "node.exe", Started: began.Add(time.Second)}, stillThere},
		{"unknown start time", &Owner{Where: whereWSL, Distro: "Ubuntu", PID: 901, Name: "nginx"}, stillThere},
	}
	for _, c := range cases {
		if got := classifyAfter(c.o, closed, began); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

// withInput 把提示的輸入換成固定內容，並把提示文字丟掉。
func withInput(t *testing.T, input string) {
	t.Helper()
	oldIn, oldOut := stdin, promptOut
	stdin, promptOut = bufio.NewReader(strings.NewReader(input)), io.Discard
	t.Cleanup(func() { stdin, promptOut = oldIn, oldOut })
}

func TestChooseTargets(t *testing.T) {
	node := &Owner{Where: whereWindows, PID: 1111, Name: "node.exe"}
	system := winOwner(4, "System")
	wsl := &Owner{Where: whereWSL, Distro: "Ubuntu", PID: 4321, Name: "node"}
	pick := func(rep *Report, opts options, input string) []string {
		withInput(t, input)
		return labels(chooseTargets(rep, opts))
	}
	single := &Report{Port: 3000, Owners: []*Owner{node}}
	both := &Report{Port: 3000, Owners: []*Owner{node, system, wsl}}
	nodeLabel, wslLabel := "windows::node.exe (PID 1111)", "wsl:Ubuntu:node (PID 4321)"

	cases := []struct {
		name  string
		rep   *Report
		opts  options
		input string
		want  []string
	}{
		{"single owner, yes", single, options{}, "y\n", []string{nodeLabel}},
		{"single owner, Enter means no", single, options{}, "\n", nil},
		{"single owner, anything else means no", single, options{}, "maybe\n", nil},
		// 沒有可互動的輸入（管線、排程）：一律視為否。
		{"single owner, EOF means no", single, options{}, "", nil},
		{"single owner, -k skips the prompt", single, options{kill: true}, "", []string{nodeLabel}},
		// 編號對應畫面上的 [1] [2] [3]；受保護的 [2] 不能選。
		{"several owners, pick by number", both, options{}, "3\n", []string{wslLabel}},
		{"several owners, pick two", both, options{}, "1, 3\n", []string{nodeLabel, wslLabel}},
		{"several owners, all", both, options{}, "a\n", []string{nodeLabel, wslLabel}},
		{"several owners, protected number is refused", both, options{}, "2\n", nil},
		{"several owners, unknown number cancels everything", both, options{}, "1 9\n", nil},
		{"several owners, Enter cancels", both, options{}, "\n", nil},
		// -k 在佔用者不只一個時不可以自己挑一個關。
		{"several owners, -k still has to choose", both, options{kill: true}, "", nil},
		{"only protected owners", &Report{Port: 445, Owners: []*Owner{system}}, options{kill: true}, "y\n", nil},
	}
	for _, c := range cases {
		if got := pick(c.rep, c.opts, c.input); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClipMiddle(t *testing.T) {
	short := "node server.js"
	if got := clipMiddle(short, 90); got != short {
		t.Errorf("short text changed: %q", got)
	}
	// Windows 上的 npm：重點在最後面的 run dev，從中間截才不會被切掉。
	long := `"C:\Program Files\nodejs\node.exe" "C:\Program Files\nodejs\node_modules\npm\bin\npm-cli.js" run dev`
	got := clipMiddle(long, 60)
	if widths.StringWidth(got) != 60 || !strings.HasPrefix(got, `"C:\Program Files`) || !strings.HasSuffix(got, "run dev") || !strings.Contains(got, "…") {
		t.Errorf("clipMiddle = %q (width %d)", got, widths.StringWidth(got))
	}
	// 全形字佔兩欄，不可以切在字的中間。
	wide := strings.Repeat("專案", 40)
	if got := clipMiddle(wide, 21); widths.StringWidth(got) > 21 || !strings.Contains(got, "…") {
		t.Errorf("clipMiddle(wide) = %q (width %d)", got, widths.StringWidth(got))
	}
}

func TestParseDockerPS(t *testing.T) {
	out := "abc123|web|nginx:latest\r\n\r\ndef456|db-1|postgres:16\n"
	want := []container{{"abc123", "web", "nginx:latest"}, {"def456", "db-1", "postgres:16"}}
	if got := parseDockerPS(out); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	// docker 沒在跑時印的是錯誤訊息，不能被當成容器。
	if got := parseDockerPS("Cannot connect to the Docker daemon at unix:///var/run/docker.sock.\n"); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
	if got := strings.Join(dockerPSArgs(3000), " "); got != "ps --filter publish=3000 --format {{.ID}}|{{.Names}}|{{.Image}}" {
		t.Errorf("args = %q", got)
	}
}

func TestHumanSince(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := map[time.Duration]string{
		2 * time.Second:  "剛剛",
		40 * time.Second: "40 秒前",
		5 * time.Minute:  "5 分鐘前",
		2 * time.Hour:    "2 小時前",
		72 * time.Hour:   "3 天前",
	}
	for d, want := range cases {
		if got := humanSince(now.Add(-d), now); got != want {
			t.Errorf("humanSince(%v) = %q, want %q", d, got, want)
		}
	}
}

func render(rep *Report) string {
	var b bytes.Buffer
	printReport(&b, rep, testNow)
	return b.String()
}

func TestPrintReportWording(t *testing.T) {
	wslNode := func(mode string) []probeResult {
		return []probeResult{probe("Ubuntu", mode, []*wslProc{{PID: 4321, PPID: 4300, Comm: "node", Cmd: "next-server", Cwd: "/home/user/app", Ancestors: []procRef{{PID: 4300, Name: "sh", Cmd: "sh -c next dev"}, {PID: 4290, Name: "npm", Cmd: "npm run dev"}}}}, sock("*:3000", 4321))}
	}

	// NAT：relay 降為附註，主角是 distro 裡的行程。
	out := render(mergeReport(3000, []*Owner{winOwner(9876, "wslrelay.exe", "127.0.0.1:3000")}, wslNode("nat"), testNow))
	for _, want := range []string{"Port 3000 → WSL「Ubuntu」裡的 node (PID 4321)", "/home/user/app", "sh -c next dev (PID 4300)", "← npm run dev (PID 4290)", "wslrelay.exe (PID 9876) 只是轉送"} {
		if !strings.Contains(out, want) {
			t.Errorf("NAT report missing %q:\n%s", want, out)
		}
	}

	// mirrored：Windows 端沒有對應的列是正常的，不該提「沒有轉送」。
	rep := mergeReport(3000, nil, wslNode("mirrored"), testNow)
	if out := render(rep); strings.Contains(out, "轉送") {
		t.Errorf("mirrored report must not mention relaying:\n%s", out)
	}

	// NAT 且 Windows 端沒有 relay：port 在保留範圍內時要說明 Windows 無法轉送，並附上解法。
	rep = mergeReport(2000, nil, []probeResult{probe("Ubuntu", "nat", []*wslProc{{PID: 5, PPID: 1, Comm: "node"}}, sock("0.0.0.0:2000", 5))}, testNow)
	rep.Reserved = buildReservedDiag(2000, excludedZhTW, excludedZhTW, "a : 1024\nb : 13977\n")
	out = render(rep)
	for _, want := range []string{"Windows 沒辦法替它轉送", "1962–2061", "net stop winnat", "start=49152"} {
		if !strings.Contains(out, want) {
			t.Errorf("blocked relay report missing %q:\n%s", want, out)
		}
	}

	// 沒有人監聽、但落在保留範圍：不可以說「沒有人使用」。
	rep = &Report{Port: 2000, Reserved: buildReservedDiag(2000, excludedZhTW, "", "")}
	if out := render(rep); !rep.Occupied() || strings.Contains(out, "目前沒有人使用") || !strings.Contains(out, "保留埠範圍") {
		t.Errorf("reserved report:\n%s", out)
	}

	// 帶 * 的自訂保留列不擋綁定：port 算是空的。
	rep = &Report{Port: 50010, Reserved: buildReservedDiag(50010, excludedZhTW, "", "")}
	if out := render(rep); rep.Occupied() || !strings.Contains(out, "目前沒有人使用") || !strings.Contains(out, "不會擋住") {
		t.Errorf("admin range report:\n%s", out)
	}

	// mirrored 模式下查不到任何東西：要提醒還有看不到的保留區段。
	rep = &Report{Port: 3000, Mode: "mirrored"}
	if out := render(rep); !strings.Contains(out, "mirrored") {
		t.Errorf("mirrored empty report:\n%s", out)
	}

	// 非監聽佔用：只說明，不當成可關閉的對象。
	rep = &Report{Port: 3000, Ephemeral: []*Owner{winOwner(555, "chrome.exe")}}
	if out := render(rep); !rep.Occupied() || !strings.Contains(out, "chrome.exe (PID 555)") || len(rep.Owners) != 0 {
		t.Errorf("ephemeral report:\n%s", out)
	}
}
