#!/usr/bin/env bash
# End-to-end tests for wslport. Run inside WSL; they start, query and kill real test processes:
#   npm run build && npm run e2e
# Only processes started here are touched, on randomly chosen ports nobody is using.
set -u

cd "$(dirname "$0")/.."
# Assertions below match the English output, whatever the system language is.
WP=(node bin/wslport.js --lang en)
DISTRO="${WSL_DISTRO_NAME:-}"
SYS32="$(wslpath 'C:\Windows\System32' 2>/dev/null)"

[ -n "$DISTRO" ] || { echo "Run this inside WSL."; exit 2; }
[ -f bin/wslport-x64.exe ] || { echo "bin/wslport-x64.exe is missing; run npm run build first."; exit 2; }
command -v python3 >/dev/null || { echo "python3 is required."; exit 2; }

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
	printf '  \033[32mpass\033[0m %s\n' "$1"
}
bad() {
	fail=$((fail + 1))
	printf '  \033[31mFAIL\033[0m %s\n' "$1"
	[ -n "${2:-}" ] && printf '%s\n' "$2" | sed 's/^/        | /'
}
skip() {
	skipped=$((skipped + 1))
	printf '  \033[33mskip\033[0m %s\n' "$1"
}

# run: run wslport with stdin at /dev/null; output goes to OUT, exit code to RC.
run() {
	OUT="$(timeout 90 "${WP[@]}" "$@" 2>&1 </dev/null)"
	RC=$?
}
# run_in <input> <args…>: feed input to the confirmation prompt.
run_in() {
	local input="$1"
	shift
	OUT="$(printf '%b' "$input" | timeout 90 "${WP[@]}" "$@" 2>&1)"
	RC=$?
}
expect_rc() {
	if [ "$RC" = "$1" ]; then ok "$2"; else bad "$2 (exit code $RC, expected $1)" "$OUT"; fi
}
expect_has() {
	if grep -qF -- "$1" <<<"$OUT"; then ok "$2"; else bad "$2 (output lacks \"$1\")" "$OUT"; fi
}
expect_lacks() {
	if grep -qF -- "$1" <<<"$OUT"; then bad "$2 (output contains \"$1\")" "$OUT"; else ok "$2"; fi
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

# Windows reserved port ranges, one "start end [*]" per line.
RANGES="$("$SYS32/netsh.exe" int ipv4 show excludedportrange protocol=tcp 2>/dev/null | tr -d '\r' |
	awk '$1 ~ /^[0-9]+$/ && $2 ~ /^[0-9]+$/ { print $1, $2, $3 }')"

# free_port: a port unused on both sides and outside every reserved range.
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

# serve <port>: start python's http.server in $WORK/app; its PID goes to SPID.
serve() {
	(cd "$WORK/app" && exec python3 -m http.server "$1" >/dev/null 2>&1) &
	SPID=$!
	disown "$SPID"
	pids+=("$SPID")
	wait_listen "$1"
}

MODE="$(wslinfo --networking-mode 2>/dev/null)"
echo "distro: $DISTRO   networking mode: ${MODE:-unknown}   version: $("${WP[@]}" -v)"

section "1. A port nobody uses"
P=$(free_port)
run "$P" -n
expect_rc 1 "exit code is 1"
expect_has "Port $P is not in use." "says the port is free"

section "2. Following a port into WSL"
P=$(free_port)
serve "$P"
sleep 1.3 # in NAT mode wslrelay shows up to a second late
run "$P" -n
expect_rc 0 "exit code is 0"
expect_has "python3 (PID $SPID) in WSL distro $DISTRO" "names the distro, the process and its PID"
expect_has "python3 -m http.server $P" "shows the command line"
expect_has "$WORK/app" "shows the working directory"
if [ "$MODE" = nat ]; then
	expect_has "is only a forwarder" "demotes wslrelay on the Windows side to a footnote"
else
	# Outside NAT mode there is no relay, so nothing about forwarding should be said.
	expect_lacks "forwarder" "does not talk about forwarding outside NAT mode"
