package main

import (
	"strings"

	"golang.org/x/sys/windows"
)

// 介面文字集中在這個檔案：catalog 的每個欄位是一句話，zhTW 與 enUS 各有一份。
// 新增文字時兩份都要填，TestCatalogParity 會檢查有沒有漏掉，以及兩邊的格式參數是否一致。
// 需要調換參數順序時用 %[1]s 這種寫法。

type language int

const (
	langEN language = iota
	langZhTW
)

// envLang 是指定語言的環境變數；在 WSL 裡由啟動器轉成 --lang 參數。
const envLang = "WSLPORT_LANG"

// listComma 是輸入編號時也接受的中文頓號。
const listComma = '、'

// yesWords 是確認提示接受的肯定回答，不分介面語言。
var yesWords = []string{"y", "yes", "是", "好"}

// T 是目前使用的語言；由 setLanguage 在啟動時決定。
var T = &enUS

func setLanguage(l language) {
	if l == langZhTW {
		T = &zhTW
	} else {
		T = &enUS
	}
}

// parseLanguage 解析使用者明確指定的語言（--lang 或環境變數）。
func parseLanguage(s string) (language, bool) {
	tag := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", "-"))
	switch {
	case tag == "en" || strings.HasPrefix(tag, "en-"):
		return langEN, true
	case tag == "zh" || strings.HasPrefix(tag, "zh-"):
		return langZhTW, true
	}
	return langEN, false
}

// languageFromSystem 由 Windows 的顯示語言清單決定預設語言：
// 第一順位是繁體中文（台灣、香港、澳門）才用中文，其他一律英文。
func languageFromSystem(tags []string) language {
	if len(tags) == 0 {
		return langEN
	}
	tag := strings.ToLower(strings.ReplaceAll(tags[0], "_", "-"))
	if !strings.HasPrefix(tag, "zh") {
		return langEN
	}
	for _, mark := range []string{"hant", "-tw", "-hk", "-mo"} {
		if strings.Contains(tag, mark) {
			return langZhTW
		}
	}
	return langEN
}

func systemLanguageTags() []string {
	tags, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return nil
	}
	return tags
}

// pickLanguage 決定介面語言：--lang 參數優先（重複出現時以最後一個為準），
// 其次是環境變數，最後看系統語言。參數寫錯時先退回後面的來源，錯誤由 parseArgs 回報。
func pickLanguage(args []string, env string, system []string) language {
	chosen, found := langEN, false
	for i, a := range args {
		value := ""
		switch {
		case a == "--lang" && i+1 < len(args):
			value = args[i+1]
		case strings.HasPrefix(a, "--lang="):
			value = strings.TrimPrefix(a, "--lang=")
		default:
			continue
		}
		if l, ok := parseLanguage(value); ok {
			chosen, found = l, true
		}
	}
	if found {
		return chosen
	}
	if l, ok := parseLanguage(env); ok {
		return l
	}
	return languageFromSystem(system)
}

