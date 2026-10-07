package main

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// portRange 是 `netsh … show excludedportrange` 的一列。
type portRange struct {
	Start int
	End   int
	Admin bool // 帶 * 的列：使用者自行設定，仍可明確綁定
}

// defaultDynamicStart 是 Windows 預設的動態埠範圍起點。
const defaultDynamicStart = 49152

var (
	rangeRowRe = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s*(\*)?\s*$`)
	colonNumRe = regexp.MustCompile(`:\s*(\d+)\s*$`)
)

// parseExcludedRanges 只取「起、迄、是否有 *」，其餘文字（會被在地化）一律忽略。
func parseExcludedRanges(out string) []portRange {
	var ranges []portRange
	for _, line := range strings.Split(out, "\n") {
		m := rangeRowRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		start, _ := strconv.Atoi(m[1])
		end, _ := strconv.Atoi(m[2])
		ranges = append(ranges, portRange{Start: start, End: end, Admin: m[3] == "*"})
	}
	return ranges
}

// findRange 逐列判斷，不合併相鄰列。優先回傳會擋住綁定的（不帶 *）那一列。
func findRange(ranges []portRange, port int) (portRange, bool) {
	var admin portRange
	found := false
	for _, r := range ranges {
		if port < r.Start || port > r.End {
			continue
		}
		if !r.Admin {
			return r, true
		}
		admin, found = r, true
	}
	return admin, found
}

// parseDynamicPort 解析 `netsh … show dynamicport tcp`：依序出現的兩個「: 數字」是起點與數量。
func parseDynamicPort(out string) (start, num int, ok bool) {
	var nums []int
	for _, line := range strings.Split(out, "\n") {
		if m := colonNumRe.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			n, _ := strconv.Atoi(m[1])
			nums = append(nums, n)
		}
	}
	if len(nums) < 2 {
		return 0, 0, false
	}
	return nums[0], nums[1], true
}

// reservedDiag 是保留埠範圍的診斷結果。
type reservedDiag struct {
	Port         int
	InRange      bool      // port 是否落在某一列保留範圍內
	Range        portRange // 命中的列
	Family       string    // "IPv4"、"IPv6" 或 "IPv4／IPv6"
	DynamicStart int       // 0 表示讀不到
	DynamicNum   int
}

// Blocking 回報 port 是否落在會讓綁定失敗的保留列內。
func (d *reservedDiag) Blocking() bool { return d != nil && d.InRange && !d.Range.Admin }

// LowDynamicRange 回報動態埠範圍是否低於 Windows 預設值。
func (d *reservedDiag) LowDynamicRange() bool {
	return d != nil && d.DynamicStart > 0 && d.DynamicStart < defaultDynamicStart
}

// buildReservedDiag 由三份 netsh 輸出組出診斷。
func buildReservedDiag(port int, v4, v6, dynamic string) *reservedDiag {
	d := &reservedDiag{Port: port}
	if start, num, ok := parseDynamicPort(dynamic); ok {
		d.DynamicStart, d.DynamicNum = start, num
	}
	r4, ok4 := findRange(parseExcludedRanges(v4), port)
	r6, ok6 := findRange(parseExcludedRanges(v6), port)
	switch {
	case ok4 && ok6:
		d.Family = "IPv4／IPv6"
		d.Range = r4
		if r4.Admin && !r6.Admin {
			d.Range = r6
		}
	case ok4:
		d.Family, d.Range = "IPv4", r4
	case ok6:
		d.Family, d.Range = "IPv6", r6
	default:
		return d
	}
	d.InRange = true
	return d
}

// diagnoseReserved 執行 netsh 取得保留列與動態埠範圍。只讀取，不更改任何設定。
func diagnoseReserved(port int) *reservedDiag {
	netsh := system32("netsh.exe")
	cmds := [][]string{
		{"int", "ipv4", "show", "excludedportrange", "protocol=tcp"},
		{"int", "ipv6", "show", "excludedportrange", "protocol=tcp"},
		{"int", "ipv4", "show", "dynamicport", "tcp"},
	}
	outs := make([]string, len(cmds))
	var wg sync.WaitGroup
	for i, args := range cmds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, _ := runSystem(ctx, "", netsh, args...)
			outs[i] = string(out)
		}()
	}
	wg.Wait()
	return buildReservedDiag(port, outs[0], outs[1], outs[2])
}
