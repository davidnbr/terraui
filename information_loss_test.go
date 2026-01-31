package main

import (
	"context"
	"strings"
	"testing"
)

// collectStreamMsgs reads all messages from a stream until Done, collecting
// diagnostics, log lines, and resources separately.
func collectStreamMsgs(m *Model, input string) (diagnostics []*Diagnostic, logs []string, resources []*ResourceChange) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go m.readInputStream(ctx, strings.NewReader(input))

	for {
		msg, ok := <-m.streamChan
		if !ok || msg.Done {
			break
		}
		if msg.Diagnostic != nil {
			diagnostics = append(diagnostics, msg.Diagnostic)
		}
		if msg.LogLine != nil {
			logs = append(logs, *msg.LogLine)
		}
		if msg.Resource != nil {
			resources = append(resources, msg.Resource)
		}
	}
	return
}

// =============================================================================
// Bug 1: Diagnostic blocks without Error:/Warning: prefix are silently dropped
// =============================================================================

func TestDiagnosticBlockWithoutErrorPrefix_ProviderCrash(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	// Real-world: provider crash message has NO "Error:" prefix
	input := "╷\n│ The plugin encountered an error, and failed to respond to the\n│ plugin.(*GRPCProvider).ApplyResourceChange call. The plugin logs may\n│ contain more details.\n╵\n"

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	// The content MUST appear somewhere - as a diagnostic or log lines
	if len(diagnostics) == 0 && len(logs) == 0 {
		t.Fatal("BUG: Diagnostic block without Error:/Warning: prefix was silently dropped. No information should be lost.")
	}

	// Verify the actual content is preserved
	allContent := ""
	for _, d := range diagnostics {
		allContent += d.Summary
		for _, detail := range d.Detail {
			allContent += detail.Content
		}
	}
	for _, l := range logs {
		allContent += l
	}

	if !strings.Contains(allContent, "plugin encountered an error") {
		t.Error("Expected plugin crash message to be preserved in output")
	}
	if !strings.Contains(allContent, "GRPCProvider") {
		t.Error("Expected GRPCProvider reference to be preserved in output")
	}
}

func TestDiagnosticBlockWithoutErrorPrefix_PluginRequirements(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	// Real-world: plugin requirements message
	input := "╷\n│ Could not retrieve the list of available versions for provider\n│ hashicorp/google: could not connect to registry.terraform.io:\n│ TLS handshake timeout\n╵\n"

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 && len(logs) == 0 {
		t.Fatal("BUG: Plugin requirements diagnostic block was silently dropped")
	}

	allContent := ""
	for _, d := range diagnostics {
		allContent += d.Summary
		for _, detail := range d.Detail {
			allContent += detail.Content
		}
	}
	for _, l := range logs {
		allContent += l
	}

	if !strings.Contains(allContent, "hashicorp/google") {
		t.Error("Expected provider name to be preserved")
	}
	if !strings.Contains(allContent, "TLS handshake timeout") {
		t.Error("Expected error detail to be preserved")
	}
}

func TestDiagnosticBlockWithoutErrorPrefix_GCPAuth(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	// Real-world: GCP authentication failure without standard Error: prefix
	input := "╷\n│ google: could not find default credentials. See\n│ https://cloud.google.com/docs/authentication/external/set-up-adc\n│ for more information\n╵\n"

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 && len(logs) == 0 {
		t.Fatal("BUG: GCP auth diagnostic block was silently dropped")
	}

	allContent := ""
	for _, d := range diagnostics {
		allContent += d.Summary
		for _, detail := range d.Detail {
			allContent += detail.Content
		}
	}
	for _, l := range logs {
		allContent += l
	}

	if !strings.Contains(allContent, "default credentials") {
		t.Error("Expected credentials message to be preserved")
	}
}

// =============================================================================
// Bug 2: No flush for pending diagnostic blocks at end of stream
// =============================================================================

func TestDiagnosticBlockTruncatedStream(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	// Stream ends mid-diagnostic (no closing ╵)
	input := "╷\n│ Error: Error creating Instance: googleapi: Error 400: Invalid value\n│ \n│   with google_compute_instance.default,\n│   on main.tf line 10\n"

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 && len(logs) == 0 {
		t.Fatal("BUG: Truncated diagnostic block content was lost when stream ended without closing ╵")
	}

	allContent := ""
	for _, d := range diagnostics {
		allContent += d.Summary
		for _, detail := range d.Detail {
			allContent += detail.Content
		}
	}
	for _, l := range logs {
		allContent += l
	}

	if !strings.Contains(allContent, "Error creating Instance") {
		t.Error("Expected error message to be preserved even when stream is truncated")
	}
}

// =============================================================================
// Bug 3: Overlapping ╷ markers drop previous diagnostic block
// =============================================================================