type catalog struct {
	Usage string

	// 參數錯誤
	ErrorWithHint string // err
	ErrorLine     string // err
	UnknownFlag   string // 參數
	BadPort       string // 參數
	OnePort       string
	NeedPort      string // 旗標
	FlagConflict  string
	BadLang       string // 值
	NeedLangValue string

	// 不提供關閉的原因
	ProtectKernel  string
	ProtectSystem  string
	ProtectWSL     string
	ProtectRelay   string
	ProtectSvchost string
	ProtectDocker  string
	ProtectDead    string
	DeadOwner      string

	// 探測
	ProbeFailed  string // distro, err
	ProbeTimeout string
	NoOutput     string
	NoTool       string // distro
	WSL1Skipped  string // distro
	OrphanShort  string

	// 時間
	JustNow    string
	SecondsAgo string // n
	MinutesAgo string // n
	HoursAgo   string // n
	DaysAgo    string // n

	// 佔用者
	WhoWindows  string // 行程
	WhoWSL      string // distro, 行程
	ListSep     string
	WorkersText string // 數量, PID 清單
	Container   string // 名稱, 映像
	TargetCont  string // 名稱清單

	LabelAddress   string
	LabelPath      string
	LabelCommand   string
	LabelDir       string
	LabelUser      string
	LabelStarted   string
	LabelParent    string
	LabelWorkers   string
	LabelService   string
	LabelContainer string
	LabelMaybe     string

	NoteInContainer  string
	NoteNotForwarded string
	NoteRelayBlocked string // 起, 迄, port
	NoteNoRelayYet   string
	NoteLimited      string
	NoteService      string

	// 保留埠範圍與動態埠範圍
	DynamicAdvice     string // 起點, 預設起點
	ReservedSingleFix string
	ReservedRangeWhy  string
	ReservedRangeFix  string
	ReservedOrChange  string
	ReservedSingle    string // port, 位址家族
	ReservedRange     string // port, 範圍, 位址家族
	AdminRangeInfo    string // 起, 迄

	// 報告
	OrphanHead    string // port, 位址清單
	OrphanNote    string
	RelayFootnote string // 行程
	EphemeralHead string // port, 行程清單
	EphemeralWhy  string
	EphemeralWait string
	Free          string // port
	MirroredNote  string

	// 總表
	ListEmpty         string
	ListHeaders       [6]string
	ListRelayFootnote string

	// 關閉
	KillNothing         string
	AskKill             string // 對象
	MultiNoK            string
	AskWhich            string // 編號清單
	NoSuchNumber        string // 輸入
	StopContainerFailed string // 名稱, err
	ContainerStopped    string // 名稱
	Killed              string // 對象
	AlreadyGone         string // 對象
	PIDReused           string // pid
	WinStillAlive       string // 對象
	AccessDenied        string // 對象
	KillFailed          string // 對象, err
	NoSigterm           string // 對象
	UseForce            string
	AskSigkill          string
	SigkillFailed       string // 對象

	// 關閉後複查
	Released       string // port
	RelayLingers   string
	LeftoverWorker string // 子行程, port
	Respawned      string // port, 行程
	HintSystemd    string // 服務單位
	HintWinService string
	HintPM2        string
	HintParent     string // 父行程, pid
	StillHeld      string // port, 行程
}

