package main

import (
	"reflect"
	"testing"
)

// zh-TW Windows 的輸出：文字被在地化，只有數字與 * 可以信任。
const excludedZhTW = "\r\n通訊協定 tcp 連接埠排除範圍\r\n\r\n" +
	"開始連接埠    結束連接埠      \r\n" +
	"----------    --------      \r\n" +
	"      1162        1261      \r\n" +
	"      1262        1361      \r\n" +
	"      1562        1661      \r\n" +
	"      1962        2061      \r\n" +
	"      5357        5357      \r\n" +
	"     50000       50059     *\r\n" +
	"\r\n* - 系統管理的連接埠排除。\r\n\r\n"

func TestParseExcludedRanges(t *testing.T) {
	want := []portRange{
		{1162, 1261, false}, {1262, 1361, false}, {1562, 1661, false},
		{1962, 2061, false}, {5357, 5357, false}, {50000, 50059, true},
	}
	if got := parseExcludedRanges(excludedZhTW); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

// 逐列判斷：1362–1561 是兩段保留列之間的空隙，不能因為相鄰列而被算進去。
func TestFindRange(t *testing.T) {
	ranges := parseExcludedRanges(excludedZhTW)
	cases := []struct {
		port  int
		found bool
		want  portRange
	}{
		{2000, true, portRange{1962, 2061, false}},
		{1261, true, portRange{1162, 1261, false}},
		{1262, true, portRange{1262, 1361, false}},
		{1400, false, portRange{}},
		{3000, false, portRange{}},
		{5357, true, portRange{5357, 5357, false}},
		{50010, true, portRange{50000, 50059, true}},
	}
	for _, c := range cases {
		got, ok := findRange(ranges, c.port)
		if ok != c.found || got != c.want {
			t.Errorf("findRange(%d) = %+v, %v; want %+v, %v", c.port, got, ok, c.want, c.found)
		}
	}
}

func TestParseDynamicPort(t *testing.T) {
	out := "\r\n通訊協定 tcp 動態連接埠範圍\r\n---------------------------------\r\n開始連接埠      : 1024\r\n連接埠數目      : 13977\r\n\r\n"
	start, num, ok := parseDynamicPort(out)
	if !ok || start != 1024 || num != 13977 {
		t.Errorf("got %d, %d, %v", start, num, ok)
	}
	if _, _, ok := parseDynamicPort("找不到元素。\r\n"); ok {
		t.Error("text without numbers must not parse")
	}
}

func TestBuildReservedDiag(t *testing.T) {
	dynamic := "Start Port      : 1024\nNumber of Ports : 13977\n"

	d := buildReservedDiag(2000, excludedZhTW, excludedZhTW, dynamic)
	if !d.Blocking() || d.Family != "IPv4/IPv6" || d.Range.Start != 1962 || !d.LowDynamicRange() {
		t.Errorf("2000: %+v", d)
	}
	// 帶 * 的列（使用者自訂）不會擋住綁定。
	if d := buildReservedDiag(50010, excludedZhTW, "", dynamic); d.Blocking() || !d.InRange || d.Family != "IPv4" {
		t.Errorf("50010: %+v", d)
	}
	// 不在任何保留列內，但動態埠範圍的資訊仍要保留（給非監聽佔用的說明用）。
	if d := buildReservedDiag(1400, excludedZhTW, excludedZhTW, dynamic); d.InRange || d.Blocking() || d.DynamicStart != 1024 {
		t.Errorf("1400: %+v", d)
	}
	// 同一個 port 在 v4 是自訂列、v6 是系統列：以會擋住綁定的那一列為準。
	v6 := "      50000       50059      \r\n"
	if d := buildReservedDiag(50010, excludedZhTW, v6, ""); !d.Blocking() || d.LowDynamicRange() {
		t.Errorf("mixed: %+v", d)
	}
	// Windows 預設的動態埠範圍不需要提醒。
	if d := buildReservedDiag(2000, excludedZhTW, "", "Start Port : 49152\nNumber of Ports : 16384\n"); d.LowDynamicRange() {
		t.Errorf("default dynamic range flagged: %+v", d)
	}
	var none *reservedDiag
	if none.Blocking() || none.LowDynamicRange() {
		t.Error("nil diag must be inert")
	}
}
