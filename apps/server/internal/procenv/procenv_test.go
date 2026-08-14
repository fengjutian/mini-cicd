package procenv

import (
	"os"
	"strings"
	"testing"
)

func TestSafeExcludesServiceSecrets(t *testing.T) {
	t.Setenv("MINICICD_MASTER_KEY", "top-secret")
	t.Setenv("MINICICD_TEST_PRIVATE", "private")
	joined := strings.Join(Safe(), "\n")
	if strings.Contains(joined, "MINICICD_") || strings.Contains(joined, "top-secret") {
		t.Fatalf("service secret inherited: %s", joined)
	}
	if os.Getenv("PATH") != "" && !strings.Contains(strings.ToUpper(joined), "PATH=") {
		t.Fatal("PATH was not preserved")
	}
}
