package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	whereWindows = "windows"
	whereWSL     = "wsl"
	// whereWSLVM 是總表裡「在 WSL 虛擬機內、但沒有 distro 認領」那一列的位置。
	whereWSLVM = "WSL"
)

// Owner 是佔用某個 port 的一個行程（Windows 或某個 distro 內）。
type Owner struct {
	Where      string
	Distro     string // 只有 WSL
	PID        int
	Name       string
	Exe        string
	Cmdline    string
	Cwd        string // Windows 端只讀得到同一個使用者的行程
	User       string // 只有 WSL
	PPID       int
	ParentName string
	Ancestors  []procRef // 由近到遠
	Started    time.Time
	Addrs      []string
	Workers    []int    // 共用同一個 socket 的子行程
	Heirs      []string // 擁有者已結束時，繼承了 socket 的可能行程
	Unit       string   // systemd 服務單位

	InContainer  bool
	Service      bool   // 父行程是 services.exe
	Limited      bool   // 權限不足，開不了行程
	Dead         bool   // TCP 表上的 PID 已不存在
	Relay        bool   // wslrelay／wslhost
	NotForwarded bool   // NAT 模式下綁定的位址不會轉送到 Windows
	Protected    string // 不提供關閉的原因；空字串表示可以關
	Containers   []container

	winCreated uint64
	startTicks string
}

// Label 是「名稱 (PID n)」。
func (o *Owner) Label() string {
	return fmt.Sprintf("%s (PID %d)", o.Name, o.PID)
}

// Killable 回報能不能由本工具關閉：一般行程，或是找得到容器的 Docker port。
func (o *Owner) Killable() bool {
	return len(o.Containers) > 0 || o.Protected == ""
}

// Report 是查詢單一 port 的完整結果。
type Report struct {
	Port      int
	Owners    []*Owner
	Relays    []*Owner // 已確認只是替 WSL 轉送的 Windows 行程
	Orphans   []string // 在 WSL 虛擬機內、但沒有任何 distro 認領的監聽位址
	Ephemeral []*Owner // 非監聽：把這個 port 當本機埠的連線
	Reserved  *reservedDiag
	Mode      string // WSL 網路模式：nat、mirrored…
	Notes     []string
	WSL1      []string

	WinListening bool
}