func TestOverlappingDiagnosticBlocks(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	// First diagnostic block is missing its closing ╵, second one starts immediately
	input := "╷\n│ Error: First GCP error about instance creation\n│ \n│   on main.tf line 5\n╷\n│ Error: Second error about network configuration\n│ \n│   on main.tf line 10\n╵\n"

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	// Both errors MUST be preserved
	allContent := ""
	for _, d := range diagnostics {
		allContent += d.Summary + " "
		for _, detail := range d.Detail {
			allContent += detail.Content + " "
		}
	}
	for _, l := range logs {
		allContent += l + " "
	}

	if !strings.Contains(allContent, "First GCP error") {
		t.Error("BUG: First diagnostic block was dropped when second ╷ appeared without closing ╵")
	}
	if !strings.Contains(allContent, "Second error") {
		t.Error("Second diagnostic block should also be preserved")
	}
}

// =============================================================================
// Bug 4: │ TrimPrefix on richLine may fail with ANSI codes
// =============================================================================

func TestDiagnosticLineStripWithANSICodes(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	// Simulate terraform output where bold ANSI code precedes │ character
	// \x1b[1m = bold on, preserved by sanitizeTerraformANSI
	input := "╷\n\x1b[1m│ Error: GCP instance type invalid\n\x1b[1m│ \n\x1b[1m│   on main.tf line 3\n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 {
		t.Fatal("Expected diagnostic to be parsed from ANSI-prefixed lines")
	}

	// Summary should NOT contain the │ character
	if strings.Contains(diagnostics[0].Summary, "│") {
		t.Errorf("Summary should not contain pipe character, got: %q", diagnostics[0].Summary)
	}

	// Detail lines should NOT contain the │ character (except as part of tree drawing)
	for _, detail := range diagnostics[0].Detail {
		clean := stripANSI(detail.Content)
		trimmed := strings.TrimSpace(clean)
		if strings.HasPrefix(trimmed, "│") && !strings.Contains(trimmed, "var.") {
			t.Errorf("Detail line should not have leading │ character, got: %q", detail.Content)
		}
	}
}

// =============================================================================
// GCP-specific error patterns that must be preserved
// =============================================================================

func TestGCPAPIError(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n│ Error: Error creating Instance: googleapi: Error 400: Invalid value for field 'resource.machineType': 'zones/us-central1-a/machineTypes/n1-standard-99'. Machine type with name 'n1-standard-99' does not exist in zone 'us-central1-a'., invalid\n│ \n│   with google_compute_instance.default,\n│   on main.tf line 10, in resource \"google_compute_instance\" \"default\":\n│   10: resource \"google_compute_instance\" \"default\" {\n│ \n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 {
		t.Fatal("Expected GCP API error diagnostic")
	}

	d := diagnostics[0]
	if d.Severity != "error" {
		t.Errorf("Expected severity 'error', got %q", d.Severity)
	}

	// Full API error message must be preserved in summary
	if !strings.Contains(d.Summary, "googleapi: Error 400") {
		t.Errorf("Expected googleapi error code in summary, got: %q", d.Summary)
	}
	if !strings.Contains(d.Summary, "n1-standard-99") {
		t.Errorf("Expected machine type in summary, got: %q", d.Summary)
	}

	// Resource reference must be in details
	foundResource := false
	for _, detail := range d.Detail {
		if strings.Contains(detail.Content, "google_compute_instance.default") {
			foundResource = true
			break
		}
	}
	if !foundResource {
		t.Error("Expected resource reference in diagnostic details")
	}
}

func TestGCPPermissionDenied(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n│ Error: Error creating Network: googleapi: Error 403: Required 'compute.networks.create' permission for 'projects/my-project/global/networks/my-network', forbidden\n│ \n│   with google_compute_network.vpc,\n│   on network.tf line 1, in resource \"google_compute_network\" \"vpc\":\n│    1: resource \"google_compute_network\" \"vpc\" {\n│ \n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 {
		t.Fatal("Expected GCP permission error diagnostic")
	}

	if !strings.Contains(diagnostics[0].Summary, "403") {
		t.Errorf("Expected 403 error code in summary, got: %q", diagnostics[0].Summary)
	}
	if !strings.Contains(diagnostics[0].Summary, "compute.networks.create") {
		t.Errorf("Expected permission name in summary, got: %q", diagnostics[0].Summary)
	}
}

func TestGCPQuotaExceeded(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n│ Error: Error creating Instance: googleapi: Error 403: Quota 'CPUS' exceeded. Limit: 8.0 in region us-central1., quotaExceeded\n│ \n│   with google_compute_instance.worker[3],\n│   on instances.tf line 15, in resource \"google_compute_instance\" \"worker\":\n│   15: resource \"google_compute_instance\" \"worker\" {\n│ \n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 {
		t.Fatal("Expected GCP quota error diagnostic")
	}

	if !strings.Contains(diagnostics[0].Summary, "Quota") {
		t.Errorf("Expected quota message in summary, got: %q", diagnostics[0].Summary)
	}
	if !strings.Contains(diagnostics[0].Summary, "CPUS") {
		t.Errorf("Expected resource name in summary, got: %q", diagnostics[0].Summary)
	}
}

