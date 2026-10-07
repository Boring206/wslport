package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var testNow = time.Unix(1_800_000_000, 0)

func sock(local string, pids ...int) wslSock {
	addr, port, _ := splitLocal(local)
	return wslSock{Local: local, Addr: addr, Port: port, PIDs: pids}
}

func probe(distro, mode string, procs []*wslProc, socks ...wslSock) probeResult {
	pr := probeResult{Distro: distro, Mode: mode, Socks: socks, Procs: map[int]*wslProc{}, ClkTck: 100}
	for _, p := range procs {
		pr.Procs[p.PID] = p
	}
	return pr
}

func winOwner(pid int, name string, addrs ...string) *Owner {
	return &Owner{
		Where: whereWindows, PID: pid, Name: name, Addrs: addrs,
		Relay: isRelayImage(name), Protected: winProtectReason(uint32(pid), name),
	}
}

func labels(owners []*Owner) []string {
	var s []string
	for _, o := range owners {
		s = append(s, o.Where+":"+o.Distro+":"+o.Label())
	}
	return s
}

// nginx 的 master 與 worker 共用同一個 socket：只有 master 是擁有者。
func TestWSLOwnersPicksTopAncestor(t *testing.T) {
	pr := probe("Ubuntu", "nat", []*wslProc{
		{PID: 410, PPID: 1, Comm: "nginx", Cgroup: "0::/system.slice/nginx.service;"},
		{PID: 411, PPID: 410, Comm: "nginx"},
		{PID: 412, PPID: 410, Comm: "nginx"},
	}, sock("0.0.0.0:80", 410, 411, 412), sock("[::]:80", 410, 411, 412), sock("0.0.0.0:3000", 999))

	owners := wslOwners(&pr, 80, testNow)
	if len(owners) != 1 {
		t.Fatalf("owners = %v", labels(owners))
	}
	o := owners[0]
	if o.PID != 410 || !reflect.DeepEqual(o.Workers, []int{411, 412}) {
		t.Errorf("root = %d, workers = %v", o.PID, o.Workers)
	}
	if !reflect.DeepEqual(o.Addrs, []string{"0.0.0.0:80", "[::]:80"}) {
		t.Errorf("addrs = %v", o.Addrs)
	}
	if o.Unit != "nginx.service" || o.NotForwarded {
		t.Errorf("unit = %q, notForwarded = %v", o.Unit, o.NotForwarded)
	}
}

// 兩個互不相干的行程用 SO_REUSEPORT 聽同一個 port：兩個都是擁有者。
func TestWSLOwnersUnrelatedProcesses(t *testing.T) {
	pr := probe("Ubuntu", "nat", []*wslProc{
		{PID: 500, PPID: 1, Comm: "a"},
		{PID: 600, PPID: 1, Comm: "b"},
	}, sock("0.0.0.0:9000", 500, 600))
	if got := labels(wslOwners(&pr, 9000, testNow)); len(got) != 2 {
		t.Errorf("owners = %v", got)
	}
}

func TestNotForwardedOnlyInNAT(t *testing.T) {
	procs := []*wslProc{{PID: 79, PPID: 1, Comm: "systemd-resolve"}}
	nat := probe("Ubuntu", "nat", procs, sock("127.0.0.53%lo:53", 79))
	if o := wslOwners(&nat, 53, testNow)[0]; !o.NotForwarded {
		t.Error("127.0.0.53 is not relayed in NAT mode")
	}
	mirrored := probe("Ubuntu", "mirrored", procs, sock("127.0.0.53%lo:53", 79))
	if o := wslOwners(&mirrored, 53, testNow)[0]; o.NotForwarded {
		t.Error("NotForwarded only applies to NAT mode")
	}
}

