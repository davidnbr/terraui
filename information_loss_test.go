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