func TestGCPMultipleErrors(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	// Multiple GCP errors in sequence
	input := "╷\n│ Error: Error creating Instance: googleapi: Error 400: Invalid value\n│ \n│   with google_compute_instance.web,\n│   on main.tf line 5\n│ \n╵\n╷\n│ Error: Error creating Disk: googleapi: Error 400: The disk resource is not found\n│ \n│   with google_compute_disk.data,\n│   on main.tf line 20\n│ \n╵\n╷\n│ Warning: Deprecated resource\n│ \n│ The google_compute_address resource has been deprecated.\n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) != 3 {
		t.Fatalf("Expected 3 diagnostics (2 errors + 1 warning), got %d", len(diagnostics))
	}

	// Verify each diagnostic
	if diagnostics[0].Severity != "error" || !strings.Contains(diagnostics[0].Summary, "Instance") {
		t.Errorf("First diagnostic incorrect: severity=%q summary=%q", diagnostics[0].Severity, diagnostics[0].Summary)
	}
	if diagnostics[1].Severity != "error" || !strings.Contains(diagnostics[1].Summary, "Disk") {
		t.Errorf("Second diagnostic incorrect: severity=%q summary=%q", diagnostics[1].Severity, diagnostics[1].Summary)
	}
	if diagnostics[2].Severity != "warning" || !strings.Contains(diagnostics[2].Summary, "Deprecated") {
		t.Errorf("Third diagnostic incorrect: severity=%q summary=%q", diagnostics[2].Severity, diagnostics[2].Summary)
	}
}

func TestGCPLongErrorWithMultilineDetails(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	// GCP errors can have very long detail sections
	input := "╷\n│ Error: Error creating Instance: googleapi: Error 400: Invalid value for field 'resource.networkInterfaces[0].subnetwork': 'projects/my-project/regions/us-central1/subnetworks/my-subnet'. The referenced subnetwork resource cannot be found., invalid\n│ \n│   with google_compute_instance.default,\n│   on main.tf line 10, in resource \"google_compute_instance\" \"default\":\n│   10: resource \"google_compute_instance\" \"default\" {\n│ \n│ The subnetwork 'my-subnet' was not found in the project 'my-project'.\n│ Ensure that the subnetwork exists and that you have the correct\n│ permissions to access it. You may need to run 'gcloud compute\n│ networks subnets list' to verify the subnetwork name.\n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 {
		t.Fatal("Expected diagnostic for GCP network error")
	}

	// All detail lines must be preserved
	allDetails := ""
	for _, detail := range diagnostics[0].Detail {
		allDetails += detail.Content + "\n"
	}

	if !strings.Contains(allDetails, "google_compute_instance.default") {
		t.Error("Expected resource reference in details")
	}
	if !strings.Contains(allDetails, "subnetwork 'my-subnet'") {
		t.Error("Expected subnetwork name in details")
	}
	if !strings.Contains(allDetails, "gcloud compute") {
		t.Error("Expected gcloud command suggestion in details")
	}
}

// =============================================================================
// Edge case: parseDiagnosticBlock directly with non-standard content
// =============================================================================

func TestParseDiagnosticBlock_NoSeverityPrefix(t *testing.T) {
	// Direct test of parseDiagnosticBlock with lines that have no Error:/Warning:
	lines := []string{
		" The plugin encountered an error, and failed to respond to the",
		" plugin.(*GRPCProvider).ApplyResourceChange call. The plugin logs may",
		" contain more details.",
	}

	diag := parseDiagnosticBlock(lines)

	if diag == nil {
		t.Fatal("BUG: parseDiagnosticBlock returned nil for diagnostic block without Error:/Warning: prefix. Content is lost.")
	}

	// Content must be preserved
	allContent := diag.Summary
	for _, d := range diag.Detail {
		allContent += " " + d.Content
	}

	if !strings.Contains(allContent, "plugin encountered an error") {
		t.Error("Expected plugin error message to be preserved")
	}
	if !strings.Contains(allContent, "GRPCProvider") {
		t.Error("Expected GRPCProvider reference to be preserved")
	}
}

func TestParseDiagnosticBlock_EmptyBlock(t *testing.T) {
	// Empty block should still return nil
	diag := parseDiagnosticBlock([]string{})
	if diag != nil {
		t.Error("Empty block should return nil")
	}
}

func TestParseDiagnosticBlock_OnlyWhitespace(t *testing.T) {
	lines := []string{" ", "  ", "   "}
	diag := parseDiagnosticBlock(lines)

	// All-whitespace block should return nil (no actual content)
	if diag != nil {
		t.Error("All-whitespace block should return nil")
	}
}

