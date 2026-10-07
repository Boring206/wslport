package main

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TCP_TABLE_CLASS 與 MIB_TCP_STATE 的值（iphlpapi.h / tcpmib.h）。
const (
	tcpTableOwnerPIDListener = 3
	tcpTableOwnerPIDAll      = 5
	mibTCPStateListen        = 2
)

var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// tcpRow 是 Windows TCP 表解碼後的一列。
type tcpRow struct {
	Addr  netip.Addr
	Port  int
	PID   uint32
	State uint32
}

// decodeTCP4Rows 解碼 MIB_TCPTABLE_OWNER_PID：4 bytes 的列數，接著每列 24 bytes。
// port 放在 DWORD 的低 16 位元，network byte order。
func decodeTCP4Rows(buf []byte) []tcpRow {
	const rowSize = 24
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf))
	rows := make([]tcpRow, 0, n)
	for i := 0; i < n; i++ {
		off := 4 + i*rowSize
		if off+rowSize > len(buf) {
			break
		}
		r := buf[off : off+rowSize]
		rows = append(rows, tcpRow{
			State: binary.LittleEndian.Uint32(r[0:]),
			Addr:  netip.AddrFrom4([4]byte(r[4:8])),
			Port:  int(binary.BigEndian.Uint16(r[8:])),
			PID:   binary.LittleEndian.Uint32(r[20:]),
		})
	}
	return rows
}

// decodeTCP6Rows 解碼 MIB_TCP6TABLE_OWNER_PID：每列 56 bytes，欄位順序和 v4 不同，
// state 在 offset 48、pid 在 52。
func decodeTCP6Rows(buf []byte) []tcpRow {
	const rowSize = 56
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf))
	rows := make([]tcpRow, 0, n)
	for i := 0; i < n; i++ {
		off := 4 + i*rowSize
		if off+rowSize > len(buf) {
			break
		}
		r := buf[off : off+rowSize]
		rows = append(rows, tcpRow{
			Addr:  netip.AddrFrom16([16]byte(r[0:16])),
			Port:  int(binary.BigEndian.Uint16(r[20:])),
			State: binary.LittleEndian.Uint32(r[48:]),
			PID:   binary.LittleEndian.Uint32(r[52:]),
		})
	}
	return rows
}

func tcpTable(family, class uint32) ([]byte, error) {
	if err := procGetExtendedTcpTable.Find(); err != nil {
		return nil, err
	}
	size := uint32(32 * 1024)
	for attempt := 0; attempt < 8; attempt++ {
		buf := make([]byte, size)
		r, _, _ := procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
			0,
			uintptr(family),
			uintptr(class),
			0,
		)
		switch windows.Errno(r) {
		case windows.ERROR_SUCCESS:
			return buf, nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			// 表格可能在兩次呼叫之間變大，多留一些空間再試。
			size += 4096
		default:
			return nil, fmt.Errorf("GetExtendedTcpTable: %w", windows.Errno(r))
		}
	}
	return nil, fmt.Errorf("GetExtendedTcpTable: %w", windows.ERROR_INSUFFICIENT_BUFFER)
}

// windowsTCPRows 讀出 IPv4 與 IPv6 兩張表。
func windowsTCPRows(class uint32) ([]tcpRow, error) {
	b4, err := tcpTable(windows.AF_INET, class)
	if err != nil {
		return nil, err
	}
	b6, err := tcpTable(windows.AF_INET6, class)
	if err != nil {
		return nil, err
	}
	return append(decodeTCP4Rows(b4), decodeTCP6Rows(b6)...), nil
}

// formatAddr 把位址與 port 組成顯示用字串，IPv6 加上方括號。
func formatAddr(a netip.Addr, port int) string {
	return netip.AddrPortFrom(a, uint16(port)).String()
}
