package main

import (
	"testing"
)

func TestPlanViewTimingGap_ErrorSwitchesToLogView(t *testing.T) {
	m := Model{
		streamChan: make(chan StreamMsg, 10),
		showLogs:   true, // Start in LOG view (default)
	}

	// 1. Receive a resource -> should switch to PLAN view
	res := ResourceChange{Address: "aws_instance.foo", Action: "create"}
	msg1 := StreamMsg{Resource: &res}

	// We need to call Update directly as the goroutine loop is not running in this unit test setup
	// exactly like the app would.

	updatedModel, _ := m.Update(msg1)
	m = updatedModel.(Model)

	if m.showLogs {
		t.Error("Expected to switch to PLAN view (showLogs=false) after receiving a resource")
	}

	// 2. Receive an ERROR diagnostic -> should switch back to LOG view IMMEDIATELY
	// This covers the "timing gap" before exit code arrives.
	diag := Diagnostic{Severity: "error", Summary: "Something went wrong"}
	msg2 := StreamMsg{Diagnostic: &diag}

	updatedModel, _ = m.Update(msg2)
	m = updatedModel.(Model)

	if !m.showLogs {
		t.Error("Expected to switch to LOG view (showLogs=true) immediately after receiving an error diagnostic")
	}

	// 3. Receive ANOTHER resource AFTER error -> should STAY in LOG view
	res2 := ResourceChange{Address: "aws_s3_bucket.data", Action: "create"}
	msg3 := StreamMsg{Resource: &res2}

	updatedModel, _ = m.Update(msg3)
	m = updatedModel.(Model)

	if !m.showLogs {
		t.Error("Expected to STAY in LOG view after receiving a resource when error diagnostics exist")
	}
}

func TestPlanViewTimingGap_WarningDoesNotSwitchToLog(t *testing.T) {
	m := Model{
		streamChan: make(chan StreamMsg, 10),
		showLogs:   false, // Start in PLAN view
	}

	// Warning diagnostic should NOT auto-switch to LOG view
	diag := Diagnostic{Severity: "warning", Summary: "Deprecated resource"}
	msg := StreamMsg{Diagnostic: &diag}

	updatedModel, _ := m.Update(msg)
	m = updatedModel.(Model)

	if m.showLogs {
		t.Error("Warning diagnostic should NOT auto-switch to LOG view")
	}
}