// 所有 WSL2 distro 共用網路命名空間：每個 distro 都看得到同一個監聽者，
// 但只有擁有它的 distro 那一列帶 PID。
func TestMultiDistroDedupe(t *testing.T) {
	probes := []probeResult{
		probe("Ubuntu", "nat", nil, sock("0.0.0.0:3000"), sock("10.255.255.254:53")),
		probe("Debian", "nat", []*wslProc{{PID: 77, PPID: 1, Comm: "node"}}, sock("0.0.0.0:3000", 77), sock("10.255.255.254:53")),
	}
	rep := mergeReport(3000, nil, probes, testNow)
	if got := labels(rep.Owners); !reflect.DeepEqual(got, []string{"wsl:Debian:node (PID 77)"}) {
		t.Errorf("owners = %v", got)
	}
	if len(rep.Orphans) != 0 {
		t.Errorf("orphans = %v", rep.Orphans)
	}
	// 沒有任何 distro 認領的那一個才是「看不到擁有者」，而且只回報一次。
	rep = mergeReport(53, nil, probes, testNow)
	if len(rep.Owners) != 0 || !reflect.DeepEqual(rep.Orphans, []string{"10.255.255.254:53"}) {
		t.Errorf("owners = %v, orphans = %v", labels(rep.Owners), rep.Orphans)
	}
}

// 實機上遇過的情況：一個 distro 有 ss、另一個只有 netstat（Alpine）。
// 同一個雙堆疊監聽者，ss 寫成 *:3000，netstat 寫成 :::3000，不可以被當成兩個。
func TestMultiDistroDifferentTools(t *testing.T) {
	ssLine, _ := parseSockLine(`LISTEN 0 1 *:3000 *:*`)
	netstatLine, _ := parseSockLine(`tcp        0      0 :::3000                 :::*                    LISTEN      31/nc`)
	if ssLine.Key() != netstatLine.Key() {
		t.Fatalf("keys differ: %q vs %q", ssLine.Key(), netstatLine.Key())
	}
	probes := []probeResult{
		probe("Ubuntu", "nat", nil, ssLine),
		probe("alpine", "nat", []*wslProc{{PID: 31, PPID: 1, Comm: "nc"}}, netstatLine),
	}
	rep := mergeReport(3000, []*Owner{winOwner(9876, "wslrelay.exe", "127.0.0.1:3000")}, probes, testNow)
	if got := labels(rep.Owners); !reflect.DeepEqual(got, []string{"wsl:alpine:nc (PID 31)"}) {
		t.Errorf("owners = %v", got)
	}
	if len(rep.Orphans) != 0 {
		t.Errorf("the socket was reported a second time as ownerless: %v", rep.Orphans)
	}
	if got := rep.Owners[0].Addrs; !reflect.DeepEqual(got, []string{"[::]:3000"}) || rep.Owners[0].NotForwarded {
		t.Errorf("addrs = %v, notForwarded = %v", got, rep.Owners[0].NotForwarded)
	}

	// 位址寫法不同但其實是同一個：ss 帶 %介面，netstat 不帶；IPv6 一個有方括號、一個沒有。
	same := [][2]string{
		{`LISTEN 0 4096 127.0.0.53%lo:53 0.0.0.0:* users:(("x",pid=1,fd=1))`, `tcp 0 0 127.0.0.53:53 0.0.0.0:* LISTEN 1/x`},
		{`LISTEN 0 5 [::1]:5173 [::]:*`, `tcp6 0 0 ::1:5173 :::* LISTEN -`},
		{`LISTEN 0 5 [::]:8080 [::]:*`, `tcp6 0 0 :::8080 :::* LISTEN -`},
	}
	for _, pair := range same {
		a, _ := parseSockLine(pair[0])
		b, _ := parseSockLine(pair[1])
		if a.Key() != b.Key() || a.Display() != b.Display() {
			t.Errorf("%q and %q should match: keys %q / %q, display %q / %q", pair[0], pair[1], a.Key(), b.Key(), a.Display(), b.Display())
		}
	}
	// IPv4 與 IPv6 的萬用位址是兩個不同的 socket，不可以合併。
	v4, _ := parseSockLine(`LISTEN 0 5 0.0.0.0:8080 0.0.0.0:*`)
	v6, _ := parseSockLine(`LISTEN 0 5 [::]:8080 [::]:*`)
	if v4.Key() == v6.Key() {
		t.Error("0.0.0.0 and :: must stay separate")
	}
}

