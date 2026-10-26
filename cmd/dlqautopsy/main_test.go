package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
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

// emptySQS is a drained queue: every receive returns no messages.
type emptySQS struct{}

func (emptySQS) ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	return &sqs.ReceiveMessageOutput{}, nil
}

func (emptySQS) DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	return &sqs.DeleteMessageOutput{}, nil
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// inspect used to print "Ingestion complete." twice: once from the collector
// (with the total) and once from the command.
func TestInspectPrintsIngestionCompleteOnce(t *testing.T) {
	sqsClient = emptySQS{}
	defer func() { sqsClient = nil }()

	out := captureStdout(t, func() {
		if _, err := executeCommand("inspect", "--name", "once", "--queue-url", "http://127.0.0.1:4566/000000000000/q", "--db", ":memory:"); err != nil {
			t.Errorf("inspect: %v", err)
		}
	})
	if n := strings.Count(out, "Ingestion complete"); n != 1 {
		t.Errorf("\"Ingestion complete\" printed %d times, want 1. Output:\n%s", n, out)
	}
	if !strings.Contains(out, "Ingestion complete. Total ingested: 0") {
		t.Errorf("missing total line. Output:\n%s", out)
	}
}

// countingEmptySQS counts receives on a drained queue.
type countingEmptySQS struct {
	emptySQS
	receives int
}

func (c *countingEmptySQS) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	c.receives++
	return c.emptySQS.ReceiveMessage(ctx, in, opts...)
}

func TestInspectEmptyReceivesFlag(t *testing.T) {
	defer func() { emptyReceives = 1; sqsClient = nil }()

	fake := &countingEmptySQS{}
	sqsClient = fake
	out := captureStdout(t, func() {
		if _, err := executeCommand("inspect", "--name", "er", "--queue-url", "q", "--db", ":memory:", "--empty-receives", "3"); err != nil {
			t.Errorf("inspect: %v", err)
		}
	})
	if fake.receives != 3 {
		t.Errorf("made %d receives, want 3", fake.receives)
	}
	if !strings.Contains(out, "No messages in 3 consecutive receives. Stopping ingestion.") {
		t.Errorf("unexpected output:\n%s", out)
	}

	if _, err := executeCommand("inspect", "--name", "er", "--queue-url", "q", "--db", ":memory:", "--empty-receives", "0"); err == nil {
		t.Error("expected an error for --empty-receives 0")
	}
}
