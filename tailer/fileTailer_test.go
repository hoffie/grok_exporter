package tailer

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type logrotateOption int
type logrotateMoveOption int
type loggerOption int

const ( // see 'man logrotate'
	copy         logrotateOption = iota // Don’t change the original logfile at all.
	copytruncate                        // Truncate the original log file in place instead of removing it.
	nocreate                            // Don't create a new logfile after rotation.
	create                              // Create a new empty logfile immediately after rotation.
)

const (
	mv logrotateMoveOption = iota // Move the old logfile to the backup.
	cp                            // Copy the old logfile to the backup, then remove it.
	rm                            // Delete the old logfile without keeping a backup.
)

const (
	closeFileAfterEachLine loggerOption = iota
	keepOpen                            // Not implemented yet. Will probably not work on Windows, because open files cannot be removed by logrotate.
)

func (opt logrotateOption) String() string {
	switch {
	case opt == copy:
		return "copy"
	case opt == copytruncate:
		return "copytruncate"
	case opt == nocreate:
		return "nocreate"
	case opt == create:
		return "create"
	default:
		return "unknown"
	}
}

func (opt logrotateMoveOption) String() string {
	switch {
	case opt == mv:
		return "mv"
	case opt == cp:
		return "cp"
	case opt == rm:
		return "rm"
	default:
		return "unknown"
	}
}

func (opt loggerOption) String() string {
	switch {
	case opt == closeFileAfterEachLine:
		return "closeFileAfterEachLine"
	case opt == keepOpen:
		return "keepOpen"
	default:
		return "unknown"
	}
}
func TestCloseFileAfterEachLine(t *testing.T) {
	for _, mvOpt := range []logrotateMoveOption{mv, cp, rm} {
		runTest(t, create, mvOpt, closeFileAfterEachLine)
		runTest(t, nocreate, mvOpt, closeFileAfterEachLine)
	}
	runTest(t, copy, cp, closeFileAfterEachLine)
	runTest(t, copytruncate, cp, closeFileAfterEachLine)
}

func runTest(t *testing.T, logrotateOpt logrotateOption, logrotateMoveOpt logrotateMoveOption, loggerOpt loggerOption) {
	fmt.Printf("Running test with logrotate option '%v', move option '%v', and logger option '%v'.\n", logrotateOpt, logrotateMoveOpt, loggerOpt)
	tmpDir := mkTmpDirOrFail(t)
	defer cleanUp(t, tmpDir)
	logfile := mkTmpFileOrFail(t, tmpDir)
	logger := newLogger(t, logfile, loggerOpt)

	logger.log(t, "test line 1")
	logger.log(t, "test line 2")

	tail := RunFileTailer2(logfile, true)

	// We don't expect errors. However, start a goroutine listening on
	// the tailer's errorChannel in case something goes wrong.
	stopFailOnError := failOnError(t, tail.ErrorChan())
	defer func() {
		stopFailOnError <- true
		close(stopFailOnError)
	}()

	// The first two lines are received without any fsnotify event,
	// because they were written before the watcher was started.
	expect(tail.LineChan(), "test line 1", 1*time.Second, t)
	expect(tail.LineChan(), "test line 2", 1*time.Second, t)

	// Append a line and see if the event is processed.
	logger.log(t, "test line 3")
	expect(tail.LineChan(), "test line 3", 1*time.Second, t)

	rotate(t, logfile, logrotateOpt, logrotateMoveOpt)

	// Log two more lines and see if they are received.
	logger.log(t, "line 4")
	logger.log(t, "line 5")
	expect(tail.LineChan(), "line 4", 1*time.Second, t)
	expect(tail.LineChan(), "line 5", 1*time.Second, t)
	tail.Close()
}

// Consume the tailer's error channel in case something goes wrong.
func failOnError(t *testing.T, errorChan chan error) chan bool {
	done := make(chan bool)
	go func() {
		for {
			select {
			case err := <-errorChan:
				// err == nil means the tailer is shutting down, we will receive
				// on done channel in that case.
				if err != nil {
					t.Fatalf("Tailer failed: %v", err.Error())
				}
			case <-done:
				return
			}
		}
	}()
	return done
}

func newLogger(t *testing.T, logfile string, opt loggerOption) logger {
	switch {
	case opt == closeFileAfterEachLine:
		return &closeFileAfterEachLineLogger{
			path: logfile,
		}
	case opt == keepOpen:
		t.Fatalf("keepOpen logger not implemented yet.")
		return nil
	default:
		t.Fatalf("Unsupported logger option.")
		return nil
	}
}

type logger interface {
	log(t *testing.T, msg string)
}

type closeFileAfterEachLineLogger struct {
	path string
}

func (l *closeFileAfterEachLineLogger) log(t *testing.T, msg string) {
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("%v: Failed to open file for writing: %v", l.path, err.Error())
	}
	defer f.Close()
	_, err = f.WriteString(fmt.Sprintf("%v\n", msg))
	if err != nil {
		t.Fatalf("%v: Failed to write to file: %v", l.path, err.Error())
	}
	err = f.Close()
	if err != nil {
		t.Fatalf("%v: Failed to close file: %v", l.path, err.Error())
	}
}

func mkTmpDirOrFail(t *testing.T) string {
	testdir, err := ioutil.TempDir("", "grok_exporter")
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err.Error())
	}
	return testdir
}

func cleanUp(t *testing.T, dir string) {
	err := os.RemoveAll(dir)
	if err != nil {
		t.Errorf("%v: Failed to remove the test directory after running the tests: %v", dir, err.Error())
	}
}