func TestRelayFoldedWhenDistroListens(t *testing.T) {
	probes := []probeResult{probe("Ubuntu", "nat", []*wslProc{{PID: 4321, PPID: 1, Comm: "node"}}, sock("*:3000", 4321))}
	win := []*Owner{winOwner(9876, "wslrelay.exe", "127.0.0.1:3000")}
	rep := mergeReport(3000, win, probes, testNow)
	if got := labels(rep.Owners); !reflect.DeepEqual(got, []string{"wsl:Ubuntu:node (PID 4321)"}) {
		t.Errorf("owners = %v", got)
	}
	if len(rep.Relays) != 1 || rep.Relays[0].PID != 9876 {
		t.Errorf("relays = %v", labels(rep.Relays))
	}
	if !rep.WinListening || rep.Mode != "nat" {
		t.Errorf("WinListening = %v, Mode = %q", rep.WinListening, rep.Mode)
	}
}

// relay 還在但 WSL 裡找不到人（行程剛結束、或探測失敗）：照實顯示，而且不能關。
func TestRelayShownWhenNoDistroListens(t *testing.T) {
	failed := probeResult{Distro: "Ubuntu", Err: errString("探測逾時")}
	rep := mergeReport(3000, []*Owner{winOwner(9876, "wslrelay.exe", "127.0.0.1:3000")}, []probeResult{failed}, testNow)
	if len(rep.Owners) != 1 || len(rep.Relays) != 0 {
		t.Fatalf("owners = %v, relays = %v", labels(rep.Owners), labels(rep.Relays))
	}
	if rep.Owners[0].Killable() {
		t.Error("wslrelay must never be killable")
	}
	if len(rep.Notes) != 1 || !strings.Contains(rep.Notes[0], "Ubuntu") {
		t.Errorf("notes = %v", rep.Notes)
	}
}

// relay 設了 SO_REUSEADDR，可以和綁在 0.0.0.0 的 Windows 程式並存：兩個真正的佔用者都要列出。
func TestWindowsAppCoexistsWithRelay(t *testing.T) {
	probes := []probeResult{probe("Ubuntu", "nat", []*wslProc{{PID: 4321, PPID: 1, Comm: "node"}}, sock("0.0.0.0:3000", 4321))}
	win := []*Owner{
		winOwner(1111, "node.exe", "0.0.0.0:3000"),
		winOwner(9876, "wslrelay.exe", "127.0.0.1:3000"),
	}
	rep := mergeReport(3000, win, probes, testNow)
	want := []string{"windows::node.exe (PID 1111)", "wsl:Ubuntu:node (PID 4321)"}
	if got := labels(rep.Owners); !reflect.DeepEqual(got, want) {
		t.Errorf("owners = %v", got)
	}
	if len(rep.Relays) != 1 {
		t.Errorf("relays = %v", labels(rep.Relays))
	}
	for _, o := range rep.Owners {
		if !o.Killable() {
			t.Errorf("%s should be killable", o.Label())
		}
	}
}

func TestProtectedProcesses(t *testing.T) {
	protected := []struct {
		pid  uint32
		name string
	}{
		{0, "[System Process]"}, {4, "System"}, {700, "csrss.exe"}, {701, "LSASS.EXE"}, {702, "services.exe"},
		{703, "svchost.exe"}, {704, "wslrelay.exe"}, {705, "wslhost.exe"}, {706, "wslservice.exe"},
		{707, "vmmem"}, {708, "com.docker.backend.exe"},
	}
	for _, p := range protected {
		if winProtectReason(p.pid, p.name) == "" {
			t.Errorf("%s (PID %d) must be protected", p.name, p.pid)
		}
	}
	for _, name := range []string{"node.exe", "python.exe", "nginx.exe", "docker-proxy"} {
		if winProtectReason(1234, name) != "" {
			t.Errorf("%s must be killable", name)
		}
	}
	// Docker Desktop 的後端不能直接關，但找到容器後可以改用 docker stop。
	o := winOwner(708, "com.docker.backend.exe", "0.0.0.0:8080")
	if o.Killable() {
		t.Error("docker backend without a container must not be killable")
	}
	o.Containers = []container{{ID: "abc", Name: "web", Image: "nginx"}}
	if !o.Killable() || targetLabel(o) != "容器 web" {
		t.Errorf("killable = %v, label = %q", o.Killable(), targetLabel(o))
	}
}