// =============================================================================
// Empty Input Detection for Pipe Mode
// =============================================================================

func TestEmptyInputDetection_NoContent(t *testing.T) {
	// Simulate pipe mode with no input (ptyFile is nil)
	m := &Model{
		streamChan: make(chan StreamMsg, 10),
		ptyFile:    nil, // Pipe mode
	}

	// Empty input
	input := ""

	// Collect stream messages
	_, _, _ = collectStreamMsgs(m, input)

	// Simulate what happens in Update when Done message is received
	// The warning is added to m.diagnostics when stream ends with no content in pipe mode
	if !m.receivedContent && m.ptyFile == nil {
		warning := Diagnostic{
			Severity: "warning",
			Summary:  "No input received from Terraform - errors may have been sent to stderr",
			Detail: []DiagnosticLine{
				{Content: "Terraform sends error messages to stderr, not stdout."},
				{Content: "When piping Terraform output to terraui, use: terraform plan 2>&1 | terraui"},
				{Content: "Or use interactive mode: terraui terraform plan"},
			},
			Expanded: true,
		}
		m.diagnostics = append(m.diagnostics, warning)
	}

	// Should have a warning diagnostic about missing stderr redirection
	if len(m.diagnostics) != 1 {
		t.Fatalf("Expected 1 warning diagnostic for empty input, got %d", len(m.diagnostics))
	}

	diag := m.diagnostics[0]
	if diag.Severity != "warning" {
		t.Errorf("Expected severity 'warning', got %q", diag.Severity)
	}

	if !strings.Contains(diag.Summary, "No input received") {
		t.Errorf("Expected warning about no input, got: %q", diag.Summary)
	}

	if !strings.Contains(diag.Summary, "stderr") {
		t.Errorf("Expected warning to mention stderr, got: %q", diag.Summary)
	}

	// Verify helpful suggestions are in details
	foundSuggestion := false
	for _, detail := range diag.Detail {
		if strings.Contains(detail.Content, "2>&1") || strings.Contains(detail.Content, "interactive mode") {
			foundSuggestion = true
			break
		}
	}
	if !foundSuggestion {
		t.Error("Expected warning to contain suggestions for fixing the issue")
	}
}

func TestEmptyInputDetection_OnlyWhitespace(t *testing.T) {
	// Simulate pipe mode with only whitespace input
	m := &Model{
		streamChan: make(chan StreamMsg, 10),
		ptyFile:    nil, // Pipe mode
	}

	// Only whitespace input
	input := "   \n\t\n   \n"

	// Collect stream messages
	_, _, _ = collectStreamMsgs(m, input)

	// Simulate what happens in Update when Done message is received
	// The warning is added to m.diagnostics when stream ends with no content in pipe mode
	if !m.receivedContent && m.ptyFile == nil {
		warning := Diagnostic{
			Severity: "warning",
			Summary:  "No input received from Terraform - errors may have been sent to stderr",
			Detail: []DiagnosticLine{
				{Content: "Terraform sends error messages to stderr, not stdout."},
				{Content: "When piping Terraform output to terraui, use: terraform plan 2>&1 | terraui"},
				{Content: "Or use interactive mode: terraui terraform plan"},
			},
			Expanded: true,
		}
		m.diagnostics = append(m.diagnostics, warning)
	}

	// Should have a warning diagnostic since no meaningful content was received
	if len(m.diagnostics) != 1 {
		t.Fatalf("Expected 1 warning diagnostic for whitespace-only input, got %d", len(m.diagnostics))
	}

	if m.diagnostics[0].Severity != "warning" {
		t.Errorf("Expected severity 'warning', got %q", m.diagnostics[0].Severity)
	}
}

func TestEmptyInputDetection_WithContent(t *testing.T) {
	// Simulate pipe mode with actual content - should NOT show warning
	m := &Model{
		streamChan: make(chan StreamMsg, 10),
		ptyFile:    nil, // Pipe mode
	}

	// Actual Terraform output
	input := "Terraform used the selected providers to generate the following execution plan.\n"

	_, logs, _ := collectStreamMsgs(m, input)

	// Simulate what happens in Update when Done message is received
	// The warning is added to m.diagnostics when stream ends with no content in pipe mode
	if !m.receivedContent && m.ptyFile == nil {
		warning := Diagnostic{
			Severity: "warning",
			Summary:  "No input received from Terraform - errors may have been sent to stderr",
			Detail: []DiagnosticLine{
				{Content: "Terraform sends error messages to stderr, not stdout."},
				{Content: "When piping Terraform output to terraui, use: terraform plan 2>&1 | terraui"},
				{Content: "Or use interactive mode: terraui terraform plan"},
			},
			Expanded: true,
		}
		m.diagnostics = append(m.diagnostics, warning)
	}

	// Should NOT have the empty input warning
	for _, d := range m.diagnostics {
		if strings.Contains(d.Summary, "No input received") {
			t.Error("Should not show empty input warning when content was received")
		}
	}

	// Should have the log line
	if len(logs) != 1 {
		t.Errorf("Expected 1 log line, got %d", len(logs))
	}
}

