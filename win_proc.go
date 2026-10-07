package main

import (
	"errors"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type winProcInfo struct {
	PID  uint32
	PPID uint32
	Name string
}

// snapshotProcesses 用 Toolhelp 快照取得所有行程的名稱與父 PID。
// 不需要開啟行程，所以連服務與已提權的行程都拿得到。
func snapshotProcesses() (map[uint32]winProcInfo, error) {
	h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	procs := make(map[uint32]winProcInfo)
	for err = windows.Process32First(h, &pe); err == nil; err = windows.Process32Next(h, &pe) {
		procs[pe.ProcessID] = winProcInfo{
			PID:  pe.ProcessID,
			PPID: pe.ParentProcessID,
			Name: windows.UTF16ToString(pe.ExeFile[:]),
		}
	}
	return procs, nil
}

type winDetails struct {
	Exe     string
	Cmdline string
	Created uint64 // 建立時間（FILETIME，100ns）；0 表示未知
	OK      bool   // 是否開得了行程
}

func filetimeTicks(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}

func ticksToTime(t uint64) time.Time {
	if t == 0 {
		return time.Time{}
	}
	ft := windows.Filetime{LowDateTime: uint32(t), HighDateTime: uint32(t >> 32)}
	return time.Unix(0, ft.Nanoseconds())
}

func processCreated(h windows.Handle) uint64 {
	var created, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exit, &kernel, &user); err != nil {
		return 0
	}
	return filetimeTicks(created)
}

// queryWinProcess 讀取路徑、指令列與建立時間。開不了行程時 OK 為 false。
func queryWinProcess(pid uint32) winDetails {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return winDetails{}
	}
	defer windows.CloseHandle(h)

	d := winDetails{OK: true, Created: processCreated(h)}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err == nil {
		d.Exe = windows.UTF16ToString(buf[:n])
	}
	d.Cmdline = processCommandLine(h)
	return d
}

// processCommandLine 用 ProcessCommandLineInformation 取指令列，
// 只需要 PROCESS_QUERY_LIMITED_INFORMATION，不必讀對方的記憶體。
func processCommandLine(h windows.Handle) string {
	size := uint32(4096)
	for attempt := 0; attempt < 4; attempt++ {
		// 用 uint64 配置，確保 NTUnicodeString 的對齊。
		buf := make([]uint64, (size+7)/8)
		bufLen := uint32(len(buf) * 8)
		var ret uint32
		err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation,
			unsafe.Pointer(&buf[0]), bufLen, &ret)
		if err == nil {
			s := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0])).String()
			runtime.KeepAlive(buf)
			return s
		}
		if ret <= bufLen {
			return ""
		}
		size = ret
	}
	return ""
}

// processBasicInfo 對應 PROCESS_BASIC_INFORMATION。x/sys 的同名結構把 PEB 位址宣告成指標，
// 但那是「對方行程」裡的位址，放進指標型別的欄位會讓 Go 的垃圾回收器誤判，所以這裡全用 uintptr。
type processBasicInfo struct {
	ExitStatus      uintptr
	PebBaseAddress  uintptr
	AffinityMask    uintptr
	BasePriority    uintptr
	UniqueProcessID uintptr
	InheritedFromID uintptr
}

func readRemote(h windows.Handle, addr uintptr, buf []byte) bool {
	if addr == 0 || len(buf) == 0 {
		return false
	}
	var n uintptr
	err := windows.ReadProcessMemory(h, addr, &buf[0], uintptr(len(buf)), &n)
	return err == nil && n == uintptr(len(buf))
}

func remotePtr(buf []byte, off uintptr) uintptr {
	return *(*uintptr)(unsafe.Pointer(&buf[off]))
}

