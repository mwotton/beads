package main

import (
	"strings"
	"testing"
)

// sk-1pc: `bd ready`'s empty-queue hint blamed blocking dependencies
// unconditionally, but a bead held for an operator decision is unblocked and
// still absent — and absent from `bd blocked` too, so that message sent the
// reader somewhere the bead could not be found.
//
// The direct and proxied listing routes render this hint through one function
// precisely because they had already diverged once: the direct route learned to
// count held beads while daemon mode still blamed blockers. Testing the shared
// renderer is what makes that divergence impossible rather than merely fixed.
func TestPrintEmptyReadyQueue(t *testing.T) {
	tests := []struct {
		name          string
		hasOpenIssues bool
		held          int
		wantContains  []string
		wantAbsent    []string
		wantCounted   bool
	}{
		{
			name:          "no open issues at all",
			hasOpenIssues: false,
			wantContains:  []string{"No open issues"},
			wantAbsent:    []string{"blocking dependencies", "human"},
			// Counting costs a query and cannot change this answer.
			wantCounted: false,
		},
		{
			name:          "open issues, none held: blockers are the honest explanation",
			hasOpenIssues: true,
			held:          0,
			wantContains:  []string{"No ready work found", "blocking dependencies"},
			wantAbsent:    []string{"human decision"},
			wantCounted:   true,
		},
		{
			name:          "one held bead is named, and blockers are not blamed",
			hasOpenIssues: true,
			held:          1,
			wantContains:  []string{"1 issue is held for a human decision", "bd human list"},
			// The branch knows the held count and nothing else: under a
			// label or type filter the others may simply not match.
			wantAbsent:  []string{"blocking dependencies"},
			wantCounted: true,
		},
		{
			name:          "plural agreement",
			hasOpenIssues: true,
			held:          3,
			wantContains:  []string{"3 issues are held for a human decision"},
			wantAbsent:    []string{"blocking dependencies", "issue is held"},
			wantCounted:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counted := false
			out := captureStdout(t, func() error {
				printEmptyReadyQueue(tt.hasOpenIssues, func() int {
					counted = true
					return tt.held
				})
				return nil
			})
			for _, want := range tt.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(out, absent) {
					t.Errorf("output must not contain %q:\n%s", absent, out)
				}
			}
			if counted != tt.wantCounted {
				t.Errorf("held-count query ran = %v, want %v", counted, tt.wantCounted)
			}
		})
	}
}
