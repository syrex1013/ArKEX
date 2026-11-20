package zeroday

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanFileFindsCmdSink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\nfunc f(){ exec.Command(\"sh\", \"-c\", x) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sinks, err := scanFile(path, "go")
	if err != nil {
		t.Fatal(err)
	}
	if len(sinks) == 0 {
		t.Fatal("expected cmdi sink")
	}
}

func TestAnalyzePatchDiffTODO(t *testing.T) {
	diff := "+++ b/a.go\n@@\n+// TODO fix properly\n"
	f := analyzePatchDiff(diff)
	if len(f) == 0 {
		t.Fatal("expected todo finding")
	}
}
