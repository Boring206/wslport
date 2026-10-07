#!/usr/bin/env bash
# wslport 的端對端測試。在 WSL 裡執行，會實際啟動並關閉測試用的行程：
#   npm run build && npm run e2e
# 只會動到自己啟動的行程，使用隨機挑選、沒有人用的 port。
set -u

cd "$(dirname "$0")/.."
WP=(node bin/wslport.js)
DISTRO="${WSL_DISTRO_NAME:-}"
SYS32="$(wslpath 'C:\Windows\System32' 2>/dev/null)"

[ -n "$DISTRO" ] || { echo "請在 WSL 裡執行。"; exit 2; }
[ -f bin/wslport-x64.exe ] || { echo "找不到 bin/wslport-x64.exe，請先執行 npm run build。"; exit 2; }
command -v python3 >/dev/null || { echo "需要 python3。"; exit 2; }

pass=0
fail=0
skipped=0
pids=()
WORK="$(mktemp -d)"
WINWORK=""
mkdir -p "$WORK/app"

cleanup() {
	for p in "${pids[@]:-}"; do
		[ -n "$p" ] && kill -KILL "$p" 2>/dev/null
	done
	rm -rf "$WORK"
	[ -n "$WINWORK" ] && rm -rf "$WINWORK"
}
trap cleanup EXIT

section() { printf '\n\033[1m%s\033[0m\n' "$1"; }
ok() {
	pass=$((pass + 1))
	printf '  \033[32m通過\033[0m %s\n' "$1"
}
bad() {
	fail=$((fail + 1))
	printf '  \033[31m失敗\033[0m %s\n' "$1"
	[ -n "${2:-}" ] && printf '%s\n' "$2" | sed 's/^/        | /'
}
skip() {
	skipped=$((skipped + 1))
	printf '  \033[33m略過\033[0m %s\n' "$1"
}

# run：執行 wslport（stdin 是 /dev/null），輸出存進 OUT、結束碼存進 RC。
run() {
	OUT="$(timeout 90 "${WP[@]}" "$@" 2>&1 </dev/null)"
	RC=$?
}
# run_in <輸入> <參數…>：把輸入餵給確認提示。
run_in() {
	local input="$1"
	shift
	OUT="$(printf '%b' "$input" | timeout 90 "${WP[@]}" "$@" 2>&1)"
	RC=$?
}
expect_rc() {
	if [ "$RC" = "$1" ]; then ok "$2"; else bad "$2（結束碼 $RC，預期 $1）" "$OUT"; fi
}
expect_has() {
	if grep -qF -- "$1" <<<"$OUT"; then ok "$2"; else bad "$2（輸出裡找不到「$1」）" "$OUT"; fi
}
expect_true() {
	local label="$1"
	shift
	if "$@"; then ok "$label"; else bad "$label"; fi
}

listening() { ss -ltnH "sport = :$1" 2>/dev/null | grep -q .; }
not_listening() { ! listening "$1"; }
wait_listen() {
	for _ in $(seq 50); do
		listening "$1" && return 0
		sleep 0.1
	done
	return 1
}
win_listening() { "$SYS32/netstat.exe" -ano -p tcp 2>/dev/null | tr -d '\r' | grep -E ":$1\s" | grep -q LISTENING; }
win_not_listening() { ! win_listening "$1"; }

# Windows 保留埠範圍：每列「起 迄 [*]」。
RANGES="$("$SYS32/netsh.exe" int ipv4 show excludedportrange protocol=tcp 2>/dev/null | tr -d '\r' |
	awk '$1 ~ /^[0-9]+$/ && $2 ~ /^[0-9]+$/ { print $1, $2, $3 }')"

# free_port：挑一個兩邊都沒人用、也不在保留範圍內的 port。
free_port() {
	local p
	while :; do
		p=$((20000 + RANDOM % 10000))
		listening "$p" && continue
		awk -v p="$p" '$1 <= p && p <= $2 { hit = 1 } END { exit !hit }' <<<"$RANGES" && continue
		"$SYS32/netstat.exe" -ano -p tcp 2>/dev/null | grep -qE ":$p\s" && continue
		echo "$p"
		return
	done
}

# serve <port>：在 $WORK/app 啟動 python 的 http.server，PID 存進 SPID。
serve() {
	(cd "$WORK/app" && exec python3 -m http.server "$1" >/dev/null 2>&1) &
	SPID=$!
	disown "$SPID"
	pids+=("$SPID")
	wait_listen "$1"
}

MODE="$(wslinfo --networking-mode 2>/dev/null)"
echo "distro：$DISTRO　網路模式：${MODE:-未知}　版本：$("${WP[@]}" -v)"

section "1. 沒有人用的 port"
P=$(free_port)
run "$P" -n
expect_rc 1 "結束碼為 1"
expect_has "Port $P 目前沒有人使用" "說明 port 是空的"

section "2. 追進 WSL"
P=$(free_port)
serve "$P"
sleep 1.3 # NAT 模式下 wslrelay 最多延遲 1 秒才出現
run "$P" -n
expect_rc 0 "結束碼為 0"
expect_has "WSL「$DISTRO」裡的 python3 (PID $SPID)" "指出 distro、行程名稱與 PID"
expect_has "python3 -m http.server $P" "顯示指令列"
expect_has "$WORK/app" "顯示工作目錄"
if [ "$MODE" = nat ]; then
	expect_has "只是轉送" "Windows 端的 wslrelay 降為附註"
else
	skip "wslrelay 附註（只有 NAT 模式才有 relay）"
