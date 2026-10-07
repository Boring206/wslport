# wslport

[English](README.md) | **繁體中文**

查出是誰佔用了 port，連 WSL 裡的行程都追得到，確認後幫你關掉。

在 Windows 遇到「port 3000 已被佔用」時，`netstat` 或工作管理員查到的佔用者常常只是 `wslrelay.exe`。
那是 WSL 的 localhost 轉送程式，真正的佔用者在某個 distro 裡，而且把 `wslrelay.exe` 關掉只會讓所有 WSL
port 的轉送一起失效。`wslport` 直接告訴你答案：

```
> wslport 3000
Port 3000 → WSL「Ubuntu-24.04」裡的 node (PID 4321)
  位址     *:3000
  指令     next-server (v15.1.0)
  目錄     /home/me/projects/my-app
  使用者   me
  啟動     2 小時前
  父行程   sh -c next dev (PID 4300)
           ← npm run dev (PID 4290)
           ← -bash (PID 512)
  Windows 端的 wslrelay.exe (PID 9876) 只是轉送，真正的佔用者在 WSL 裡。

要關掉 node (PID 4321) 嗎？ [y/N]
```

## 安裝

需要 Node.js 18 以上。在 Windows 的終端機或 WSL 裡安裝都可以：

```
npm install -g wslport
```

也可以不安裝直接跑：

```
npx wslport 3000
```

套件裡是一支編好的 Windows 執行檔（x64 與 arm64），不需要另外安裝 Go。在 WSL 裡安裝時，同一支執行檔會透過
WSL 的 Windows 互通功能執行。

## 用法

```
wslport              列出 Windows 與各 distro 所有監聽中的 port
wslport <port>       查這個 port 的佔用者，找到後詢問是否關閉

  -n, --no-kill      只查詢，不詢問是否關閉
  -k, --kill         不詢問，直接關閉（只在佔用者唯一時有效）
  -f, --force        WSL 裡的行程直接用 SIGKILL 強制終止
      --lang <語言>  介面語言：en 或 zh-TW
      --debug        顯示每個步驟的耗時與探測結果
  -h, --help         顯示說明
  -v, --version      顯示版本
```

結束碼：`0` 找到佔用者（或已關閉）、`1` port 沒有人使用、`2` 發生錯誤，或指定了 `-k` 卻沒能關閉。

port 可以寫成 `3000` 或 `:3000`，選項放在 port 前後都可以。

## 介面語言

介面有英文與繁體中文。預設跟著 Windows 的顯示語言：繁體中文（台灣、香港、澳門）顯示中文，其他一律顯示英文。

要指定語言，可以用 `--lang en`、`--lang zh-TW`，或設定環境變數 `WSLPORT_LANG`。

## 它會告訴你什麼

- **Windows 的程式**：名稱、PID、完整路徑、指令列、工作目錄、啟動時間。
- **WSL 裡的行程**：哪個 distro、名稱、PID、指令列、工作目錄、使用者；如果是 systemd 服務，會顯示服務單位名稱。
- **是誰啟動它的**：往上列出最多三層父行程，所以看得出這個 `node` 是哪個終端機裡的 `npm run dev` 帶起來的。
- **Docker 容器**：佔用者是 Docker 替容器開的 port 時，顯示容器名稱與映像，關閉時改用 `docker stop`。
- **沒有人監聽卻綁不上的 port**：port 落在 Windows 的保留埠範圍（Hyper-V／WinNAT）時，`netstat` 什麼都查不到，
  程式卻會收到「存取被拒」。`wslport` 會指出是哪一段範圍，並附上暫時與永久的解法。
- **被對外連線暫時借用的 port**：系統把這個 port 分配給某條對外連線時，會說明是哪個程式，但不會去關它。

## 關閉行程時的保護

- 一律先顯示、再詢問；沒有可互動的輸入時視為「否」。
- 關閉前會比對行程的啟動時間，確認 PID 沒有被別的行程重複使用。
- WSL 裡的行程先送 `SIGTERM`，3 秒內沒結束才詢問是否 `SIGKILL`。
- 關閉後會再查一次。如果 port 又被同名的行程佔用，會提示是 systemd、PM2 或 Windows 服務把它重新啟動了。
- 下列對象只顯示、不提供關閉：Windows 核心（PID 4）、關鍵系統行程、`svchost.exe`、WSL 本身的行程，以及
  `wslrelay.exe`。
- `wslport` 只查詢與關閉行程，不會更改任何系統設定；修復保留埠範圍的指令只會印出來，由你自己決定要不要執行。

## 運作方式

1. 直接呼叫 Windows API（`GetExtendedTcpTable`）讀取 TCP 表，不解析 `netstat` 的文字輸出，所以不受系統語系影響。
2. 同時對每個**執行中**的 WSL2 distro 執行一段唯讀的探測腳本（`ss` 加上 `/proc`），以 root 身分執行才看得到所有使用者的行程。已停止的 distro 不會被啟動。
3. 合併兩邊的結果：Windows 端若是 `wslrelay.exe`，而 WSL 裡有同一個 port 的監聽者，就以 WSL 裡的行程為準。

不論 Windows 端查到什麼，都會探測 WSL，所以在 mirrored 網路模式（Windows 端完全看不到佔用者）下也查得到。

## 限制

- 只處理 TCP。
- 不追進 WSL1 的 distro；WSL1 的行程會直接出現在 Windows 這一側。
- 關閉服務或其他使用者的 Windows 行程需要系統管理員權限，請用「以系統管理員身分執行」開啟終端機。這類行程的路徑、指令列與工作目錄也讀不到。
- distro 裡需要有 `ss`（iproute2）或 `netstat`，多數 distro 預設就有。
- 在 PowerShell 5.1 裡把輸出接到管線時，中文可能變成亂碼；直接顯示在終端機上沒有問題。

## 疑難排解

- **查不到 WSL 裡的行程**：加上 `--debug` 看每個 distro 的探測結果。distro 必須是執行中的 WSL2，而且裡面要有 `ss` 或 `netstat`。
- **每次執行都要等一兩秒**：`--debug` 的第一行 `process creation to program start` 如果就佔了大部分時間，延遲是發生在 wslport 開始執行之前（例如防毒軟體在掃描執行檔），不是查詢本身慢；通常把 wslport 的執行檔加進防毒軟體的例外清單就能解決。查詢本身通常在半秒內完成，時間主要花在呼叫 `wsl.exe`。
- **在 WSL 裡出現「無法執行 Windows 程式」**：WSL 的 Windows 互通被關閉了，請檢查 `/etc/wsl.conf` 的 `[interop]` 設定。
- **關閉後 port 馬上又被佔用**：有監督程式（systemd、PM2、Docker 的 restart policy）在重新啟動它，要從監督程式那邊停止；wslport 會指出是哪一個。

## 開發

需要 Go 1.24 以上與 Node.js。

```
npm test         # go vet 加上單元測試
npm run build    # 編出 bin/wslport-x64.exe 與 bin/wslport-arm64.exe
npm run e2e      # 端對端測試：在 WSL 裡實際啟動、查詢並關閉測試用的行程
node bin/wslport.js 3000
```

在 WSL 裡開發時，如果 WSL 沒有安裝 Go，建置腳本會改用 Windows 上的 `go.exe`；也可以用環境變數 `GO` 指定路徑。

介面文字都在 `i18n.go`，中英文各一份；新增或修改文字時兩份都要改，測試會檢查有沒有漏掉。

## 授權

[MIT](LICENSE)
