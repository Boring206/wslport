# wslport 探測腳本（唯讀）。由 wslport.exe 經 stdin 送進 distro，以 root 執行：
#   wsl.exe -d <distro> -u root -e sh -s -- <port>
# $1 = port；0 表示列出全部監聽者。
# 每筆輸出都以 @wslport 開頭、tab 分隔，wslport.exe 只認這個前綴的行。
main() {
	P='@wslport'
	port="$1"
	[ "$port" = 0 ] && port=

	emit() { printf '%s\tproc\t%s\t%s\t%s\n' "$P" "$pid" "$1" "$2"; }
	flat() { tr '\000\n\t' '   ' <"$1" 2>/dev/null; }

	printf '%s\tmode\t%s\n' "$P" "$(wslinfo --networking-mode 2>/dev/null)"
	printf '%s\tuptime\t%s\n' "$P" "$(cut -d' ' -f1 /proc/uptime 2>/dev/null)"
	printf '%s\tclk\t%s\n' "$P" "$(getconf CLK_TCK 2>/dev/null || echo 100)"

	if command -v ss >/dev/null 2>&1; then
		tool=ss
		out=$(ss -ltnp 2>/dev/null)
	elif command -v netstat >/dev/null 2>&1; then
		tool=netstat
		out=$(netstat -ltnp 2>/dev/null)
	else
		printf '%s\tnotool\n' "$P"
		return 0
	fi
	printf '%s\ttool\t%s\n' "$P" "$tool"

	# 兩種工具的第 4 欄都是「本機位址:port」；標題列的第 1 欄不是 LISTEN 也不是 tcp*。
	lines=$(printf '%s\n' "$out" | awk -v p="$port" '
		$1 != "LISTEN" && $1 !~ /^tcp/ { next }
		{ n = split($4, a, ":"); if (p == "" || a[n] == p) print }')
	[ -n "$lines" ] || return 0

	printf '%s\n' "$lines" | while IFS= read -r line; do
		printf '%s\tsock\t%s\n' "$P" "$line"
	done

	if [ "$tool" = ss ]; then
		pids=$(printf '%s\n' "$lines" | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -un)
	else
		pids=$(printf '%s\n' "$lines" | awk '{ split($7, b, "/"); if (b[1] ~ /^[0-9]+$/) print b[1] }' | sort -un)
	fi

	for pid in $pids; do
		d=/proc/$pid
		[ -d "$d" ] || continue
		ppid=$(awk '/^PPid:/ { print $2 }' "$d/status" 2>/dev/null)
		emit comm "$(cat "$d/comm" 2>/dev/null)"
		emit user "$(stat -c %U "$d" 2>/dev/null)"
		emit ppid "$ppid"
		# /proc/PID/stat 的 comm 可能含空白與括號，先砍到最後一個 ")" 再數欄位：
		# 原第 22 欄 starttime 變成第 20 欄。
		emit start "$(sed 's/.*) //' "$d/stat" 2>/dev/null | awk '{ print $20 }')"
		emit cwd "$(readlink "$d/cwd" 2>/dev/null)"
		emit exe "$(readlink "$d/exe" 2>/dev/null)"
		emit cgroup "$(tr '\n' ';' <"$d/cgroup" 2>/dev/null)"
		emit cmd "$(flat "$d/cmdline")"
		# 往上列出最多 5 層祖先（不含 PID 1），每筆是「PID<TAB>comm<TAB>cmdline」。
		anc=$ppid
		depth=0
		while [ -n "$anc" ] && [ "$anc" -gt 1 ] && [ "$depth" -lt 5 ]; do
			[ -d "/proc/$anc" ] || break
			emit anc "$(printf '%s\t%s\t%s' "$anc" "$(cat "/proc/$anc/comm" 2>/dev/null)" "$(flat "/proc/$anc/cmdline")")"
			anc=$(awk '/^PPid:/ { print $2 }' "/proc/$anc/status" 2>/dev/null)
			depth=$((depth + 1))
		done
	done
}
main "$@" </dev/null