fi
run
expect_true "總表列出這個 port 與 distro" grep -qE "^$P +$DISTRO" <<<"$OUT"

section "3. 確認提示"
run_in 'n\n' "$P"
expect_has "要關掉 python3 (PID $SPID) 嗎？" "顯示確認提示"
expect_true "回答 n 不會關閉" listening "$P"
run "$P"
expect_true "沒有可互動的輸入時視為否" listening "$P"
run_in 'y\n' "$P"
expect_rc 0 "回答 y 後結束碼為 0"
expect_has "已關閉 python3 (PID $SPID)" "回報已關閉"
expect_has "Port $P 已釋放" "關閉後複查 port 已釋放"
expect_true "行程確實不在了" not_listening "$P"
sleep 1.5
run "$P" -n
expect_rc 1 "再查一次，port 已經沒人用"

section "4. -k 不詢問直接關閉"
P=$(free_port)
serve "$P"
run "$P" -k
expect_rc 0 "結束碼為 0"
expect_true "行程確實不在了" not_listening "$P"

section "5. 不理會 SIGTERM 的行程"
P=$(free_port)
python3 -c "
import signal, socket, sys, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
s = socket.socket()
s.bind(('127.0.0.1', int(sys.argv[1])))
s.listen()
time.sleep(600)" "$P" >/dev/null 2>&1 &
disown "$!"
pids+=("$!")
wait_listen "$P"
run "$P" -k
expect_rc 2 "-k 關不掉時結束碼為 2"
expect_has "沒有回應 SIGTERM" "說明行程沒有回應"
expect_true "沒有擅自強制終止" listening "$P"
run_in 'y\ny\n' "$P"
expect_has "要強制終止 (SIGKILL) 嗎？" "詢問是否改用 SIGKILL"
expect_true "同意後行程被終止" not_listening "$P"

section "6. 主行程與子行程共用同一個 port"
P=$(free_port)
python3 -c "
import os, socket, sys, time
s = socket.socket()
s.bind(('0.0.0.0', int(sys.argv[1])))
s.listen()
if os.fork() == 0:
    time.sleep(600)
time.sleep(600)" "$P" >/dev/null 2>&1 &
MASTER=$!
disown "$MASTER"
pids+=("$MASTER")
wait_listen "$P"
sleep 0.3
run "$P" -n
expect_has "python3 (PID $MASTER)" "以主行程為佔用者"
expect_has "共用這個 port" "列出共用 port 的子行程"
run "$P" -k
expect_has "還佔著 port $P" "關掉主行程後，指出子行程還佔著 port"
run "$P" -k
expect_true "再執行一次就釋放了" not_listening "$P"

section "7. 有監督程式會把它重新啟動"
P=$(free_port)
(while :; do
	python3 -m http.server "$P" >/dev/null 2>&1
	sleep 0.2
done) 2>/dev/null &
LOOP=$!
disown "$LOOP"
pids+=("$LOOP")
wait_listen "$P"
run "$P" -k
expect_has "重新啟動" "關閉後發現同名行程又佔用了 port"
kill -KILL "$LOOP" 2>/dev/null
run "$P" -kf
expect_true "停掉監督程式後可以關閉" not_listening "$P"

section "8. Windows 的行程"
if "$SYS32/cmd.exe" /c "where node" >/dev/null 2>&1; then
	P=$(free_port)
	WINWORK="$(wslpath "$("$SYS32/cmd.exe" /c 'echo %TEMP%' 2>/dev/null | tr -d '\r')")/wslport-e2e-$$"
	mkdir -p "$WINWORK"
	echo "require('http').createServer((q, r) => r.end('ok')).listen(Number(process.argv[2]), '127.0.0.1');" >"$WINWORK/server.js"
	(cd "$WINWORK" && exec "$SYS32/cmd.exe" /c node server.js "$P" >/dev/null 2>&1) &
	for _ in $(seq 50); do
		win_listening "$P" && break
		sleep 0.2
	done
	run "$P" -n
	expect_has "Windows 的 node.exe" "指出是 Windows 的 node.exe"
	expect_has "node  server.js $P" "顯示指令列"
	expect_has "$(wslpath -w "$WINWORK")" "顯示工作目錄"
	run "$P" -k
	expect_rc 0 "關閉後結束碼為 0"
	expect_true "Windows 端不再監聽" win_not_listening "$P"
else
	skip "Windows 上沒有 node，無法測試"
fi

section "9. 不提供關閉的系統行程"
run 135 -k
expect_rc 2 "-k 沒關掉任何東西時結束碼為 2"
expect_has "不提供關閉" "說明為什麼不能關"
expect_true "port 135 仍在監聽" win_listening 135

section "10. Windows 保留埠範圍"
BLOCKED="$(awk '$3 != "*" && $2 > $1 { print $1 + 1; exit }' <<<"$RANGES")"
if [ -n "$BLOCKED" ]; then
	run "$BLOCKED" -n
	expect_rc 0 "保留範圍內的 port 不算空的"
	expect_has "保留埠範圍" "指出落在保留埠範圍內"
	expect_has "net stop winnat" "附上解法"
else
	skip "這台電腦目前沒有保留埠範圍"
fi

section "11. 參數錯誤"
run 99999
expect_rc 2 "無效的 port"
run 3000 -n -k
expect_rc 2 "-n 與 -k 不能併用"
run -k
expect_rc 2 "-k 需要 port"

printf '\n通過 %d 項、失敗 %d 項、略過 %d 項\n' "$pass" "$fail" "$skipped"
[ "$fail" -eq 0 ]
