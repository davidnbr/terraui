package main

import (
	"strings"
	"testing"
)

// TestAWSProviderErrors validates AWS-specific error formats
func TestAWSProviderErrors(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		expectedError  string
		expectedDetail string
	}{
		{
			name: "AWS API Error - InvalidInstanceID",
			input: "╷\n│ Error: Error launching source instance: InvalidInstanceID.NotFound\n│ \n│   with aws_instance.example,\n│   on main.tf line 10:\n│   10: resource \"aws_instance\" \"example\" {\n│ \n│ status code: 400\n╵\n",
			expectedError:  "InvalidInstanceID.NotFound",
			expectedDetail: "status code: 400",
		},
		{
			name: "AWS Access Denied",
			input: "╷\n│ Error: error creating EC2 Instance: UnauthorizedOperation\n│ \n│   with aws_instance.web,\n│   on main.tf line 15:\n│   15: resource \"aws_instance\" \"web\" {\n│ \n│ status code: 403\n╵\n",
			expectedError:  "UnauthorizedOperation",
			expectedDetail: "status code: 403",
		},
		{
			name: "AWS Rate Limiting",
			input: "╷\n│ Error: RequestLimitExceeded: Request limit exceeded.\n│ \n│   with aws_instance.batch[0],\n│   on main.tf line 25:\n│ \n│ status code: 503\n╵\n",
			expectedError:  "RequestLimitExceeded",
			expectedDetail: "Request limit exceeded",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{streamChan: make(chan StreamMsg, 10)}
			diagnostics, logs, _ := collectStreamMsgs(m, tc.input)

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

			if !strings.Contains(allContent, tc.expectedError) {
				t.Errorf("Expected error containing %q, got:\n%s", tc.expectedError, allContent)
			}
			if !strings.Contains(allContent, tc.expectedDetail) {
				t.Errorf("Expected detail containing %q", tc.expectedDetail)
			}
		})
	}
}

// TestGCPProviderErrors validates GCP-specific error formats
func TestGCPProviderErrors(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		expectedError  string
		expectedDetail string
	}{
		{
			name: "GCP API Error - 400 Bad Request",
			input: "╷\n│ Error: Error creating instance: googleapi: Error 400\n│ \n│   with google_compute_instance.example,\n│   on main.tf line 10:\n│   10:   name = \"invalid_name\"\n╵\n",
			expectedError:  "Error 400",
			expectedDetail: "invalid_name",
		},
		{
			name: "GCP Permission Denied",
			input: "╷\n│ Error: Error creating Bucket: googleapi: Error 403\n│ \n│   with google_storage_bucket.data,\n│   on storage.tf line 1:\n╵\n",
			expectedError:  "Error 403",
			expectedDetail: "google_storage_bucket",
		},
		{
			name: "GCP Quota Exceeded",
			input: "╷\n│ Error: Error creating instance: googleapi: Error 403: Quota exceeded\n│ \n│   with google_compute_instance.vm[5],\n│   on vms.tf line 45:\n╵\n",
			expectedError:  "Quota exceeded",
			expectedDetail: "google_compute_instance",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{streamChan: make(chan StreamMsg, 10)}
			diagnostics, logs, _ := collectStreamMsgs(m, tc.input)

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

			if !strings.Contains(allContent, tc.expectedError) {
				t.Errorf("Expected error containing %q", tc.expectedError)
			}
			if !strings.Contains(allContent, tc.expectedDetail) {
				t.Errorf("Expected detail containing %q", tc.expectedDetail)
			}
		})
	}
}

// TestAzureProviderErrors validates Azure-specific error formats
func TestAzureProviderErrors(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		expectedError  string
		expectedDetail string
	}{
		{
			name: "Azure Resource Not Found",
			input: "╷\n│ Error: StatusCode=404 Code=ResourceGroupNotFound\n│ \n│   with data.azurerm_resource_group.example,\n│   on main.tf line 5:\n╵\n",
			expectedError:  "ResourceGroupNotFound",
			expectedDetail: "azurerm_resource_group",
		},
		{
			name: "Azure Authorization Failed",
			input: "╷\n│ Error: StatusCode=403 Code=AuthorizationFailed\n│ \n│   with azurerm_resource_group.example,\n│   on main.tf line 10:\n╵\n",
			expectedError:  "AuthorizationFailed",
			expectedDetail: "azurerm_resource_group",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{streamChan: make(chan StreamMsg, 10)}
			diagnostics, logs, _ := collectStreamMsgs(m, tc.input)

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

			if !strings.Contains(allContent, tc.expectedError) {
				t.Errorf("Expected error containing %q", tc.expectedError)
			}
			if !strings.Contains(allContent, tc.expectedDetail) {
				t.Errorf("Expected detail containing %q", tc.expectedDetail)
			}
		})
	}
}

// TestTerraformPlanErrors validates errors specific to terraform plan
func TestTerraformPlanErrors(t *testing.T) {
	testCases := []struct {
		name          string
		input         string
		expectedError string
	}{
		{
			name:          "Plan Syntax Error",
			input:         "╷\n│ Error: Argument or block definition required\n│ \n│   on main.tf line 5:\n│    5:   invalid syntax\n╵\n",
			expectedError: "Argument or block definition required",
		},
		{
			name:          "Plan Failed",
			input:         "╷\n│ Error: Planning failed\n│ \n│ Terraform encountered an error\n╵\n",
			expectedError: "Planning failed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{streamChan: make(chan StreamMsg, 10)}
			diagnostics, logs, _ := collectStreamMsgs(m, tc.input)

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

			if !strings.Contains(allContent, tc.expectedError) {
				t.Errorf("Expected error containing %q", tc.expectedError)
			}
		})
	}
}

// TestTerraformApplyErrors validates errors specific to terraform apply
func TestTerraformApplyErrors(t *testing.T) {
	testCases := []struct {
		name          string
		input         string
		expectedError string
	}{
		{
			name:          "Apply State Lock Error",
			input:         "╷\n│ Error: Error locking state: Error acquiring the state lock\n│ \n│ state blob already locked\n╵\n",
			expectedError: "Error acquiring the state lock",
		},
		{
			name:          "Apply Resource Failed",
			input:         "╷\n│ Error: Apply complete, but resources failed to create\n│ \n│   with aws_instance.example,\n│   on main.tf line 10:\n╵\n",
			expectedError: "resources failed to create",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{streamChan: make(chan StreamMsg, 10)}
			diagnostics, logs, _ := collectStreamMsgs(m, tc.input)

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

			if !strings.Contains(allContent, tc.expectedError) {
				t.Errorf("Expected error containing %q", tc.expectedError)
			}
		})
	}
}

// TestTerraformInitErrors validates errors specific to terraform init
func TestTerraformInitErrors(t *testing.T) {
	testCases := []struct {
		name          string
		input         string
		expectedError string
	}{
		{
			name:          "Init Backend Error",
			input:         "╷\n│ Error: Failed to get existing workspaces: S3 bucket does not exist\n│ \n│   with backend \"s3\",\n│   on main.tf line 5:\n╵\n",
			expectedError: "S3 bucket does not exist",
		},
		{
			name:          "Init Provider Error",
			input:         "╷\n│ Error: Failed to query available provider packages\n│ \n│ Could not retrieve the list of available versions\n╵\n",
			expectedError: "Failed to query available provider packages",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{streamChan: make(chan StreamMsg, 10)}
			diagnostics, logs, _ := collectStreamMsgs(m, tc.input)

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

			if !strings.Contains(allContent, tc.expectedError) {
				t.Errorf("Expected error containing %q", tc.expectedError)
			}
		})
	}
}
