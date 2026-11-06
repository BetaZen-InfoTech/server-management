package services

import (
	"net"
	"testing"
)

func TestDecidePort(t *testing.T) {
	always := func(int) bool { return true }
	never := func(int) bool { return false }

	// free explicit port is honored
	if p, err := decidePort(map[int]bool{}, 3555, false, always); err != nil || p != 3555 {
		t.Fatalf("free want: got %d, %v", p, err)
	}
	// DB-used explicit port, no reassign -> error
	if _, err := decidePort(map[int]bool{3555: true}, 3555, false, always); err == nil {
		t.Fatal("expected error for used port")
	}
	// OS-bound explicit port, no reassign -> error
	if _, err := decidePort(map[int]bool{}, 3555, false, never); err == nil {
		t.Fatal("expected error for OS-bound port")
	}
	// used + reassign -> different free port
	p, err := decidePort(map[int]bool{3555: true}, 3555, true, always)
	if err != nil || p == 3555 || p == 0 {
		t.Fatalf("reassign: got %d, %v", p, err)
	}
	// auto
	p, err = decidePort(map[int]bool{}, 0, false, always)
	if err != nil || p == 0 {
		t.Fatalf("auto: got %d, %v", p, err)
	}
}

func TestDecidePortRealListener(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot listen")
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	if isPortFree(port) {
		t.Skip("isPortFree does not probe loopback on this platform")
	}
	if _, err := decidePort(map[int]bool{}, port, false, isPortFree); err == nil {
		t.Fatal("expected error for OS-bound port")
	}
	p, err := decidePort(map[int]bool{}, port, true, isPortFree)
	if err != nil || p == port {
		t.Fatalf("reassign off busy port: got %d, %v", p, err)
	}
}