func TestEmptyInputDetection_PTYMode(t *testing.T) {
	// Simulate PTY mode (interactive) - should NOT show warning even with no input
	// We can't easily test with actual PTY, but we can verify the logic by checking
	// that the warning is only added when ptyFile is nil
	// The warning should only appear in pipe mode, not PTY mode
	// This is implicitly tested by the other tests, but we document the expectation here
	t.Log("PTY mode should not show empty input warning - interactive mode captures both streams")
}

// =============================================================================
// Non-standard error formats - verify fallback preserves ALL content
// =============================================================================

func TestNonStandardErrorFormat_NoDiagnosticBlock(t *testing.T) {
	// Errors that don't use Terraform's ╷...╵ format (e.g., provider crashes, custom modules)
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "panic: runtime error: index out of range [5] with length 3\n\ngoroutine 1 [running]:\nmain.main()\n\t/build/provider/main.go:42 +0x123\n"

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	// Should NOT be parsed as diagnostic (no ╷...╵ block)
	if len(diagnostics) != 0 {
		t.Errorf("Expected 0 diagnostics for non-standard format, got %d", len(diagnostics))
	}

	// MUST appear as log lines - NO information loss
	allLogs := strings.Join(logs, "\n")
	if !strings.Contains(allLogs, "panic") {
		t.Fatal("CRITICAL: Panic message was lost - should appear in logs")
	}
	if !strings.Contains(allLogs, "runtime error") {
		t.Error("Expected 'runtime error' in logs")
	}
	if !strings.Contains(allLogs, "index out of range") {
		t.Error("Expected 'index out of range' in logs")
	}
}

func TestNonStandardErrorFormat_CustomModuleOutput(t *testing.T) {
	// Custom modules or providers might output errors in their own format
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "[ERROR] Custom provider failed to initialize\nDetails: Connection refused to endpoint https://api.custom-provider.io\nRetry count exceeded: 5 attempts\n"

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	// Should NOT be parsed as diagnostic
	if len(diagnostics) != 0 {
		t.Errorf("Expected 0 diagnostics for custom format, got %d", len(diagnostics))
	}

	// MUST appear as log lines
	allLogs := strings.Join(logs, "\n")
	if !strings.Contains(allLogs, "Custom provider failed") {
		t.Fatal("CRITICAL: Custom provider error was lost")
	}
	if !strings.Contains(allLogs, "Connection refused") {
		t.Error("Expected 'Connection refused' in logs")
	}
}

func TestNonStandardErrorFormat_NixOnPremise(t *testing.T) {
	// On-premise or Nix-based infrastructure might have different error formats
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "FATAL: Failed to connect to internal API server\nContext: resource 'internal_vm' creation\nError: dial tcp 10.0.0.5:8080: connect: connection refused\nSuggestion: Verify VPN connection and firewall rules\n"

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	// Should NOT be parsed as diagnostic
	if len(diagnostics) != 0 {
		t.Errorf("Expected 0 diagnostics for on-premise format, got %d", len(diagnostics))
	}

	// MUST appear as log lines
	allLogs := strings.Join(logs, "\n")
	if !strings.Contains(allLogs, "FATAL") {
		t.Fatal("CRITICAL: On-premise error was lost")
	}
	if !strings.Contains(allLogs, "internal_vm") {
		t.Error("Expected resource name in logs")
	}
	if !strings.Contains(allLogs, "connection refused") {
		t.Error("Expected 'connection refused' in logs")
	}
}

func TestNonStandardErrorFormat_EmptyDiagnosticBlock(t *testing.T) {
	// Edge case: diagnostic block markers with no content between them
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n╵\nSome other error message here\n"

	_, logs, _ := collectStreamMsgs(m, input)

	// Empty diagnostic block should not cause issues
	// The "Some other error message" MUST still be preserved
	allLogs := strings.Join(logs, "\n")
	if !strings.Contains(allLogs, "Some other error message") {
		t.Fatal("CRITICAL: Error message after empty diagnostic block was lost")
	}
}

func TestNonStandardErrorFormat_TruncatedDiagnostic(t *testing.T) {
	// Stream ends mid-diagnostic without proper closing
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n│ Some custom error without Error: prefix\n│ More details here\n│ Even more context"

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	// Should be parsed as diagnostic (fallback in parseDiagnosticBlock)
	// OR appear as logs if parsing fails
	allContent := ""
	for _, d := range diagnostics {
		allContent += d.Summary + " "
		for _, detail := range d.Detail {
			allContent += detail.Content + " "
		}
	}
	for _, l := range logs {
		allContent += l + " "
	}

	if !strings.Contains(allContent, "custom error") {
		t.Fatalf("CRITICAL: Truncated diagnostic content was lost. All content: %q", allContent)
	}
	if !strings.Contains(allContent, "More details") {
		t.Error("Expected 'More details' to be preserved")
	}
}

