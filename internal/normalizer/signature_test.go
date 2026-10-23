package normalizer

import (
	"encoding/hex"
	"testing"
)

// Two errors that differ only in volatile tokens must hash to the same
// signature; errors that differ in meaning must not.
func TestSignatureStability(t *testing.T) {
	n := &Normalizer{}

	tests := []struct {
		name     string
		a, b     string
		wantSame bool
	}{
		{
			name:     "different UUIDs",
			a:        "order a1b2c3d4-e5f6-7890-abcd-ef1234567890 not found",
			b:        "order 00000000-1111-2222-3333-444444444444 not found",
			wantSame: true,
		},
		{
			name:     "different IPs and ports",
			a:        "dial tcp 10.0.14.72:6379: connect: connection refused",
			b:        "dial tcp 10.0.14.99:6380: connect: connection refused",
			wantSame: true,
		},
		{
			name:     "different memory addresses",
			a:        "main.(*Handler).Send(0xc0001a2000, 0x0)",
			b:        "main.(*Handler).Send(0xc000198000, 0x1f)",
			wantSame: true,
		},
		{
			name:     "different Go line numbers",
			a:        "/app/handler/notification.go:87 +0x1a4",
			b:        "/app/handler/notification.go:91 +0x2b8",
			wantSame: true,
		},
		{
			name:     "timestamps with different fractional seconds and zones",
			a:        "token expired at 2026-02-24T09:45:00.123Z",
			b:        "token expired at 2026-03-01T11:02:13.987654+03:00",
			wantSame: true,
		},
		{
			name:     "surrounding whitespace",
			a:        "  context deadline exceeded\n",
			b:        "context deadline exceeded",
			wantSame: true,
		},
		{
			name:     "different error text",
			a:        "connection refused",
			b:        "connection reset by peer",
			wantSame: false,
		},
		{
			// Plain numbers are not normalized: two deadlocks with different
			// process ids get different signatures (same class, though).
			name:     "plain numeric ids are kept",
			a:        "deadlock detected: process 12847",
			b:        "deadlock detected: process 12849",
			wantSame: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, sigA := n.normalize(tt.a)
			_, sigB := n.normalize(tt.b)
			if (sigA == sigB) != tt.wantSame {
				t.Errorf("same signature = %v, want %v\n a=%q -> %s\n b=%q -> %s", sigA == sigB, tt.wantSame, tt.a, sigA, tt.b, sigB)
			}
		})
	}
}

func TestSignatureIsSHA256Hex(t *testing.T) {
	n := &Normalizer{}
	_, sig := n.normalize("") // sha256 of the empty string
	if sig != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("unexpected signature for empty input: %s", sig)
	}
	_, sig = n.normalize("context deadline exceeded")
	if len(sig) != 64 {
		t.Fatalf("signature length = %d, want 64", len(sig))
	}
	if _, err := hex.DecodeString(sig); err != nil {
		t.Fatalf("signature is not hex: %v", err)
	}
}

// Parser edge cases for message bodies.
func TestExtractErrorAndShape_EdgeCases(t *testing.T) {
	n := &Normalizer{}

	tests := []struct {
		name      string
		body      string
		wantError string
		wantShape string
	}{
		{
			name:      "error wins over message (priority order)",
			body:      `{"message":"user facing text","error":"real cause"}`,
			wantError: "real cause",
			wantShape: "error,message",
		},
		{
			name:      "empty error string falls through to the next field",
			body:      `{"error":"","reason":"quota exceeded"}`,
			wantError: "quota exceeded",
			wantShape: "error,reason",
		},
		{
			name:      "non-string, non-object error value is skipped",
			body:      `{"error":true,"cause":"upstream closed"}`,
			wantError: "upstream closed",
			wantShape: "cause,error",
		},
		{
			name:      "arrays are leaf keys in the shape",
			body:      `{"error":"x","payload":{"items":[{"sku":"a"}],"id":1}}`,
			wantError: "x",
			wantShape: "error,payload.id,payload.items",
		},
		{
			name:      "deep nesting",
			body:      `{"a":{"b":{"c":{"error":"not top level"}}}}`,
			wantError: `{"a":{"b":{"c":{"error":"not top level"}}}}`,
			wantShape: "a.b.c.error",
		},
		{
			name:      "top-level JSON array is not an object",
			body:      `[{"error":"x"}]`,
			wantError: `[{"error":"x"}]`,
			wantShape: "",
		},
		{
			name:      "whitespace around a plain-text body is trimmed",
			body:      "\n  unexpected EOF  \n",
			wantError: "unexpected EOF",
			wantShape: "",
		},
		{
			name:      "empty object",
			body:      `{}`,
			wantError: `{}`,
			wantShape: "",
		},
		{
			name:      "Exception field (Java style)",
			body:      `{"Exception":"java.net.SocketTimeoutException: Read timed out"}`,
			wantError: "java.net.SocketTimeoutException: Read timed out",
			wantShape: "Exception",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotErr, gotShape := n.extractErrorAndShape(tt.body)
			if gotErr != tt.wantError {
				t.Errorf("error = %q, want %q", gotErr, tt.wantError)
			}
			if gotShape != tt.wantShape {
				t.Errorf("shape = %q, want %q", gotShape, tt.wantShape)
			}
		})
	}
}
