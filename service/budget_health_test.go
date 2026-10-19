package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFailureCountCheckedDistinguishesMissingAndMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget")
	if n, err := (Budget{Path: path}).FailureCountChecked(time.Now()); err != nil || n != 0 {
		t.Fatalf("missing count = %d, %v", n, err)
	}
	now := time.Now()
	data := []byte(fmt.Sprintf("%d\n%d\n", now.Add(-time.Minute).UnixMilli(), now.Add(-20*time.Minute).UnixMilli()))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := (Budget{Path: path}).FailureCountChecked(now); err != nil || n != 1 {
		t.Fatalf("recent count = %d, %v", n, err)
	}
	if err := os.WriteFile(path, []byte("not-a-timestamp"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Budget{Path: path}).FailureCountChecked(now); err == nil {
		t.Fatal("malformed budget was accepted")
	}
}
