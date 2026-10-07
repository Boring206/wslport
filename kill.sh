# wslport 關閉腳本。由 wslport.exe 經 stdin 送進 distro，以 root 執行：
#   wsl.exe -d <distro> -u root -e sh -s -- <pid> <starttime> <TERM|KILL>
# 先比對 starttime（/proc/PID/stat 第 22 欄），確認 PID 沒被別的行程重複使用，
# 送出訊號後最多等 3 秒。結果以一行 @wslport<TAB>狀態 回報。
main() {
	P='@wslport'
	pid="$1"
	want="$2"
	sig="$3"

	field() { sed 's/.*) //' "/proc/$pid/stat" 2>/dev/null | awk -v n="$1" '{ print $n }'; }

	cur=$(field 20)
	if [ -z "$cur" ]; then
		printf '%s\tgone\n' "$P"
		return 0
	fi
	if [ "$cur" != "$want" ]; then
		printf '%s\tmismatch\n' "$P"
		return 0
	fi

	if [ "$sig" = KILL ]; then
		kill -KILL "$pid" 2>/dev/null
	else
		kill -TERM "$pid" 2>/dev/null
	fi

	i=0
	while [ "$i" -lt 30 ]; do
		state=$(field 1)
		# 行程消失、變成殭屍，或 PID 已換人，都算結束。
		if [ -z "$state" ] || [ "$state" = Z ] || [ "$(field 20)" != "$want" ]; then
			printf '%s\tkilled\n' "$P"
			return 0
		fi
		sleep 0.1 2>/dev/null || sleep 1
		i=$((i + 1))
	done
	printf '%s\talive\n' "$P"
}
main "$@" </dev/null
