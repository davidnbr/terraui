package main

import (
	"context"
	"strings"
)

// collectStreamMsgs reads all messages from a stream until Done, collecting
// diagnostics, log lines, and resources separately.
func collectStreamMsgs(m *Model, input string) (diagnostics []*Diagnostic, logs []string, resources []*ResourceChange, receivedContent bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go m.readInputStream(ctx, strings.NewReader(input))

	for {
		msg, ok := <-m.streamChan
		if !ok || msg.Done {
			receivedContent = msg.ReceivedContent
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
