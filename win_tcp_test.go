package main

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func TestDecodeTCP4Rows(t *testing.T) {
	buf := make([]byte, 4+2*24)
	binary.LittleEndian.PutUint32(buf, 2)
	put := func(i int, state uint32, addr [4]byte, port uint16, pid uint32) {
		r := buf[4+i*24:]
		binary.LittleEndian.PutUint32(r[0:], state)
		copy(r[4:8], addr[:])
		binary.BigEndian.PutUint16(r[8:], port)
		r[10], r[11] = 0xAB, 0xCD // DWORD 高 16 位元的殘值不該影響 port
		binary.LittleEndian.PutUint32(r[20:], pid)
	}
	put(0, mibTCPStateListen, [4]byte{0, 0, 0, 0}, 3000, 1234)
	put(1, 5, [4]byte{127, 0, 0, 1}, 50123, 4)

	rows := decodeTCP4Rows(buf)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	want0 := tcpRow{Addr: netip.MustParseAddr("0.0.0.0"), Port: 3000, PID: 1234, State: mibTCPStateListen}
	want1 := tcpRow{Addr: netip.MustParseAddr("127.0.0.1"), Port: 50123, PID: 4, State: 5}
	if rows[0] != want0 || rows[1] != want1 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestDecodeTCP6Rows(t *testing.T) {
	buf := make([]byte, 4+56)
	binary.LittleEndian.PutUint32(buf, 1)
	r := buf[4:]
	r[15] = 1 // ::1
	binary.BigEndian.PutUint16(r[20:], 5173)
	binary.LittleEndian.PutUint32(r[48:], mibTCPStateListen)
	binary.LittleEndian.PutUint32(r[52:], 9876)

	rows := decodeTCP6Rows(buf)
	want := tcpRow{Addr: netip.MustParseAddr("::1"), Port: 5173, PID: 9876, State: mibTCPStateListen}
	if len(rows) != 1 || rows[0] != want {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
	if got := formatAddr(rows[0].Addr, rows[0].Port); got != "[::1]:5173" {
		t.Errorf("formatAddr = %q", got)
	}
}

// 列數宣稱比緩衝區實際容納的多時，不可以讀超出範圍。
func TestDecodeTruncatedTable(t *testing.T) {
	buf := make([]byte, 4+24)
	binary.LittleEndian.PutUint32(buf, 10)
	if rows := decodeTCP4Rows(buf); len(rows) != 1 {
		t.Errorf("v4: got %d rows, want 1", len(rows))
	}
	if rows := decodeTCP6Rows(buf); len(rows) != 0 {
		t.Errorf("v6: got %d rows, want 0", len(rows))
	}
	if rows := decodeTCP4Rows(nil); rows != nil {
		t.Errorf("nil buffer: got %v", rows)
	}
}