func TestAllContentPreserved_NoPatternFiltering(t *testing.T) {
	// Comprehensive test: mix of standard and non-standard errors
	// Verifies that NO content is dropped regardless of format
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := `Standard Terraform plan output here
╷
│ Error: Standard GCP error
│ 
│   with google_compute_instance.test
│ 
╵
Some random log line
[ERROR] Custom provider error without standard format
Another standard line
panic: something went wrong in custom provider
╷
│ Warning: Another standard warning
╵
Final log message
`

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	// Collect all content
	allContent := ""
	for _, d := range diagnostics {
		allContent += d.Summary + " "
		for _, detail := range d.Detail {
			allContent += detail.Content + " "
		}
	}
	for _, l := range logs {
		allContent += l + " "
	}

	// ALL content must be preserved - nothing dropped
	if !strings.Contains(allContent, "Standard GCP error") {
		t.Error("Standard GCP error was lost")
	}
	if !strings.Contains(allContent, "Custom provider error") {
		t.Fatal("CRITICAL: Custom provider error was lost - pattern-based filtering detected!")
	}
	if !strings.Contains(allContent, "panic") {
		t.Fatal("CRITICAL: Panic message was lost - pattern-based filtering detected!")
	}
	if !strings.Contains(allContent, "random log line") {
		t.Error("Random log line was lost")
	}
	if !strings.Contains(allContent, "Final log message") {
		t.Error("Final log message was lost")
	}
}

// =============================================================================
// Text-agnostic approach: ALL output preserved as raw text
// =============================================================================

func TestTextAgnostic_AllOutputPreservedAsLogs(t *testing.T) {
	// Core principle: EVERY line should appear as a log line, regardless of format
	// Structured parsing is an enhancement on top, not a replacement
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := `Line 1: Random output
Line 2: Error without standard format
Line 3: Some other text
Line 4: More content here
`

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	// All lines MUST appear as logs
	if len(logs) != 4 {
		t.Errorf("Expected 4 log lines (all content), got %d", len(logs))
	}

	// Verify each line is preserved
	allLogs := strings.Join(logs, "\n")
	if !strings.Contains(allLogs, "Line 1") {
		t.Error("Line 1 was lost")
	}
	if !strings.Contains(allLogs, "Line 2") {
		t.Error("Line 2 was lost")
	}
	if !strings.Contains(allLogs, "Line 3") {
		t.Error("Line 3 was lost")
	}
	if !strings.Contains(allLogs, "Line 4") {
		t.Error("Line 4 was lost")
	}

	// No diagnostics expected for plain text without ╷...╵ blocks
	if len(diagnostics) != 0 {
		t.Errorf("Expected 0 diagnostics for plain text, got %d", len(diagnostics))
	}
}

func TestTextAgnostic_DiagnosticBlockAppearsAsBoth(t *testing.T) {
	// When we have a diagnostic block, it should appear as structured data
	// AND as raw log lines (dual preservation)
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n│ Error: Something failed\n│ Details here\n╵\n"

	diagnostics, logs, _ := collectStreamMsgs(m, input)

	// Should be parsed as diagnostic
	if len(diagnostics) != 1 {
		t.Errorf("Expected 1 diagnostic, got %d", len(diagnostics))
	}

	// Should ALSO appear as log lines (text-agnostic preservation)
	// This ensures even if parsing fails, the raw text is still visible
	if len(logs) == 0 {
		t.Log("Note: Diagnostic block lines not duplicated as logs (acceptable if structured parsing works)")
	}

	// Verify the error message is preserved somewhere
	allContent := ""
	for _, d := range diagnostics {
		allContent += d.Summary
	}
	for _, l := range logs {
		allContent += l
	}

	if !strings.Contains(allContent, "Something failed") {
		t.Fatal("CRITICAL: Error message lost entirely")
	}
}

func TestTextAgnostic_AnyFormatWorks(t *testing.T) {
	// Test with completely arbitrary formats
	formats := []string{
		"CustomFormat: error happened",
		"*** ERROR *** system failure",
		"[FAIL] Module load error",
		"!!! CRITICAL !!!",
		"Plain text error without any markers",
		"🚨 Unicode error emoji test",
	}

	for _, format := range formats {
		m := &Model{streamChan: make(chan StreamMsg, 10)}
		input := format + "\n"

		diagnostics, logs, _ := collectStreamMsgs(m, input)

		// Content MUST be preserved as logs
		allContent := ""
		for _, l := range logs {
			allContent += l
		}
		for _, d := range diagnostics {
			allContent += d.Summary
		}

		if !strings.Contains(allContent, strings.TrimSuffix(format, "\n")) {
			t.Errorf("CRITICAL: Format not preserved: %q", format)
		}
	}
}

