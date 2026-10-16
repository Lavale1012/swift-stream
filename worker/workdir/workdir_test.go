package workdir

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenCreatesAPrivateDirectory(t *testing.T) {
	root := t.TempDir()
	d, removed, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("removed %d directories from an empty root", removed)
	}
	if filepath.Dir(d.Path) != root {
		t.Errorf("dir %s is not under %s", d.Path, root)
	}
	if err := os.WriteFile(filepath.Join(d.Path, "source.mp4"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.Path); !os.IsNotExist(err) {
		t.Errorf("Close left %s behind", d.Path)
	}
}

func TestOpenLeavesLiveWorkersAlone(t *testing.T) {
	root := t.TempDir()
	live, _, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()

	second, removed, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	if removed != 0 {
		t.Errorf("removed %d directories while their worker was running", removed)
	}
	if _, err := os.Stat(live.Path); err != nil {
		t.Errorf("live worker's directory is gone: %v", err)
	}
}

func TestOpenRemovesDirectoriesOfDeadWorkers(t *testing.T) {
	root := t.TempDir()

	// A worker that held its lock and then died, mid-job.
	dead := crashedWorker(t, root)
	// A directory from a crash that happened before it was even locked.
	if err := os.Mkdir(filepath.Join(root, "worker-unlocked"), 0o700); err != nil {
		t.Fatal(err)
	}

	d, removed, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if removed != 2 {
		t.Errorf("removed %d directories, want 2", removed)
	}
	for _, path := range []string{dead, filepath.Join(root, "worker-unlocked")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s was left behind", path)
		}
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Errorf("root holds %d entries, want only the new directory", len(entries))
	}
}

// crashedWorker starts a separate process that opens a scratch directory,
// writes a job file into it, and is then killed with SIGKILL, so nothing in
// it gets to clean up. It returns the directory the process left behind.
func crashedWorker(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperWorker$")
	cmd.Env = append(os.Environ(), "WORKDIR_HELPER_ROOT="+root)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 4096)
	n, err := out.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	path := string(buf[:n])
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()

	if _, err := os.Stat(filepath.Join(path, "job-1", "source.mp4")); err != nil {
		t.Fatalf("killed worker left no files to clean up: %v", err)
	}
	return path
}

// TestHelperWorker is the process crashedWorker starts. It does nothing in a
// normal test run.
func TestHelperWorker(t *testing.T) {
	root := os.Getenv("WORKDIR_HELPER_ROOT")
	if root == "" {
		t.Skip("helper process for TestOpenRemovesDirectoriesOfDeadWorkers")
	}
	d, _, err := Open(root)
	if err != nil {
		os.Exit(2)
	}
	if err := os.MkdirAll(filepath.Join(d.Path, "job-1"), 0o700); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(filepath.Join(d.Path, "job-1", "source.mp4"), []byte("x"), 0o600); err != nil {
		os.Exit(2)
	}
	os.Stdout.WriteString(d.Path)
	time.Sleep(time.Minute) // killed long before this ends
}