var zhTW = catalog{
	Usage: `wslport — 查出是誰佔用了 port，連 WSL 裡的行程都追得到

用法：
  wslport              列出 Windows 與各 distro 所有監聽中的 port
  wslport <port>       查這個 port 的佔用者，找到後詢問是否關閉

選項：
  -n, --no-kill        只查詢，不詢問是否關閉
  -k, --kill           不詢問，直接關閉（只在佔用者唯一時有效）
  -f, --force          WSL 裡的行程直接用 SIGKILL 強制終止
      --lang <語言>    介面語言：en 或 zh-TW（預設依系統語言）
      --debug          顯示每個步驟的耗時與探測結果（回報問題時請附上）
  -h, --help           顯示這份說明
  -v, --version        顯示版本

結束碼：0 找到佔用者（或已關閉）、1 port 沒有人使用、2 發生錯誤或 -k 沒能關閉
`,

	ErrorWithHint: "wslport：%v\n用 wslport -h 查看用法。\n",
	ErrorLine:     "wslport：%v",
	UnknownFlag:   "不認得的參數「%s」",
	BadPort:       "「%s」不是有效的 port（請輸入 1–65535 的整數）",
	OnePort:       "一次只能查一個 port",
	NeedPort:      "%s 需要搭配 port 使用",
	FlagConflict:  "-n 不能和 -k、-f 一起使用",
	BadLang:       "不支援的語言「%s」（可用：en、zh-TW）",
	NeedLangValue: "--lang 後面要接語言（en 或 zh-TW）",

	ProtectKernel:  "這個 port 由 Windows 核心（System）持有，通常是 HTTP.sys、SMB 之類的系統元件，沒辦法用關閉行程的方式釋放。可以用 `netsh http show servicestate` 查是哪個服務註冊的。",
	ProtectSystem:  "這是 Windows 的關鍵系統行程，關掉會讓系統不穩定，wslport 不提供關閉。",
	ProtectWSL:     "這是 WSL 虛擬機本身的行程，wslport 不提供關閉。要關掉整個 WSL 請用 `wsl --shutdown`。",
	ProtectRelay:   "這是 WSL 的 localhost 轉送程式。關掉它會讓所有 WSL port 的轉送失效，要 `wsl --shutdown` 才會恢復，所以 wslport 不提供關閉。",
	ProtectSvchost: "這是 Windows 服務的宿主行程，裡面可能同時有好幾個服務，wslport 不提供關閉。如果是 port 轉送規則，可以用 `netsh interface portproxy show all` 查看。",
	ProtectDocker:  "這是 Docker Desktop 的後端程式，請用 `docker stop <容器>` 關閉對應的容器，不要直接關掉它。",
	ProtectDead:    "登記在這個 port 上的行程已經結束，socket 由它啟動的子行程繼承，無法確定是哪一個，所以 wslport 不提供關閉。",
	DeadOwner:      "已結束的行程",

	ProbeFailed:  "無法探測 distro「%s」：%v",
	ProbeTimeout: "探測逾時",
	NoOutput:     "沒有回應",
	NoTool:       "distro「%s」裡沒有 ss 也沒有 netstat，無法查詢（安裝 iproute2 即可）。",
	WSL1Skipped:  "distro「%s」是 WSL1，wslport 不會追進去；它的行程會直接出現在 Windows 這一側。",
	OrphanShort:  "（看不到擁有者）",

	JustNow:    "剛剛",
	SecondsAgo: "%d 秒前",
	MinutesAgo: "%d 分鐘前",
	HoursAgo:   "%d 小時前",
	DaysAgo:    "%d 天前",

	WhoWindows:  "Windows 的 %s",
	WhoWSL:      "WSL「%[1]s」裡的 %[2]s",
	ListSep:     "、",
	WorkersText: "另有 %d 個子行程共用這個 port（PID %s）",
	Container:   "%s（%s）",
	TargetCont:  "容器 %s",

	LabelAddress:   "位址",
	LabelPath:      "路徑",
	LabelCommand:   "指令",
	LabelDir:       "目錄",
	LabelUser:      "使用者",
	LabelStarted:   "啟動",
	LabelParent:    "父行程",
	LabelWorkers:   "子行程",
	LabelService:   "服務",
	LabelContainer: "容器",
	LabelMaybe:     "可能是",

	NoteInContainer:  "這個行程跑在容器裡，建議用容器工具（例如 docker stop）關閉。",
	NoteNotForwarded: "它綁定的位址不會被轉送到 Windows，所以不會和 Windows 上的程式搶這個 port。",
	NoteRelayBlocked: "Windows 沒辦法替它轉送：這個 port 落在 Windows 的保留範圍 %d–%d 內，所以從 Windows 連 localhost:%d 會失敗。",
	NoteNoRelayYet:   "Windows 這一側目前沒有對應的轉送；行程剛啟動的話，大約 1 秒內會出現。",
	NoteLimited:      "權限不足，讀不到路徑與指令。用「以系統管理員身分執行」開啟終端機可以看到更多。",
	NoteService:      "這是 Windows 服務，強制結束後可能會自動重新啟動；建議改用「服務」管理員或 `sc stop` 停止。",

	DynamicAdvice:     "\n  這台電腦的動態埠範圍從 %d 開始（Windows 預設是 %d），所以系統保留和對外連線\n  會落在開發常用的 port 上。永久解法（需要系統管理員權限，設定後重新開機）：\n",
	ReservedSingleFix: "  單一 port 的保留通常由系統元件持有，重新啟動 winnat 也不會釋放，建議換一個 port。",
	ReservedRangeWhy:  "  這類範圍通常是 Hyper-V／WinNAT（WSL、Docker 會用到）開機時動態保留的。",
	ReservedRangeFix:  "  暫時解法（需要系統管理員權限；重啟後範圍會重新分配，不保證避開）：",
	ReservedOrChange:  "  或者直接換一個 port。",
	ReservedSingle:    "%s 沒有任何行程在監聽，但它被 Windows 單獨保留了（%s），程式綁定時會被拒絕。",
	ReservedRange:     "%s 沒有任何行程在監聽，但它落在 Windows 的保留埠範圍 %s 內（%s），\n程式綁定時會被拒絕（存取被拒，錯誤 10013），看起來就像被佔用。",
	AdminRangeInfo:    "（它在使用者自訂的保留範圍 %d–%d 內；這種範圍不會擋住程式綁定。）",

	OrphanHead:    "%s → 在 WSL 虛擬機內有人監聽（%s），但看不到擁有者",
	OrphanNote:    "沒有任何執行中的 distro 認領它；可能屬於 WSL 本身、Docker Desktop，或上面探測失敗的 distro。",
	RelayFootnote: "Windows 端的 %s 只是轉送，真正的佔用者在 WSL 裡。",
	EphemeralHead: "%s 沒有任何行程在監聽，但目前被 %s 的連線當作本機埠使用中。",
	EphemeralWhy:  "  這是系統隨機分配給對外連線的 port，連線結束就會釋放，wslport 不會去關它。",
	EphemeralWait: "  稍等一下再試，或換一個 port。",
	Free:          "%s 目前沒有人使用。",
	MirroredNote:  "WSL 目前是 mirrored 網路模式，這個模式會另外保留一段 port，而且不會顯示在任何清單裡；如果還是綁不上，可能就是落在那一段。",

	ListEmpty:         "目前沒有任何監聽中的 port。",
	ListHeaders:       [6]string{"PORT", "位置", "PID", "行程", "位址", "指令"},
	ListRelayFootnote: "* 這個 port 由 wslrelay 轉送到 Windows 的 localhost。",

	KillNothing:         "wslport：-k 沒有關閉任何行程。",
	AskKill:             "要關掉 %s 嗎？ [y/N] ",
	MultiNoK:            "有多個佔用者，-k 不適用，請選擇要關閉的對象。",
	AskWhich:            "要關掉哪一個？輸入編號（%s），a 表示全部，直接按 Enter 取消：",
	NoSuchNumber:        "沒有編號「%s」，已取消。",
	StopContainerFailed: "停不了容器 %s：%v",
	ContainerStopped:    "已停止容器 %s。",
	Killed:              "已關閉 %s。",
	AlreadyGone:         "%s 已經不在了。",
	PIDReused:           "PID %d 已經換成別的行程，為了安全沒有關閉，請重新查詢。",
	WinStillAlive:       "已要求終止 %s，但它在 3 秒內沒有結束。",
	AccessDenied:        "關不掉 %s：權限不足。請用「以系統管理員身分執行」開啟 Windows 終端機後再試一次。",
	KillFailed:          "關不掉 %s：%v",
	NoSigterm:           "%s 在 3 秒內沒有回應 SIGTERM。",
	UseForce:            "加上 -f 可以直接強制終止。",
	AskSigkill:          "要強制終止 (SIGKILL) 嗎？ [y/N] ",
	SigkillFailed:       "已送出 SIGKILL，但 %s 仍然存在。",

	Released:       "Port %d 已釋放。",
	RelayLingers:   "（Windows 端的 wslrelay 轉送大約 1 秒內會消失。）",
	LeftoverWorker: "關掉的是主行程，但它的子行程 %[1]s 還佔著 port %[2]d；再執行一次 wslport %[2]d 就能關掉。",
	Respawned:      "Port %d 又被 %s 佔用了，看起來有監督程式把它重新啟動。",
	HintSystemd:    "  它屬於 systemd 服務 %[1]s，請在 distro 裡用 `sudo systemctl stop %[1]s` 停止。",
	HintWinService: "  它是 Windows 服務，請用「服務」管理員或 `sc stop` 停止。",
	HintPM2:        "  它由 PM2 管理，請用 `pm2 stop` 停止。",
	HintParent:     "  它的父行程是 %s (PID %d)，請從那裡停止。",
	StillHeld:      "Port %d 仍被 %s 佔用。",
}