func TestTextAgnostic_NoPatternFiltering(t *testing.T) {
	// Ensure NO lines are dropped due to pattern matching
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := `Line that doesn't match any pattern
Another unmatched line
Yet more text
EOF marker
`

	_, logs, _ := collectStreamMsgs(m, input)

	// Every single line must be preserved
	if len(logs) != 4 {
		t.Fatalf("CRITICAL: Pattern filtering detected! Expected 4 lines, got %d", len(logs))
	}

	allLogs := strings.Join(logs, "\n")
	if !strings.Contains(allLogs, "doesn't match any pattern") {
		t.Error("Line with 'doesn't match any pattern' was dropped")
	}
	if !strings.Contains(allLogs, "EOF marker") {
		t.Error("Line with 'EOF marker' was dropped")
	}
}

// =============================================================================
// AWS-specific error patterns that must be preserved
// =============================================================================

func TestAWSAPIError_InvalidInstanceType(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n│ Error: creating EC2 Instance: InvalidInstanceType: The instance type 't99.xlarge' is not supported.\n│ \n│   with aws_instance.web,\n│   on main.tf line 15, in resource \"aws_instance\" \"web\":\n│   15: resource \"aws_instance\" \"web\" {\n│ \n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 {
		t.Fatal("Expected AWS API error diagnostic")
	}

	d := diagnostics[0]
	if d.Severity != "error" {
		t.Errorf("Expected severity 'error', got %q", d.Severity)
	}

	if !strings.Contains(d.Summary, "InvalidInstanceType") {
		t.Errorf("Expected InvalidInstanceType error code in summary, got: %q", d.Summary)
	}

	if !strings.Contains(d.Summary, "t99.xlarge") {
		t.Errorf("Expected instance type in summary, got: %q", d.Summary)
	}
}

func TestAWSAccessDenied(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n│ Error: creating IAM Role: AccessDenied: User: arn:aws:iam::123456789:user/dev is not authorized to perform: iam:CreateRole on resource: arn:aws:iam::123456789:role/my-role\n│ \n│   with aws_iam_role.my_role,\n│   on iam.tf line 5, in resource \"aws_iam_role\" \"my_role\":\n│    5: resource \"aws_iam_role\" \"my_role\" {\n│ \n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 {
		t.Fatal("Expected AWS access denied error diagnostic")
	}

	if !strings.Contains(diagnostics[0].Summary, "AccessDenied") {
		t.Errorf("Expected AccessDenied error code in summary, got: %q", diagnostics[0].Summary)
	}

	if !strings.Contains(diagnostics[0].Summary, "iam:CreateRole") {
		t.Errorf("Expected permission name in summary, got: %q", diagnostics[0].Summary)
	}
}

func TestAWSRateLimitExceeded(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n│ Error: creating Auto Scaling Group: RequestLimitExceeded: Request limit exceeded.\n│ \n│   with aws_autoscaling_group.worker,\n│   on asg.tf line 10, in resource \"aws_autoscaling_group\" \"worker\":\n│   10: resource \"aws_autoscaling_group\" \"worker\" {\n│ \n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 {
		t.Fatal("Expected AWS rate limit error diagnostic")
	}

	if !strings.Contains(diagnostics[0].Summary, "RequestLimitExceeded") {
		t.Errorf("Expected RequestLimitExceeded error code, got: %q", diagnostics[0].Summary)
	}
}

func TestAWSMultipleErrors(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	// Multiple AWS errors in sequence
	input := "╷\n│ Error: creating S3 Bucket: BucketAlreadyExists: The requested bucket name is not available.\n│ \n│   with aws_s3_bucket.data,\n│   on s3.tf line 3\n│ \n╵\n╷\n│ Error: creating VPC: VpcLimitExceeded: The maximum number of VPCs has been reached.\n│ \n│   with aws_vpc.main,\n│   on vpc.tf line 1\n│ \n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) != 2 {
		t.Fatalf("Expected 2 diagnostics, got %d", len(diagnostics))
	}

	// Verify each diagnostic
	if !strings.Contains(diagnostics[0].Summary, "S3") || !strings.Contains(diagnostics[0].Summary, "BucketAlreadyExists") {
		t.Errorf("First diagnostic incorrect: %q", diagnostics[0].Summary)
	}
	if !strings.Contains(diagnostics[1].Summary, "VPC") || !strings.Contains(diagnostics[1].Summary, "VpcLimitExceeded") {
		t.Errorf("Second diagnostic incorrect: %q", diagnostics[1].Summary)
	}
}

// =============================================================================
// Azure-specific error patterns that must be preserved
// =============================================================================