// 實機上遇過的情況：Docker 替同一個 port 開了 IPv4 與 IPv6 兩個 docker-proxy。
func TestMergeContainerOwners(t *testing.T) {
	web := []container{{ID: "fe64", Name: "web", Image: "busybox"}}
	v4 := &Owner{Where: whereWSL, Distro: "Ubuntu", PID: 38138, Name: "docker-proxy", Addrs: []string{"0.0.0.0:3000"}, Containers: web}
	v6 := &Owner{Where: whereWSL, Distro: "Ubuntu", PID: 38144, Name: "docker-proxy", Addrs: []string{"[::]:3000"}, Containers: web}
	node := &Owner{Where: whereWindows, PID: 1111, Name: "node.exe", Addrs: []string{"127.0.0.1:3000"}}
	// 查不到容器的 docker-proxy 維持各自一筆，不可以被併掉。
	lost := &Owner{Where: whereWSL, Distro: "Debian", PID: 50, Name: "docker-proxy"}
	other := &Owner{Where: whereWSL, Distro: "Debian", PID: 60, Name: "docker-proxy", Containers: []container{{ID: "aaaa", Name: "api"}}}

	got := mergeContainerOwners([]*Owner{node, v4, v6, lost, other})
	if want := []string{"windows::node.exe (PID 1111)", "wsl:Ubuntu:docker-proxy (PID 38138)", "wsl:Debian:docker-proxy (PID 50)", "wsl:Debian:docker-proxy (PID 60)"}; !reflect.DeepEqual(labels(got), want) {
		t.Fatalf("owners = %v", labels(got))
	}
	if !reflect.DeepEqual(v4.Addrs, []string{"0.0.0.0:3000", "[::]:3000"}) || !reflect.DeepEqual(v4.Workers, []int{38144}) {
		t.Errorf("merged owner: addrs %v, workers %v", v4.Addrs, v4.Workers)
	}
	// 併成一筆之後，容器是唯一可關閉的對象，-k 才能直接用。
	rep := &Report{Port: 3000, Owners: mergeContainerOwners([]*Owner{v4, v6})}
	withInput(t, "")
	if chosen := chooseTargets(rep, options{kill: true}); len(chosen) != 1 || targetLabel(chosen[0]) != "容器 web" {
		t.Errorf("chosen = %v", labels(chosen))
	}
}

func TestBuildListRowsFoldsRelay(t *testing.T) {
	probes := []probeResult{probe("Ubuntu", "nat", []*wslProc{
		{PID: 4321, PPID: 1, Comm: "node", Cmd: "node server.js"},
		{PID: 79, PPID: 1, Comm: "systemd-resolve"},
	}, sock("*:3000", 4321), sock("127.0.0.53%lo:53", 79), sock("10.255.255.254:53"))}
	win := []listRow{
		{Port: 3000, Where: "Windows", PID: 9876, Name: "wslrelay.exe", Addrs: []string{"127.0.0.1"}},
		{Port: 5432, Where: "Windows", PID: 9876, Name: "wslrelay.exe", Addrs: []string{"127.0.0.1"}},
		{Port: 135, Where: "Windows", PID: 1920, Name: "svchost.exe", Addrs: []string{"0.0.0.0", "::"}},
	}
	rows := buildListRows(win, map[int]bool{3000: true, 5432: true}, probes, testNow)

	var got []string
	for _, r := range rows {
		got = append(got, strings.Join([]string{itoa(r.Port), r.Where, r.Name, strings.Join(r.Addrs, ",")}, "|"))
	}
	want := []string{
		"53|Ubuntu|systemd-resolve|127.0.0.53",
		"53|WSL|-|10.255.255.254",
		"135|Windows|svchost.exe|0.0.0.0,::",
		"3000|Ubuntu|node|*",
		// 5432 的 relay 在 WSL 裡找不到對應的監聽者，所以照實留下。
		"5432|Windows|wslrelay.exe|127.0.0.1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows =\n%s", strings.Join(got, "\n"))
	}
	// 只有 WSL 那一列、且 Windows 端有 relay 的 port 才標示為已轉送。
	for _, r := range rows {
		if r.Relayed != (r.Port == 3000) {
			t.Errorf("port %d (%s): Relayed = %v", r.Port, r.Where, r.Relayed)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func itoa(n int) string { return joinInts([]int{n}) }