// Occupied 回報這個 port 是否有任何佔用或保留的跡象。
func (r *Report) Occupied() bool {
	return len(r.Owners) > 0 || len(r.Orphans) > 0 || len(r.Ephemeral) > 0 || r.Reserved.Blocking()
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// wslOwners 找出某個 distro 內擁有這個 port 的行程。
// 多個 PID 共用同一個 socket 時（nginx、node cluster），只把最上層的祖先當作擁有者，其餘列為子行程。
func wslOwners(pr *probeResult, port int, now time.Time) []*Owner {
	addrs := map[int][]string{}
	var pids []int
	for _, s := range pr.Socks {
		if s.Port != port {
			continue
		}
		for _, pid := range s.PIDs {
			if _, ok := addrs[pid]; !ok {
				pids = append(pids, pid)
			}
			addrs[pid] = appendUnique(addrs[pid], s.Display())
		}
	}
	sort.Ints(pids)

	rootOf := func(pid int) int {
		for depth := 0; depth < 64; depth++ {
			p := pr.Procs[pid]
			if p == nil {
				break
			}
			if _, ok := addrs[p.PPID]; !ok {
				break
			}
			pid = p.PPID
		}
		return pid
	}

	byRoot := map[int]*Owner{}
	var owners []*Owner
	for _, pid := range pids {
		root := rootOf(pid)
		o := byRoot[root]
		if o == nil {
			o = newWSLOwner(pr, root, now)
			byRoot[root] = o
			owners = append(owners, o)
		}
		for _, a := range addrs[pid] {
			o.Addrs = appendUnique(o.Addrs, a)
		}
		if pid != root {
			o.Workers = append(o.Workers, pid)
		}
	}
	for _, o := range owners {
		if pr.Mode != "nat" {
			continue
		}
		o.NotForwarded = true
		for _, a := range o.Addrs {
			if addr, _, ok := splitLocal(a); ok && relayEligible(addr) {
				o.NotForwarded = false
			}
		}
	}
	return owners
}

func newWSLOwner(pr *probeResult, pid int, now time.Time) *Owner {
	o := &Owner{Where: whereWSL, Distro: pr.Distro, PID: pid, Name: "?"}
	p := pr.Procs[pid]
	if p == nil {
		return o
	}
	o.Name = displayName(p.Comm, p.Exe, p.Cmd)
	o.Exe, o.Cmdline, o.Cwd, o.User = p.Exe, p.Cmd, p.Cwd, p.User
	o.PPID = p.PPID
	o.Ancestors = userAncestors(p.Ancestors)
	if len(p.Ancestors) > 0 {
		o.ParentName = p.Ancestors[0].Name
	}
	o.Started = pr.started(p, now)
	o.Unit = unitFromCgroup(p.Cgroup)
	o.InContainer = inContainerCgroup(p.Cgroup)
	o.startTicks = p.StartTicks
	return o
}

// orphanSocks 回傳沒有任何 distro 認領的監聽位址。
// 所有 WSL2 distro 共用網路命名空間，所以每個 distro 都看得到全部監聽者，
// 但只有擁有該 socket 的 distro 那一列才帶 PID。port 為 0 表示不篩選。
func orphanSocks(probes []probeResult, port int) []string {
	claimed := map[string]bool{}
	seen := map[string]bool{}
	var order []wslSock
	for _, pr := range probes {
		for _, s := range pr.Socks {
			if port != 0 && s.Port != port {
				continue
			}
			if len(s.PIDs) > 0 {
				claimed[s.Key()] = true
			} else if !seen[s.Key()] {
				seen[s.Key()] = true
				order = append(order, s)
			}
		}
	}
	var orphans []string
	for _, s := range order {
		if !claimed[s.Key()] {
			orphans = append(orphans, s.Display())
		}
	}
	return orphans
}

// mergeReport 把 Windows 端與各 distro 的結果合併成一份報告。
// Windows 端的 relay 只有在 WSL 裡確實有人監聽同一個 port 時才降為附註。
func mergeReport(port int, win []*Owner, probes []probeResult, now time.Time) *Report {
	rep := &Report{Port: port, WinListening: len(win) > 0}
	var wsl []*Owner
	for i := range probes {
		pr := &probes[i]
		if rep.Mode == "" {
			rep.Mode = pr.Mode
		}
		switch {
		case pr.Err != nil:
			rep.Notes = append(rep.Notes, fmt.Sprintf(T.ProbeFailed, pr.Distro, pr.Err))
		case pr.NoTool:
			rep.Notes = append(rep.Notes, fmt.Sprintf(T.NoTool, pr.Distro))
		default:
			wsl = append(wsl, wslOwners(pr, port, now)...)
		}
	}
	rep.Orphans = orphanSocks(probes, port)

	inWSL := len(wsl) > 0 || len(rep.Orphans) > 0
	for _, o := range win {
		if o.Relay && inWSL {
			rep.Relays = append(rep.Relays, o)
			continue
		}
		rep.Owners = append(rep.Owners, o)
	}
	rep.Owners = append(rep.Owners, wsl...)
	return rep
}

// mergeContainerOwners 把指向同一組容器的 Docker 行程併成一個佔用者。
// Docker 會替同一個發佈的 port 各開一個 IPv4 與一個 IPv6 的 docker-proxy；
// 對使用者來說那是同一個容器，分成兩筆會變成要用編號選，-k 也用不了。
func mergeContainerOwners(owners []*Owner) []*Owner {
	first := map[string]*Owner{}
	var out []*Owner
	for _, o := range owners {
		if len(o.Containers) == 0 {
			out = append(out, o)
			continue
		}
		key := o.Where + "|" + o.Distro
		for _, c := range o.Containers {
			key += "|" + c.ID
		}
		if kept := first[key]; kept != nil {
			for _, a := range o.Addrs {
				kept.Addrs = appendUnique(kept.Addrs, a)
			}
			kept.Workers = append(kept.Workers, o.PID)
			continue
		}
		first[key] = o
		out = append(out, o)
	}
	return out
}

func hasWSLOwner(rep *Report) bool {
	for _, o := range rep.Owners {
		if o.Where == whereWSL {
			return true
		}
	}
	return len(rep.Orphans) > 0
}

// newWinOwner 建立 Windows 端的佔用者。full 為 true 時多讀工作目錄與祖先鏈（總表用不到）。
func newWinOwner(pid uint32, procs map[uint32]winProcInfo, full bool) *Owner {
	o := &Owner{Where: whereWindows, PID: int(pid)}
	info, alive := procs[pid]
	if !alive {
		o.Name, o.Dead, o.Protected = T.DeadOwner, true, T.ProtectDead
		for _, p := range procs {
			if p.PPID == pid {
				o.Heirs = append(o.Heirs, fmt.Sprintf("%s (PID %d)", p.Name, p.PID))
			}
		}
		sort.Strings(o.Heirs)
		return o
	}
	o.Name, o.PPID = info.Name, int(info.PPID)
	if parent, ok := procs[info.PPID]; ok && info.PPID != 0 {
		o.ParentName = parent.Name
	}
	o.Service = baseName(o.ParentName) == "services"
	o.Relay = isRelayImage(o.Name)
	o.Protected = winProtectReason(pid, o.Name)
	if pid == 0 || pid == 4 {
		return o
	}
	d := queryWinProcess(pid)
	o.Exe, o.Cmdline, o.winCreated = d.Exe, d.Cmdline, d.Created
	o.Started = ticksToTime(d.Created)
	o.Limited = !d.OK
	if full {
		o.Cwd = processCwd(pid)
		o.Ancestors = winAncestors(info, d.Created, procs)
	}
	return o
}

// buildWinOwners 把 TCP 表中屬於這個 port 的列依 PID 合併。
// 雙堆疊監聽者會同時出現在 v4 與 v6 兩張表。
func buildWinOwners(rows []tcpRow, port int, listening bool) []*Owner {
	procs, _ := snapshotProcesses()
	byPID := map[uint32]*Owner{}
	var owners []*Owner
	for _, r := range rows {
		if r.Port != port || (r.State == mibTCPStateListen) != listening {
			continue
		}
		o := byPID[r.PID]
		if o == nil {
			o = newWinOwner(r.PID, procs, true)
			byPID[r.PID] = o
			owners = append(owners, o)
		}
		o.Addrs = appendUnique(o.Addrs, formatAddr(r.Addr, r.Port))
	}
	return owners
}

// lookupPort 並行查詢 Windows 的 TCP 表與所有執行中的 distro。
// diag 為 false 時略過保留範圍與非監聽連線的診斷（關閉後複查用）。
func lookupPort(port int, diag bool) (*Report, error) {
	var (
		probes []probeResult
		wsl1   []string
		wg     sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		probes, wsl1 = probeDistros(port)
	}()
	began := time.Now()
	rows, err := windowsTCPRows(tcpTableOwnerPIDListener)
	var win []*Owner
	if err == nil {
		win = buildWinOwners(rows, port, true)
	}
	debugf("Windows TCP table: %d listener row(s), %d process(es) on port %d, %v", len(rows), len(win), port, time.Since(began).Round(time.Millisecond))
	wg.Wait()
	if err != nil {
		return nil, err
	}

	rep := mergeReport(port, win, probes, time.Now())
	rep.WSL1 = wsl1
	// 同一個位置（Windows 或某個 distro）只需要問 docker 一次。
	containers := map[string][]container{}
	for _, o := range rep.Owners {
		if !isDockerOwner(o) {
			continue
		}
		found, asked := containers[o.Distro]
		if !asked {
			found = findContainers(o.Distro, port)
			containers[o.Distro] = found
		}
		o.Containers = found
	}
	rep.Owners = mergeContainerOwners(rep.Owners)

	if !diag {
		return rep, nil
	}
	// Windows 端沒人監聽時才查保留範圍：可能是 port 沒人用卻綁不上，
	// 也可能是 WSL 裡有人監聽、Windows 卻無法替它轉送。
	// mirrored 模式下 Windows 端本來就不會有對應的列。
	if !rep.WinListening && !(rep.Mode == "mirrored" && hasWSLOwner(rep)) {
		began := time.Now()
		rep.Reserved = diagnoseReserved(port)
		debugf("reserved ranges (netsh): %+v, %v", *rep.Reserved, time.Since(began).Round(time.Millisecond))
	}
	if len(rep.Owners) == 0 && len(rep.Orphans) == 0 {
		if all, err := windowsTCPRows(tcpTableOwnerPIDAll); err == nil {
			rep.Ephemeral = buildWinOwners(all, port, false)
		}
	}
	return rep, nil
}

// listRow 是總表的一列。
type listRow struct {
	Port    int
	Where   string // "Windows" 或 distro 名稱
	PID     int
	Name    string
	Addrs   []string
	Detail  string
	Relayed bool // WSL 的監聽者，且 Windows 端有 relay 替它轉送
}

// buildListRows 合併兩邊的監聽者：relay 那一列若在 WSL 找得到同 port 的監聽者就收起來。
func buildListRows(win []listRow, relayPorts map[int]bool, probes []probeResult, now time.Time) []listRow {
	var rows []listRow
	wslPorts := map[int]bool{}
	for i := range probes {
		pr := &probes[i]
		if pr.Err != nil || pr.NoTool {
			continue
		}
		ports := map[int]bool{}
		for _, s := range pr.Socks {
			if len(s.PIDs) > 0 {
				ports[s.Port] = true
			}
		}
		for port := range ports {
			for _, o := range wslOwners(pr, port, now) {
				detail := o.Cmdline
				if detail == "" {
					detail = o.Exe
				}
				var addrs []string
				for _, local := range o.Addrs {
					if addr, _, ok := splitLocal(local); ok {
						addrs = appendUnique(addrs, addr)
					}
				}
				rows = append(rows, listRow{Port: port, Where: pr.Distro, PID: o.PID, Name: o.Name, Addrs: addrs, Detail: detail})
				wslPorts[port] = true
			}
		}
	}
	for _, local := range orphanSocks(probes, 0) {
		if addr, port, ok := splitLocal(local); ok {
			rows = append(rows, listRow{Port: port, Where: whereWSLVM, Name: "-", Addrs: []string{addr}, Detail: T.OrphanShort})
			wslPorts[port] = true
		}
	}
	for i := range rows {
		rows[i].Relayed = relayPorts[rows[i].Port]
	}
	for _, r := range win {
		if isRelayImage(r.Name) && wslPorts[r.Port] {
			continue
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		if (a.Where == "Windows") != (b.Where == "Windows") {
			return a.Where == "Windows"
		}
		if a.Where != b.Where {
			return a.Where < b.Where
		}
		return a.PID < b.PID
	})
	return rows
}

// listAll 列出 Windows 與所有執行中 distro 的監聽者。
func listAll() (rows []listRow, notes []string, err error) {
	var (
		probes []probeResult
		wsl1   []string
		wg     sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		probes, wsl1 = probeDistros(0)
	}()
	tcp, err := windowsTCPRows(tcpTableOwnerPIDListener)
	wg.Wait()
	if err != nil {
		return nil, nil, err
	}

	procs, _ := snapshotProcesses()
	type key struct {
		port int
		pid  uint32
	}
	index := map[key]int{}
	owners := map[uint32]*Owner{}
	relayPorts := map[int]bool{}
	var win []listRow
	for _, r := range tcp {
		k := key{r.Port, r.PID}
		i, ok := index[k]
		if !ok {
			o := owners[r.PID]
			if o == nil {
				o = newWinOwner(r.PID, procs, false)
				owners[r.PID] = o
			}
			detail := o.Cmdline
			if detail == "" {
				detail = o.Exe
			}
			i = len(win)
			index[k] = i
			win = append(win, listRow{Port: r.Port, Where: "Windows", PID: int(r.PID), Name: o.Name, Detail: detail})
			if o.Relay {
				relayPorts[r.Port] = true
			}
		}
		win[i].Addrs = appendUnique(win[i].Addrs, r.Addr.String())
	}

	for _, pr := range probes {
		switch {
		case pr.Err != nil:
			notes = append(notes, fmt.Sprintf(T.ProbeFailed, pr.Distro, pr.Err))
		case pr.NoTool:
			notes = append(notes, fmt.Sprintf(T.NoTool, pr.Distro))
		}
	}
	for _, name := range wsl1 {
		notes = append(notes, fmt.Sprintf(T.WSL1Skipped, name))
	}
	return buildListRows(win, relayPorts, probes, time.Now()), notes, nil
}