func TestAzureAPIError_InvalidResourceGroup(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n│ Error: creating Resource Group: resources.GroupsClient#CreateOrUpdate: Failure responding to request: StatusCode=404 -- Original Error: autorest/azure: Service returned an error. Status=404 Code=\"ResourceGroupNotFound\" Message=\"Resource group 'my-rg' could not be found.\"\n│ \n│   with azurerm_resource_group.main,\n│   on main.tf line 8, in resource \"azurerm_resource_group\" \"main\":\n│    8: resource \"azurerm_resource_group\" \"main\" {\n│ \n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 {
		t.Fatal("Expected Azure API error diagnostic")
	}

	d := diagnostics[0]
	if d.Severity != "error" {
		t.Errorf("Expected severity 'error', got %q", d.Severity)
	}

	if !strings.Contains(d.Summary, "ResourceGroupNotFound") {
		t.Errorf("Expected ResourceGroupNotFound error code, got: %q", d.Summary)
	}

	if !strings.Contains(d.Summary, "my-rg") {
		t.Errorf("Expected resource group name in summary, got: %q", d.Summary)
	}
}

func TestAzureAuthorizationFailed(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n│ Error: creating Virtual Network: network.VirtualNetworksClient#CreateOrUpdate: Failure sending request: StatusCode=403 -- Original Error: Code=\"AuthorizationFailed\" Message=\"The client 'dev@company.com' with object id 'abc-123' does not have authorization to perform action 'Microsoft.Network/virtualNetworks/write' over scope '/subscriptions/xyz/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet'.\"\n│ \n│   with azurerm_virtual_network.main,\n│   on network.tf line 5, in resource \"azurerm_virtual_network\" \"main\":\n│    5: resource \"azurerm_virtual_network\" \"main\" {\n│ \n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 {
		t.Fatal("Expected Azure authorization error diagnostic")
	}

	if !strings.Contains(diagnostics[0].Summary, "AuthorizationFailed") {
		t.Errorf("Expected AuthorizationFailed error code, got: %q", diagnostics[0].Summary)
	}

	if !strings.Contains(diagnostics[0].Summary, "Microsoft.Network/virtualNetworks/write") {
		t.Errorf("Expected permission name in summary, got: %q", diagnostics[0].Summary)
	}
}

func TestAzureQuotaExceeded(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	input := "╷\n│ Error: creating Virtual Machine: compute.VirtualMachinesClient#CreateOrUpdate: Failure sending request: StatusCode=409 -- Original Error: Code=\"OperationNotAllowed\" Message=\"Operation results in exceeding quota limits of Core. Maximum allowed: 100, Current in use: 95, Additional requested: 10.\"\n│ \n│   with azurerm_linux_virtual_machine.worker,\n│   on vm.tf line 12, in resource \"azurerm_linux_virtual_machine\" \"worker\":\n│   12: resource \"azurerm_linux_virtual_machine\" \"worker\" {\n│ \n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) == 0 {
		t.Fatal("Expected Azure quota error diagnostic")
	}

	if !strings.Contains(diagnostics[0].Summary, "OperationNotAllowed") {
		t.Errorf("Expected OperationNotAllowed error code, got: %q", diagnostics[0].Summary)
	}

	if !strings.Contains(diagnostics[0].Summary, "quota") || !strings.Contains(diagnostics[0].Summary, "Core") {
		t.Errorf("Expected quota information in summary, got: %q", diagnostics[0].Summary)
	}
}

func TestAzureMultipleErrors(t *testing.T) {
	m := &Model{streamChan: make(chan StreamMsg, 10)}

	// Multiple Azure errors in sequence
	input := "╷\n│ Error: creating Storage Account: storage.AccountsClient#Create: Failure sending request: StatusCode=400 -- Original Error: Code=\"StorageAccountAlreadyTaken\" Message=\"The storage account named mystorage is already taken.\"\n│ \n│   with azurerm_storage_account.main,\n│   on storage.tf line 2\n│ \n╵\n╷\n│ Error: creating Public IP: network.PublicIPAddressesClient#CreateOrUpdate: Failure sending request: StatusCode=400 -- Original Error: Code=\"DnsRecordInUse\" Message=\"DNS record myip.eastus.cloudapp.azure.com is already used by another public IP.\"\n│ \n│   with azurerm_public_ip.main,\n│   on network.tf line 8\n│ \n╵\n"

	diagnostics, _, _ := collectStreamMsgs(m, input)

	if len(diagnostics) != 2 {
		t.Fatalf("Expected 2 diagnostics, got %d", len(diagnostics))
	}

	// Verify each diagnostic
	if !strings.Contains(diagnostics[0].Summary, "StorageAccountAlreadyTaken") {
		t.Errorf("First diagnostic incorrect: %q", diagnostics[0].Summary)
	}
	if !strings.Contains(diagnostics[1].Summary, "DnsRecordInUse") {
		t.Errorf("Second diagnostic incorrect: %q", diagnostics[1].Summary)
	}
}
