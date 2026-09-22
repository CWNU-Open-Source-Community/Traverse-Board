package application

import (
	"context"
)

// ReadScreenshot only reads an immutable, completed screenshot call from this
// exact manager session. The caller cannot choose a filesystem path.
func (s *AgentBrowserService) ReadScreenshot(ctx context.Context, runID, sessionID, artifactLocator string) ([]byte, string, error) {
	if s == nil {
		return nil, "", agentBrowserUnavailable("browser screenshot unavailable")
	}
	s.mu.Lock()
	slot := s.sessions[sessionID]
	callID := ""
	if slot != nil && slot.authority.RunID == runID {
		callID = slot.screenshotCalls[artifactLocator]
	}
	s.mu.Unlock()
	if callID == "" {
		return nil, "", agentBrowserUnavailable("screenshot does not belong to this browser session")
	}
	st, ok := s.store.(agentBrowserApprovalStore)
	if !ok {
		return nil, "", agentBrowserUnavailable("browser screenshot ledger unavailable")
	}
	call, _, e := st.GetAgentBrowserCall(ctx, runID, callID)
	if e != nil {
		return nil, "", e
	}
	image, e := readCompletedBrowserScreenshot(ctx, s.store, call, true)
	if e != nil {
		return nil, "", e
	}
	return image.Image.Data, image.Image.SHA256, nil
}
