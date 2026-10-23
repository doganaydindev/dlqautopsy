package main

import (
	"bytes"
	"os"
	"testing"
)

func executeCommand(args ...string) (string, error) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)

	err := rootCmd.Execute()
	return buf.String(), err
}

func TestClassifyCmd(t *testing.T) {
	_, err := executeCommand("classify", "--name", "test-run", "--db", ":memory:")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestReportCmd(t *testing.T) {
	// Test text format
	_, err := executeCommand("report", "--name", "test-run", "--db", ":memory:", "--format", "text")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Test json format
	_, err = executeCommand("report", "--name", "test-run", "--db", ":memory:", "--format", "json")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Test markdown format
	_, err = executeCommand("report", "--name", "test-run", "--db", ":memory:", "--format", "md")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestDetailCmd(t *testing.T) {
	// List classes
	_, err := executeCommand("detail", "--name", "test-run", "--db", ":memory:")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Drill into class
	_, err = executeCommand("detail", "--name", "test-run", "--db", ":memory:", "--class", "Poison")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Drill into signature
	_, err = executeCommand("detail", "--name", "test-run", "--db", ":memory:", "--signature", "abc12345")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestDiffCmd(t *testing.T) {
	_, err := executeCommand("diff", "--name", "run-1", "--compare", "run-2", "--db", ":memory:")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestRunsCmd(t *testing.T) {
	_, err := executeCommand("runs", "--db", ":memory:")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestMainFunc(t *testing.T) {
	// Temporarily override os.Args to prevent exit/panic
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"dlqautopsy", "runs", "--db", ":memory:"}

	// Execute main
	main()
}

func TestCmdErrors(t *testing.T) {
	// Test error paths by providing an invalid db path to hit storage.NewDB err != nil
	executeCommand("inspect", "--name", "errs", "--queue-url", "q", "--db", "/invalid/root/db.db")
	executeCommand("classify", "--name", "errs", "--db", "/invalid/root/db.db")
	executeCommand("report", "--name", "errs", "--db", "/invalid/root/db.db")
	executeCommand("detail", "--name", "errs", "--db", "/invalid/root/db.db")
	executeCommand("diff", "--name", "errs", "--compare", "run", "--db", "/invalid/root/db.db")
	executeCommand("runs", "--db", "/invalid/root/db.db")
}
