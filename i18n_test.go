package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// 其他測試檔的預期輸出都是中文，所以測試預設用中文介面；要測英文的地方用 withLanguage 切換。
func TestMain(m *testing.M) {
	setLanguage(langZhTW)
	os.Exit(m.Run())
}

func withLanguage(t *testing.T, l language) {
	t.Helper()
	old := T
	setLanguage(l)
	t.Cleanup(func() { T = old })
}

func hasCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || (r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF) {
			return true
		}
	}
	return false
}

var verbRe = regexp.MustCompile(`%(?:\[(\d+)\])?[-+# 0]*\d*(?:\.\d+)?([a-zA-Z%])`)

// formatArgs 回傳格式字串的每個參數位置需要哪一類的值：'d' 是整數，'s' 是其他。
func formatArgs(t *testing.T, name, format string) map[int]byte {
	t.Helper()
	args := map[int]byte{}
	next := 1
	for _, m := range verbRe.FindAllStringSubmatch(format, -1) {
		if m[2] == "%" {
			continue
		}
		idx := next
		if m[1] != "" {
			idx, _ = strconv.Atoi(m[1])
		}
		next = idx + 1
		kind := byte('s')
		if m[2] == "d" {
			kind = 'd'
		}
		if prev, ok := args[idx]; ok && prev != kind {
			t.Errorf("%s: argument %d is used both as a number and as text in %q", name, idx, format)
		}
		args[idx] = kind
	}
	return args
}

// 兩種語言的每一句都要有，而且吃的參數要一樣：少翻一句、或參數對不上，都會在執行時印出 %!s(MISSING) 之類的東西。
func TestCatalogParity(t *testing.T) {
	zh, en := reflect.ValueOf(zhTW), reflect.ValueOf(enUS)
	for i := 0; i < zh.NumField(); i++ {
		name := zh.Type().Field(i).Name
		switch zh.Field(i).Kind() {
		case reflect.String:
			a, b := zh.Field(i).String(), en.Field(i).String()
			if a == "" || b == "" {
				t.Errorf("%s: missing translation (zh-TW %q, en %q)", name, a, b)
				continue
			}
			if hasCJK(b) {
				t.Errorf("%s: the English text contains Chinese characters: %q", name, b)
			}
			if za, ea := formatArgs(t, name, a), formatArgs(t, name, b); !reflect.DeepEqual(za, ea) {
				t.Errorf("%s: format arguments differ\n  zh-TW %q\n  en    %q", name, a, b)
			}
		case reflect.Array:
			for j := 0; j < zh.Field(i).Len(); j++ {
				if zh.Field(i).Index(j).String() == "" || en.Field(i).Index(j).String() == "" {
					t.Errorf("%s[%d]: missing translation", name, j)
				}
			}
		default:
			t.Errorf("%s: unexpected field kind %s; teach this test about it", name, zh.Field(i).Kind())
		}
	}
	for _, c := range []catalog{zhTW, enUS} {
		for _, flag := range []string{"--no-kill", "--kill", "--force", "--lang", "--debug", "--help", "--version"} {
			if !strings.Contains(c.Usage, flag) {
				t.Errorf("usage text does not mention %s:\n%s", flag, c.Usage)
			}
		}
	}
}