fi
run
expect_true "the list shows this port with its distro" grep -qE "^$P +$DISTRO" <<<"$OUT"

section "3. The confirmation prompt"
run_in 'n\n' "$P"
expect_has "Kill python3 (PID $SPID)? [y/N]" "asks before killing"
expect_true "answering n kills nothing" listening "$P"
run "$P"
expect_true "no interactive input counts as no" listening "$P"
run_in 'y\n' "$P"
expect_rc 0 "exit code is 0 after answering y"
expect_has "Killed python3 (PID $SPID)." "reports the kill"
expect_has "Port $P is free." "re-checks the port afterwards"
expect_true "the process is really gone" not_listening "$P"
sleep 1.5
run "$P" -n
expect_rc 1 "a second look finds the port unused"

section "4. -k kills without asking"
P=$(free_port)
serve "$P"
run "$P" -k
expect_rc 0 "exit code is 0"
expect_true "the process is really gone" not_listening "$P"

section "5. A process that ignores SIGTERM"
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
expect_rc 2 "exit code is 2 when -k cannot kill it"
expect_has "did not respond to SIGTERM" "says the process did not respond"
expect_true "does not force-kill on its own" listening "$P"
run_in 'y\ny\n' "$P"
expect_has "Force-kill it with SIGKILL?" "asks before escalating to SIGKILL"
expect_true "the process is killed once confirmed" not_listening "$P"

section "6. A master and a worker sharing the port"
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
expect_has "python3 (PID $MASTER)" "treats the master as the owner"
expect_has "share this port" "lists the worker sharing the port"
run "$P" -k
expect_has "still holds port $P" "after killing the master, points at the worker still holding the port"
run "$P" -k
expect_true "running it again frees the port" not_listening "$P"

section "7. A supervisor that restarts it"
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
expect_has "a supervisor appears to have restarted it" "notices the same program holding the port again"
kill -KILL "$LOOP" 2>/dev/null
run "$P" -kf
expect_true "the port is freed once the supervisor is stopped" not_listening "$P"

section "8. A Windows process"
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
	expect_has "node.exe (PID" "names node.exe"
	expect_has "on Windows" "places it on Windows"
	expect_has "node  server.js $P" "shows the command line"
	expect_has "$(wslpath -w "$WINWORK")" "shows the working directory"
	run "$P" -k
	expect_rc 0 "exit code is 0 after the kill"
	expect_true "Windows no longer listens on the port" win_not_listening "$P"
else
	skip "node is not installed on Windows"
fi

section "9. System processes it refuses to kill"
run 135 -k
expect_rc 2 "exit code is 2 when -k killed nothing"
expect_has "wslport will not kill it" "explains why it will not kill it"
expect_true "port 135 is still listening" win_listening 135

section "10. Windows reserved port ranges"
BLOCKED="$(awk '$3 != "*" && $2 > $1 { print $1 + 1; exit }' <<<"$RANGES")"
if [ -n "$BLOCKED" ]; then
	run "$BLOCKED" -n
	expect_rc 0 "a port inside a reserved range does not count as free"
	expect_has "reserved port range" "says it lies inside a reserved range"
	expect_has "net stop winnat" "includes the fix"
else
	skip "this machine has no reserved port ranges right now"
fi

section "11. Bad arguments"
run 99999
expect_rc 2 "invalid port"
run 3000 -n -k
expect_rc 2 "-n and -k cannot be combined"
run -k
expect_rc 2 "-k needs a port"
run 3000 --lang fr
expect_rc 2 "unsupported language"

section "12. Traditional Chinese output"
P=$(free_port)
run "$P" -n --lang zh-TW
expect_has "Port $P 目前沒有人使用。" "--lang zh-TW switches the interface to Chinese"
OUT="$(WSLPORT_LANG=zh-TW timeout 90 node bin/wslport.js "$P" -n 2>&1 </dev/null)"
expect_has "目前沒有人使用" "WSLPORT_LANG is honoured from inside WSL"

printf '\n%d passed, %d failed, %d skipped\n' "$pass" "$fail" "$skipped"
[ "$fail" -eq 0 ]
