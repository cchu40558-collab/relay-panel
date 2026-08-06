package service

import (
	"net"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestCheckLineInboundListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	inbound := &model.Inbound{Listen: "0.0.0.0", Port: port}
	if err := checkLineInboundListener(inbound); err != nil {
		t.Fatalf("listener check failed: %v", err)
	}

	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if err := checkLineInboundListener(inbound); err == nil {
		t.Fatal("non-listening port must fail the runtime check")
	}
}