func TestParseLanguage(t *testing.T) {
	cases := map[string]language{
		"en": langEN, "EN": langEN, "en-US": langEN, "en_GB": langEN,
		"zh": langZhTW, "zh-TW": langZhTW, "zh_tw": langZhTW, "zh-Hant": langZhTW, " zh-HK ": langZhTW,
	}
	for in, want := range cases {
		if got, ok := parseLanguage(in); !ok || got != want {
			t.Errorf("parseLanguage(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "fr", "ja-JP", "english", "zhongwen"} {
		if _, ok := parseLanguage(in); ok {
			t.Errorf("parseLanguage(%q) should be rejected", in)
		}
	}
}

func TestLanguageFromSystem(t *testing.T) {
	cases := []struct {
		tags []string
		want language
	}{
		{[]string{"zh-TW", "en-US"}, langZhTW},
		{[]string{"zh-Hant-TW"}, langZhTW},
		{[]string{"zh-HK"}, langZhTW},
		{[]string{"zh-MO"}, langZhTW},
		// 只有繁體才用中文介面；簡體系統沒有對應的翻譯，用英文。
		{[]string{"zh-CN"}, langEN},
		{[]string{"zh-Hans-CN", "zh-TW"}, langEN},
		// 只看第一順位：顯示語言是英文的人，即使清單裡有中文也給英文。
		{[]string{"en-US", "zh-TW"}, langEN},
		{[]string{"ja-JP"}, langEN},
		{nil, langEN},
	}
	for _, c := range cases {
		if got := languageFromSystem(c.tags); got != c.want {
			t.Errorf("languageFromSystem(%v) = %v, want %v", c.tags, got, c.want)
		}
	}
}

func TestPickLanguage(t *testing.T) {
	zhSystem, enSystem := []string{"zh-TW"}, []string{"en-US"}
	cases := []struct {
		name   string
		args   []string
		env    string
		system []string
		want   language
	}{
		{"flag beats everything", []string{"3000", "--lang", "en"}, "zh-TW", zhSystem, langEN},
		{"flag with equals sign", []string{"--lang=zh-TW", "3000"}, "", enSystem, langZhTW},
		{"the last flag wins", []string{"--lang", "en", "3000", "--lang", "zh-TW"}, "", enSystem, langZhTW},
		{"environment beats the system", nil, "en", zhSystem, langEN},
		{"environment with underscore", nil, "zh_TW", enSystem, langZhTW},
		{"system language by default", []string{"3000"}, "", zhSystem, langZhTW},
		{"English when nothing says otherwise", nil, "", nil, langEN},
		// 寫錯的值不能讓程式掛掉：先退回後面的來源，錯誤由 parseArgs 回報。
		{"bad flag value falls through", []string{"--lang", "fr"}, "", zhSystem, langZhTW},
		{"bad environment value falls through", nil, "klingon", zhSystem, langZhTW},
		{"flag without a value falls through", []string{"--lang"}, "", zhSystem, langZhTW},
	}
	for _, c := range cases {
		if got := pickLanguage(c.args, c.env, c.system); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// 英文介面下，各種報告都不可以漏出中文。
func TestEnglishOutput(t *testing.T) {
	withLanguage(t, langEN)
	node := []probeResult{probe("Ubuntu", "nat", []*wslProc{{
		PID: 4321, PPID: 4300, Comm: "node", Cmd: "next-server", Cwd: "/home/user/app", User: "user", StartTicks: "1",
		Ancestors: []procRef{{PID: 4300, Name: "sh", Cmd: "sh -c next dev"}, {PID: 4290, Name: "npm", Cmd: "npm run dev"}},
	}, {PID: 4322, PPID: 4321, Comm: "node"}}, sock("*:3000", 4321, 4322))}

	reports := map[string]struct {
		rep  *Report
		want []string
	}{
		"wsl owner behind the relay": {
			mergeReport(3000, []*Owner{winOwner(9876, "wslrelay.exe", "127.0.0.1:3000")}, node, testNow),
			[]string{"Port 3000 → node (PID 4321) in WSL distro Ubuntu", "Directory", "/home/user/app", "Parent", "← npm run dev (PID 4290)",
				"1 other process(es) share this port (PID 4322)", "wslrelay.exe (PID 9876) on the Windows side is only a forwarder"},
		},
		"windows owners, one protected": {
			mergeReport(445, []*Owner{winOwner(4, "System", "0.0.0.0:445"), winOwner(1111, "node.exe", "0.0.0.0:445")}, nil, testNow),
			[]string{"[1] Port 445 → System (PID 4) on Windows", "held by the Windows kernel", "[2] Port 445 → node.exe (PID 1111) on Windows"},
		},
		"relay with nothing behind it": {
			mergeReport(3000, []*Owner{winOwner(9876, "wslrelay.exe", "127.0.0.1:3000")},
				[]probeResult{{Distro: "Ubuntu", Err: errString("probe timed out")}}, testNow),
			[]string{"WSL's localhost forwarder", `Could not probe distro "Ubuntu": probe timed out`},
		},
		"orphan listener": {
			mergeReport(53, nil, []probeResult{probe("Ubuntu", "nat", nil, sock("10.255.255.254:53"))}, testNow),
			[]string{"something is listening inside the WSL VM (10.255.255.254:53)", "No running distro claims it"},
		},
		"reserved range": {
			&Report{Port: 2000, Reserved: buildReservedDiag(2000, excludedZhTW, excludedZhTW, "a : 1024\nb : 13977\n")},
			[]string{"Nothing is listening on Port 2000", "reserved port range 1962–2061 (IPv4/IPv6)", "net stop winnat", "starts at 1024 (the Windows default is 49152)"},
		},
		"single reserved port": {
			&Report{Port: 5357, Reserved: buildReservedDiag(5357, excludedZhTW, "", "")},
			[]string{"Windows has reserved it individually (IPv4)", "use another port"},
		},
		"administered range": {
			&Report{Port: 50010, Reserved: buildReservedDiag(50010, excludedZhTW, "", "")},
			[]string{"Port 50010 is not in use.", "administered reservation 50000–50059"},
		},
		"ephemeral": {
			&Report{Port: 3000, Ephemeral: []*Owner{winOwner(555, "chrome.exe")}},
			[]string{"local port of a connection owned by chrome.exe (PID 555)", "Try again in a moment"},
		},
		"free in mirrored mode": {
			&Report{Port: 3000, Mode: "mirrored", WSL1: []string{"legacy"}},
			[]string{"Port 3000 is not in use.", "mirrored networking mode", `Distro "legacy" is WSL1`},
		},
	}
	for name, c := range reports {
		out := render(c.rep)
		for _, want := range c.want {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q in:\n%s", name, want, out)
			}
		}
		if hasCJK(out) {
			t.Errorf("%s: Chinese text leaked into the English output:\n%s", name, out)
		}
	}

	var b bytes.Buffer
	printList(&b, buildListRows(
		[]listRow{{Port: 135, Where: "Windows", PID: 1920, Name: "svchost.exe", Addrs: []string{"0.0.0.0"}}},
		map[int]bool{3000: true}, append(node, probe("Debian", "nat", nil, sock("10.255.255.254:53"))), testNow), nil)
	out := b.String()
	for _, want := range []string{"PORT", "WHERE", "PROCESS", "COMMAND", "Ubuntu *", "(owner not visible)", "* forwarded to localhost on Windows by wslrelay."} {
		if !strings.Contains(out, want) {
			t.Errorf("list: missing %q in:\n%s", want, out)
		}
	}
	if hasCJK(out) {
		t.Errorf("list: Chinese text leaked into the English output:\n%s", out)
	}
	if got := humanSince(testNow.Add(-90*60*1e9), testNow); got != "1 hr ago" {
		t.Errorf("humanSince = %q", got)
	}
	if got := targetLabel(&Owner{Containers: []container{{Name: "web"}, {Name: "db"}}}); got != "container web, db" {
		t.Errorf("targetLabel = %q", got)
	}
}

// 英文與中文的明細欄位都要對齊：欄位名稱那一欄的寬度跟著最長的名稱走。
func TestLabelWidth(t *testing.T) {
	if got := labelWidth(); got != 8 {
		t.Errorf("zh-TW label width = %d, want 8", got)
	}
	withLanguage(t, langEN)
	if got := labelWidth(); got != len("Directory")+2 {
		t.Errorf("English label width = %d, want %d", got, len("Directory")+2)
	}
}

// 給使用者看的文字只能放在 i18n.go：其他原始檔的字串裡出現中文，代表有一句話沒有英文版。
func TestNoHardCodedChinese(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	if len(files) == 0 {
		t.Skip("source files are not available next to the test binary")
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "i18n.go" {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && (lit.Kind == token.STRING || lit.Kind == token.CHAR) && hasCJK(lit.Value) {
				t.Errorf("%s: hard-coded Chinese text %s; move it into the catalog in i18n.go", fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
	}
}
