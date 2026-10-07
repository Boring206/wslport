# 更新紀錄

## 0.1.0

首次發佈。

- `wslport <port>`：查出佔用 port 的行程。Windows 端顯示名稱、路徑、指令列、工作目錄與啟動時間；
  遇到 `wslrelay.exe` 時追進 WSL，顯示 distro、行程、指令列、工作目錄與使用者。
- 兩邊都會往上列出最多三層父行程。
- 確認後關閉：WSL 行程先送 `SIGTERM`，沒回應再詢問是否 `SIGKILL`；關閉前比對啟動時間避免誤殺，
  關閉後複查 port，並指出被監督程式重新啟動或由子行程繼續佔用的情況。
- `wslport`：列出 Windows 與所有執行中 WSL2 distro 的監聽 port。
- 診斷 Windows 保留埠範圍（Hyper-V／WinNAT）造成的「沒人監聽卻綁不上」，以及被對外連線暫時借用的 port。
- 佔用者是 Docker 發佈的 port 時，顯示容器並改用 `docker stop` 關閉。
- 以 npm 套件發佈，Windows 與 WSL 裡的 Node 都能安裝。