// processCwd 從對方的 PEB 讀出目前的工作目錄。
// 需要 PROCESS_VM_READ，所以只讀得到同一個使用者的行程（或以系統管理員執行時）；
// 32 位元行程的結構不同，略過。任何一步失敗都回傳空字串。
func processCwd(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	var wow64 bool
	if err := windows.IsWow64Process(h, &wow64); err != nil || wow64 {
		return ""
	}
	var pbi processBasicInfo
	var ret uint32
	if err := windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation,
		unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), &ret); err != nil {
		return ""
	}

	peb := make([]byte, unsafe.Sizeof(windows.PEB{}))
	if !readRemote(h, pbi.PebBaseAddress, peb) {
		return ""
	}
	params := make([]byte, unsafe.Sizeof(windows.RTL_USER_PROCESS_PARAMETERS{}))
	if !readRemote(h, remotePtr(peb, unsafe.Offsetof(windows.PEB{}.ProcessParameters)), params) {
		return ""
	}
	// CurrentDirectory.DosPath 是 UNICODE_STRING：Length（位元組數）在最前面，Buffer 指標在其後。
	dosPath := unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.CurrentDirectory)
	length := uintptr(*(*uint16)(unsafe.Pointer(&params[dosPath])))
	buffer := remotePtr(params, dosPath+unsafe.Offsetof(windows.NTUnicodeString{}.Buffer))
	if length == 0 || length%2 != 0 {
		return ""
	}
	raw := make([]byte, length)
	if !readRemote(h, buffer, raw) {
		return ""
	}
	dir := windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&raw[0])), length/2))
	if len(dir) > 3 {
		dir = strings.TrimRight(dir, `\`)
	}
	return dir
}

// isSessionRoot 回報祖先鏈該不該在這個行程停下：再往上就是系統本身，對使用者沒有意義。
func isSessionRoot(image string) bool {
	switch baseName(image) {
	case "explorer", "services", "svchost", "wininit", "winlogon", "smss", "csrss", "system":
		return true
	}
	return false
}

// winAncestors 沿著父 PID 往上走，最多 maxAncestors 層。
// Windows 不會在父行程結束時更新子行程記錄的父 PID，那個 PID 可能已被不相干的行程重複使用：
// 「父行程」的建立時間若晚於子行程，就不是真正的父行程，祖先鏈到此為止。
func winAncestors(child winProcInfo, childCreated uint64, procs map[uint32]winProcInfo) []procRef {
	var chain []procRef
	for len(chain) < maxAncestors {
		parent, ok := procs[child.PPID]
		if !ok || child.PPID == 0 || parent.PID == child.PID {
			break
		}
		d := queryWinProcess(parent.PID)
		if d.Created != 0 && childCreated != 0 && d.Created > childCreated {
			break
		}
		chain = append(chain, procRef{PID: int(parent.PID), Name: parent.Name, Cmd: d.Cmdline})
		// 開不了的行程無從比對建立時間，再往上就不可靠了。
		if !d.OK || isSessionRoot(parent.Name) {
			break
		}
		child, childCreated = parent, d.Created
	}
	return chain
}

var (
	errPIDReused  = errors.New("pid reused")
	errStillAlive = errors.New("still alive")
	errGone       = errors.New("already gone")
)

// killWinProcess 終止行程。created 不為 0 時先比對建立時間，避免 PID 已被別的行程重複使用。
func killWinProcess(pid uint32, created uint64) error {
	access := uint32(windows.PROCESS_TERMINATE | windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.SYNCHRONIZE)
	h, err := windows.OpenProcess(access, false, pid)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return errGone
		}
		return err
	}
	defer windows.CloseHandle(h)

	if created != 0 {
		if cur := processCreated(h); cur != 0 && cur != created {
			return errPIDReused
		}
	}
	if err := windows.TerminateProcess(h, 1); err != nil {
		return err
	}
	if ev, _ := windows.WaitForSingleObject(h, 3000); ev != windows.WAIT_OBJECT_0 {
		return errStillAlive
	}
	return nil
}

// baseName 回傳去掉 .exe 的小寫名稱，供比對用。
func baseName(image string) string {
	return strings.TrimSuffix(strings.ToLower(image), ".exe")
}

func isRelayImage(image string) bool {
	switch baseName(image) {
	case "wslrelay", "wslhost":
		return true
	}
	return false
}

// winProtectReason 對不該由本工具關閉的行程回傳說明文字；可以關閉則回傳空字串。
func winProtectReason(pid uint32, image string) string {
	if pid == 0 || pid == 4 {
		return T.ProtectKernel
	}
	switch baseName(image) {
	case "csrss", "wininit", "lsass", "services", "smss", "winlogon":
		return T.ProtectSystem
	case "vmmem", "vmmemwsl", "wslservice":
		return T.ProtectWSL
	case "wslrelay", "wslhost":
		return T.ProtectRelay
	case "svchost":
		return T.ProtectSvchost
	case "com.docker.backend":
		return T.ProtectDocker
	}
	return ""
}
