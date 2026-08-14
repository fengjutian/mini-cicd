package runner

import (
	"net"
	"testing"
)

func TestUnsafeHealthIP(t *testing.T) {
	for _, value := range []string{"0.0.0.0", "169.254.169.254", "fe80::1", "ff02::1"} {
		if !unsafeHealthIP(net.ParseIP(value)) {
			t.Fatalf("unsafe address accepted: %s", value)
		}
	}
	for _, value := range []string{"127.0.0.1", "10.0.0.2", "192.168.1.2", "8.8.8.8"} {
		if unsafeHealthIP(net.ParseIP(value)) {
			t.Fatalf("valid deployment target blocked: %s", value)
		}
	}
}