var enUS = catalog{
	Usage: `wslport — find what is holding a port, following it into WSL

Usage:
  wslport              list every listening port on Windows and in each distro
  wslport <port>       show what holds this port, then offer to kill it

Options:
  -n, --no-kill        only look, never offer to kill
  -k, --kill           kill without asking (only when there is exactly one owner)
  -f, --force          kill WSL processes with SIGKILL straight away
      --lang <lang>    interface language: en or zh-TW (default: system language)
      --debug          show timings and probe results (include this in bug reports)
  -h, --help           show this help
  -v, --version        show the version

Exit codes: 0 an owner was found (or killed), 1 the port is not in use,
            2 an error occurred or -k could not kill anything
`,

	ErrorWithHint: "wslport: %v\nRun wslport -h for usage.\n",
	ErrorLine:     "wslport: %v",
	UnknownFlag:   "unknown option %q",
	BadPort:       "%q is not a valid port (use an integer from 1 to 65535)",
	OnePort:       "only one port can be looked up at a time",
	NeedPort:      "%s needs a port",
	FlagConflict:  "-n cannot be combined with -k or -f",
	BadLang:       "unsupported language %q (available: en, zh-TW)",
	NeedLangValue: "--lang needs a value (en or zh-TW)",

	ProtectKernel:  "This port is held by the Windows kernel (System), usually for a system component such as HTTP.sys or SMB, so killing a process cannot free it. Run `netsh http show servicestate` to see which service registered it.",
	ProtectSystem:  "This is a critical Windows system process. Killing it would destabilise the system, so wslport will not do it.",
	ProtectWSL:     "This process is part of the WSL virtual machine itself, so wslport will not kill it. To stop WSL entirely, run `wsl --shutdown`.",
	ProtectRelay:   "This is WSL's localhost forwarder. Killing it breaks forwarding for every WSL port until `wsl --shutdown`, so wslport will not do it.",
	ProtectSvchost: "This is a host process for Windows services and may be running several of them, so wslport will not kill it. If the port comes from a port-forwarding rule, check `netsh interface portproxy show all`.",
	ProtectDocker:  "This is the Docker Desktop backend. Stop the container with `docker stop <container>` instead of killing it.",
	ProtectDead:    "The process registered on this port has exited and a child it started inherited the socket. wslport cannot tell which one, so it will not kill anything.",
	DeadOwner:      "exited process",

	ProbeFailed:  "Could not probe distro %q: %v",
	ProbeTimeout: "probe timed out",
	NoOutput:     "no response",
	NoTool:       "Distro %q has neither ss nor netstat, so it cannot be queried (install iproute2).",
	WSL1Skipped:  "Distro %q is WSL1, which wslport does not look into; its processes show up on the Windows side directly.",
	OrphanShort:  "(owner not visible)",

	JustNow:    "just now",
	SecondsAgo: "%d sec ago",
	MinutesAgo: "%d min ago",
	HoursAgo:   "%d hr ago",
	DaysAgo:    "%d days ago",

	WhoWindows:  "%s on Windows",
	WhoWSL:      "%[2]s in WSL distro %[1]s",
	ListSep:     ", ",
	WorkersText: "%d other process(es) share this port (PID %s)",
	Container:   "%s (%s)",
	TargetCont:  "container %s",

	LabelAddress:   "Address",
	LabelPath:      "Path",
	LabelCommand:   "Command",
	LabelDir:       "Directory",
	LabelUser:      "User",
	LabelStarted:   "Started",
	LabelParent:    "Parent",
	LabelWorkers:   "Workers",
	LabelService:   "Service",
	LabelContainer: "Container",
	LabelMaybe:     "Possibly",

	NoteInContainer:  "This process runs inside a container; stop it with the container tooling (for example docker stop).",
	NoteNotForwarded: "The address it is bound to is not forwarded to Windows, so it does not compete with Windows programs for this port.",
	NoteRelayBlocked: "Windows cannot forward it: this port is inside the Windows reserved range %d–%d, so connecting to localhost:%d from Windows fails.",
	NoteNoRelayYet:   "Windows has no matching forwarder right now; if the process has just started, one appears within about a second.",
	NoteLimited:      "Not enough privileges to read the path and command line. Run from an elevated (Run as administrator) terminal to see more.",
	NoteService:      "This is a Windows service and may restart by itself after being killed; prefer the Services console or `sc stop`.",

	DynamicAdvice:     "\n  The dynamic port range on this machine starts at %d (the Windows default is %d), so system\n  reservations and outbound connections land on common development ports. Permanent fix\n  (needs an elevated terminal, then a reboot):\n",
	ReservedSingleFix: "  A single-port reservation is usually held by a system component and restarting winnat does not release it; use another port.",
	ReservedRangeWhy:  "  Ranges like this are usually reserved at boot by Hyper-V/WinNAT, which WSL and Docker rely on.",
	ReservedRangeFix:  "  Temporary fix (needs an elevated terminal; ranges are reassigned on restart, so it may not last):",
	ReservedOrChange:  "  Or simply use another port.",
	ReservedSingle:    "Nothing is listening on %s, but Windows has reserved it individually (%s), so binding is refused.",
	ReservedRange:     "Nothing is listening on %s, but it lies inside the Windows reserved port range %s (%s).\nBinding is refused (access denied, error 10013), which looks just like the port being in use.",
	AdminRangeInfo:    "(It is inside the administered reservation %d–%d; that kind of range does not stop programs from binding.)",

	OrphanHead:    "%s → something is listening inside the WSL VM (%s), but its owner is not visible",
	OrphanNote:    "No running distro claims it; it may belong to WSL itself, to Docker Desktop, or to a distro whose probe failed above.",
	RelayFootnote: "%s on the Windows side is only a forwarder; the real owner is inside WSL.",
	EphemeralHead: "Nothing is listening on %s, but it is currently the local port of a connection owned by %s.",
	EphemeralWhy:  "  The system picked it at random for an outbound connection. It is released when the connection ends, and wslport will not kill it.",
	EphemeralWait: "  Try again in a moment, or use another port.",
	Free:          "%s is not in use.",
	MirroredNote:  "WSL is in mirrored networking mode, which reserves an extra block of ports that no list shows; if binding still fails, the port may be inside that block.",

	ListEmpty:         "No listening ports.",
	ListHeaders:       [6]string{"PORT", "WHERE", "PID", "PROCESS", "ADDRESS", "COMMAND"},
	ListRelayFootnote: "* forwarded to localhost on Windows by wslrelay.",

	KillNothing:         "wslport: -k did not kill anything.",
	AskKill:             "Kill %s? [y/N] ",
	MultiNoK:            "There is more than one owner, so -k does not apply; choose what to kill.",
	AskWhich:            "Kill which one? Enter a number (%s), a for all, or press Enter to cancel: ",
	NoSuchNumber:        "There is no number %q; cancelled.",
	StopContainerFailed: "Could not stop container %s: %v",
	ContainerStopped:    "Stopped container %s.",
	Killed:              "Killed %s.",
	AlreadyGone:         "%s is already gone.",
	PIDReused:           "PID %d now belongs to a different process, so nothing was killed. Look the port up again.",
	WinStillAlive:       "Asked %s to terminate, but it did not exit within 3 seconds.",
	AccessDenied:        "Could not kill %s: access denied. Run again from an elevated (Run as administrator) Windows terminal.",
	KillFailed:          "Could not kill %s: %v",
	NoSigterm:           "%s did not respond to SIGTERM within 3 seconds.",
	UseForce:            "Add -f to force-kill it.",
	AskSigkill:          "Force-kill it with SIGKILL? [y/N] ",
	SigkillFailed:       "Sent SIGKILL, but %s is still there.",

	Released:       "Port %d is free.",
	RelayLingers:   " (The wslrelay forwarder on the Windows side goes away within about a second.)",
	LeftoverWorker: "That was the main process, but its child %[1]s still holds port %[2]d; run wslport %[2]d again to kill it.",
	Respawned:      "Port %d is held again by %s; a supervisor appears to have restarted it.",
	HintSystemd:    "  It belongs to the systemd unit %[1]s; stop it inside the distro with `sudo systemctl stop %[1]s`.",
	HintWinService: "  It is a Windows service; stop it from the Services console or with `sc stop`.",
	HintPM2:        "  PM2 manages it; stop it with `pm2 stop`.",
	HintParent:     "  Its parent is %s (PID %d); stop it from there.",
	StillHeld:      "Port %d is still held by %s.",
}