func mkTmpFileOrFail(t *testing.T, dir string) string {
	if len(ls(t, dir)) != 0 {
		t.Fatalf("%v: Directory not empty.", dir)
	}
	logfile, err := ioutil.TempFile(dir, "grok_exporter_test_log")
	if err != nil {
		t.Fatalf("Cannot create temp file: %v", err.Error())
	}
	err = logfile.Close()
	if err != nil {
		t.Fatalf("Failed to close temp file: %v", err.Error())
	}
	if len(ls(t, dir)) != 1 {
		t.Fatalf("%v: Expected exactly one file in directory.", dir)
	}
	return logfile.Name()
}

func containsFile(files []os.FileInfo, filename string) bool {
	for _, f := range files {
		if filepath.Base(f.Name()) == filepath.Base(filename) {
			return true
		}
	}
	return false
}

func ls(t *testing.T, path string) []os.FileInfo {
	result, err := ioutil.ReadDir(path)
	if err != nil {
		t.Fatalf("%v: Failed to list directory: %v", path, err.Error())
	}
	return result
}

func rotate(t *testing.T, logfile string, opt logrotateOption, mvOpt logrotateMoveOption) {
	dir := filepath.Dir(logfile)
	filename := filepath.Base(logfile)
	filesBefore := ls(t, dir)
	if !containsFile(filesBefore, filename) {
		t.Fatalf("%v does not contain %v before logrotate.", dir, filename)
	}
	switch {
	case opt == nocreate:
		moveOrFail(t, mvOpt, logfile)
	case opt == create:
		moveOrFail(t, mvOpt, logfile)
		createOrFail(t, logfile)
	case opt == copytruncate:
		if mvOpt != cp {
			t.Fatalf("Rotating with '%v' does not make sense when moving the logfile with '%v'", opt, mvOpt)
		}
		cpOrFail(t, logfile, fmt.Sprintf("%v.1", logfile))
		truncateOrFail(t, logfile)
	case opt == copy:
		if mvOpt != cp {
			t.Fatalf("Rotating with '%v' does not make sense when moving the logfile with '%v'", opt, mvOpt)
		}
		cpOrFail(t, logfile, fmt.Sprintf("%v.1", logfile))
	default:
		t.Fatalf("Unknown logrotate option.")
	}
}

func moveOrFail(t *testing.T, mvOpt logrotateMoveOption, logfile string) {
	dir := filepath.Dir(logfile)
	filename := filepath.Base(logfile)
	switch {
	case mvOpt == mv:
		mvOrFail(t, logfile, fmt.Sprintf("%v.1", logfile))
	case mvOpt == cp:
		cpOrFail(t, logfile, fmt.Sprintf("%v.1", logfile))
		rmOrFail(t, logfile)
	case mvOpt == rm:
		rmOrFail(t, logfile)
	}
	filesAfterRename := ls(t, dir)
	if containsFile(filesAfterRename, filename) {
		t.Fatalf("%v still contains file %v after mv.", dir, filename)
	}
}

func mvOrFail(t *testing.T, fromPath string, toPath string) {
	err := os.Rename(fromPath, toPath)
	if err != nil {
		t.Fatalf("%v: Failed to mv file: %v", fromPath, err.Error())
	}
}

func cpOrFail(t *testing.T, fromPath string, toPath string) {
	data, err := ioutil.ReadFile(fromPath)
	if err != nil {
		t.Fatalf("%v: Copy failed, cannot read file: %v", fromPath, err.Error())
	}
	err = ioutil.WriteFile(toPath, data, 0644)
	if err != nil {
		t.Fatalf("%v: Copy failed, cannot write file: %v", toPath, err.Error())
	}
}

func rmOrFail(t *testing.T, path string) {
	err := os.Remove(path)
	if err != nil {
		t.Fatalf("%v: Remove failed: %v", path, err.Error())
	}
}

func createOrFail(t *testing.T, logfile string) {
	dir := filepath.Dir(logfile)
	filename := filepath.Base(logfile)
	filesBeforeCreate := ls(t, dir)
	if containsFile(filesBeforeCreate, filename) {
		t.Fatalf("%v contains file %v before create.", dir, filename)
	}
	f, err := os.Create(logfile)
	if err != nil {
		t.Fatalf("Failed to re-create %v while simulating logrotate: %v", filename, err.Error())
	}
	err = f.Close()
	if err != nil {
		t.Fatalf("%v: Failed to close file: %v", filename, err.Error())
	}
	filesAfterCreate := ls(t, dir)
	if !containsFile(filesAfterCreate, filename) {
		t.Fatalf("%v does not contain %v after create.", dir, filename)
	}
}

func truncateOrFail(t *testing.T, logfile string) {
	err := os.Truncate(logfile, 0)
	if err != nil {
		t.Fatalf("%v: Error truncating the file: %v", logfile, err.Error())
	}
}

func expect(c chan string, line string, timeout time.Duration, t *testing.T) {
	timeoutChan := make(chan bool, 1)
	go func() {
		time.Sleep(timeout)
		timeoutChan <- true
	}()
	select {
	case result := <-c:
		if result != line {
			t.Errorf("Expected '%v', but got '%v'.", line, result)
		} else {
			fmt.Printf("Read expected line '%v'\n", line)
		}
	case <-timeoutChan:
		t.Errorf("Timeout while waiting for line '%v'", line)
	}
}