package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPadRight(t *testing.T) {
	cases := []struct {
		in     string
		width  int
		expect string
	}{
		{"abc", 5, "abc  "},
		{"abc", 3, "abc"},
		{"", 2, "  "},
	}
	for _, tc := range cases {
		got := padRight(tc.in, tc.width)
		if got != tc.expect {
			t.Fatalf("padRight(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.expect)
		}
	}
}

func TestUnicodeToLetter(t *testing.T) {
	if got := unicodeToLetter('c'); got != 'C' {
		t.Fatalf("unicodeToLetter lowercase => %c, want C", got)
	}
	if got := unicodeToLetter('Z'); got != 'Z' {
		t.Fatalf("unicodeToLetter preserves => %c, want Z", got)
	}
}

func TestPrintSummaryPlacesGradeLast(t *testing.T) {
	questions := []question{
		{Domain: 1, Prompt: "Q1", Answer: "A", Options: map[string]string{"A": "Yes", "B": "No"}},
		{Domain: 1, Prompt: "Q2", Answer: "B", Options: map[string]string{"A": "Yes", "B": "No"}},
		{Domain: 1, Prompt: "Q3", Answer: "C", Options: map[string]string{"C": "Maybe", "D": "No"}},
	}
	results := []result{
		{UserAnswer: "A", Correct: true},
		{UserAnswer: "A", Correct: false},
		{UserAnswer: "C", Correct: true},
	}

	output := captureOutput(t, func() {
		printSummary(len(results), questions, results)
	})

	lines := strings.Split(output, "\n")
	var last string
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		last = strings.TrimSpace(lines[i])
		break
	}
	if !strings.HasPrefix(last, "You answered") {
		t.Fatalf("expected grade line last, got %q", last)
	}
}

func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	w.Close()
	out := <-done
	r.Close()
	return out
}
