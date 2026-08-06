//go:build cgo

package embeddeddolt

import (
	"testing"

	"github.com/dolthub/dolt/go/store/nbs"
)

// The Dolt library defaults TableIndexGCFinalizerWithStackTrace to true, which
// makes every table index open capture a runtime stack trace. That measured
// ~20% of the user CPU of a single `bd show` against a production-shaped
// store. This package's init must clear it, exactly as the dolt CLI does.
func TestTableIndexStackTracesDisabledByDefault(t *testing.T) {
	if nbs.TableIndexGCFinalizerWithStackTrace {
		t.Fatal("nbs.TableIndexGCFinalizerWithStackTrace is true: the embedded-Dolt hot path " +
			"is paying a debug.Stack() per table index open")
	}
}

func TestTableIndexStackTracesFollowVerboseAssertEnv(t *testing.T) {
	if tableIndexStackTracesEnabled("") {
		t.Error("stack traces should be off when DOLT_VERBOSE_ASSERT_TABLE_FILES_CLOSED is unset")
	}
	if !tableIndexStackTracesEnabled("1") {
		t.Error("stack traces should be on when DOLT_VERBOSE_ASSERT_TABLE_FILES_CLOSED is set, " +
			"so the unclosed-table-file assertion stays debuggable")
	}
}
